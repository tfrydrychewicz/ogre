// Package httpd is the HTTP face of godir: the listeners, the request guards
// of local mode, the routing between the resolver and everything under "/-/",
// and the JSON API.
//
// It contains no rules about links. Every decision comes from the Service
// (package app); this package translates HTTP into Service calls and renders
// what comes back.
package httpd

import (
	"context"
	"log/slog"
	"net/http"
	"strings"

	"github.com/tfrydrychewicz/godir/internal/app"
	"github.com/tfrydrychewicz/godir/internal/linkmodel"
)

// Service is what the HTTP layer needs from the use cases. It is an interface
// so the handlers can be tested against a fake, without a database.
type Service interface {
	Resolve(escapedPath string) app.Decision
	CreateLink(ctx context.Context, actor app.Actor, d app.Draft) (linkmodel.Link, error)
	ListLinks(ctx context.Context, actor app.Actor) ([]linkmodel.Link, error)
	Owner(ctx context.Context, l linkmodel.Link) string
	// CanEdit is the permission rule behind a link's "canEdit". It is asked
	// of the Service so that this package holds no rule of its own.
	CanEdit(actor app.Actor, l linkmodel.Link) bool
}

// Options configures a Server.
type Options struct {
	// Addr is the listen address, "host:port". The host "localhost" means
	// both loopback addresses (see ListenAndServe). The port is also the one
	// the Host check expects.
	Addr string
	// AllowHosts are extra Host header values to accept besides localhost,
	// 127.0.0.1 and [::1]. An entry without a port is accepted on Addr's
	// port only; an entry with a port ("go:80") is accepted on that port.
	AllowHosts []string
	// Version is reported by the meta endpoint.
	Version string
	// Actor is who every request acts as. Local mode has no sign-in, so it
	// is the built-in user; the cloud track replaces this with the session's
	// user.
	Actor app.Actor
	// Logger receives one record per request. Nil discards them.
	Logger *slog.Logger
	// UI serves every path under "/-/" that is not the API or the health
	// check. Nil answers 404.
	UI http.Handler
}

// Server routes requests and owns the listeners.
type Server struct {
	svc      Service
	opts     Options
	log      *slog.Logger
	hosts    hostList
	internal http.Handler // everything under "/-/"
}

// New builds a Server. Nothing listens until ListenAndServe is called, so
// Handler can be used on its own in tests.
func New(svc Service, opts Options) *Server {
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.DiscardHandler)
	}
	if opts.UI == nil {
		opts.UI = http.NotFoundHandler()
	}
	s := &Server{
		svc:   svc,
		opts:  opts,
		log:   opts.Logger,
		hosts: newHostList(opts.Addr, opts.AllowHosts),
	}
	s.internal = s.internalMux()
	return s
}

// internalPrefix is the one reserved path prefix: the web UI, the API and the
// health check live under it, and it can never be a link name because a name
// starts with a letter or a digit.
const internalPrefix = "/-/"

// Handler returns the outermost handler (plan section 3.3).
//
// It dispatches on the raw, still-encoded path and deliberately does not use
// http.ServeMux at the root: ServeMux cleans paths and redirects requests
// containing "//" or "..", which would rewrite a go link before the link
// model sees it.
func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &statusRecorder{ResponseWriter: w}
		entry := &logEntry{}
		defer s.finishRequest(rec, r, entry)()

		setSecurityHeaders(rec.Header())
		s.dispatch(rec, r, entry)
	})
}

// dispatch decides, in the order of plan section 3.3, who answers.
func (s *Server) dispatch(w http.ResponseWriter, r *http.Request, entry *logEntry) {
	if !s.hosts.allows(r.Host, r.TLS != nil) {
		entry.area = areaHostRejected
		writeText(w, r, http.StatusForbidden,
			"403 Forbidden: this Directory does not answer to that host name.\n"+
				"If the name is yours, start godir with --allow-host <name>.\n")
		return
	}

	path := r.URL.EscapedPath()
	switch {
	case path == "/" || path == "" || path == "/-":
		// "/-" can never be a link (names start with a letter or digit), so
		// it is treated as a mistyped "/-/" instead of a miss.
		entry.area = areaHome
		s.redirectHome(w, r)
	case strings.HasPrefix(path, internalPrefix):
		entry.area = areaInternal
		s.serveInternal(w, r, path, entry)
	case isAutoRequest(path):
		entry.area = areaReserved
		w.Header().Set("Cache-Control", "no-store")
		writeText(w, r, http.StatusNotFound, "404 Not Found\n")
	default:
		entry.area = areaResolver
		s.serveResolver(w, r, path, entry)
	}
}

// redirectHome sends the bare address to the web UI (RES-5).
func (s *Server) redirectHome(w http.ResponseWriter, r *http.Request) {
	if !isRead(r.Method) {
		methodNotAllowedText(w, r)
		return
	}
	redirect(w, r, internalPrefix)
}

// serveInternal handles everything under "/-/": write guards first, then the
// API and health check, then the UI.
func (s *Server) serveInternal(w http.ResponseWriter, r *http.Request, path string, entry *logEntry) {
	// The inner ServeMux would answer an unclean path with a redirect to the
	// cleaned one, and "/-/../x" cleans to the go link "/x". Nothing under
	// "/-/" needs such paths, so they are simply not found.
	if !isCleanPath(path) {
		writeError(w, r, http.StatusNotFound, codeNotFound, "There is nothing at this address.", nil)
		return
	}
	if !isSafeMethod(r.Method) {
		if rejected := checkWrite(r); rejected != nil {
			writeError(w, r, rejected.status, rejected.code, rejected.message, nil)
			return
		}
	}
	s.internal.ServeHTTP(w, r)
	entry.route = r.Pattern // set by ServeMux: the matched pattern, never the query
}

// isAutoRequest reports whether the path's first segment is one browsers
// request on their own (favicon.ico, robots.txt, .well-known, ...). The list
// itself is a link-model rule: the same names are refused when a link is
// saved.
func isAutoRequest(escapedPath string) bool {
	first, _, _ := strings.Cut(strings.TrimLeft(escapedPath, "/"), "/")
	return linkmodel.ReservedName(first)
}

// isCleanPath reports whether a path has no empty, "." or ".." segment. A
// trailing slash is fine.
func isCleanPath(path string) bool {
	segments := strings.Split(strings.TrimPrefix(path, "/"), "/")
	for i, seg := range segments {
		switch seg {
		case "":
			if i != len(segments)-1 {
				return false
			}
		case ".", "..":
			return false
		}
	}
	return true
}

// isRead reports whether the method only reads: the resolver and the home
// redirect accept nothing else.
func isRead(method string) bool {
	return method == http.MethodGet || method == http.MethodHead
}
