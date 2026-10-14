// Package httpd is the HTTP face of Ogre: the listeners, the request guards
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

	"github.com/tfrydrychewicz/ogre/internal/app"
	"github.com/tfrydrychewicz/ogre/internal/linkmodel"
	"github.com/tfrydrychewicz/ogre/internal/web"
)

// Service is what the HTTP layer needs from the use cases. It is an interface
// so the handlers can be tested against a fake, without a database.
type Service interface {
	Resolve(escapedPath string) app.Decision
	CreateLink(ctx context.Context, actor app.Actor, d app.Draft) (linkmodel.Link, error)
	GetLink(ctx context.Context, actor app.Actor, id int64) (linkmodel.Link, error)
	// UpdateLink replaces the link's description and routes. version is the
	// one the caller last read; a stale one is refused.
	UpdateLink(ctx context.Context, actor app.Actor, id int64, d app.Draft, version int) (linkmodel.Link, error)
	// DeleteLink removes the link. version 0 means "whatever it is now".
	DeleteLink(ctx context.Context, actor app.Actor, id int64, version int) error
	// Validate checks a draft by the rules of saving and writes nothing.
	Validate(ctx context.Context, actor app.Actor, req app.ValidateRequest) app.ValidateResult
	ListLinks(ctx context.Context, actor app.Actor) ([]linkmodel.Link, error)
	Owner(ctx context.Context, l linkmodel.Link) string
	// CanEdit is the permission rule behind a link's "canEdit". It is asked
	// of the Service so that this package holds no rule of its own.
	CanEdit(actor app.Actor, l linkmodel.Link) bool

	// The read queries of the web UI.

	// Search ranks links for a non-blank query, best first (SRCH-2).
	Search(ctx context.Context, actor app.Actor, q string, limit int) ([]app.SearchResult, error)
	// LinkByName finds a link by name, ignoring case, and says whether the
	// actor may edit it; app.ErrNotFound when there is none.
	LinkByName(ctx context.Context, actor app.Actor, name string) (linkmodel.Link, bool, error)
	// YourLinks lists the actor's links, most recently changed first.
	YourLinks(ctx context.Context, actor app.Actor, limit int) ([]linkmodel.Link, error)
	// RecentLinks lists the newest links, most recently created first.
	RecentLinks(ctx context.Context, limit int) ([]linkmodel.Link, error)
	// OwnerName is the display name of a link owner; "" when unknown.
	OwnerName(ctx context.Context, ownerID int64) string
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
	// Web renders the pages and serves the static files. Nil means the
	// files compiled into the binary, with Version in the footer.
	Web *web.Renderer
}

// Server routes requests and owns the listeners.
type Server struct {
	svc      Service
	opts     Options
	log      *slog.Logger
	hosts    hostList
	web      *web.Renderer
	internal http.Handler // everything under "/-/"
}

// New builds a Server. Nothing listens until ListenAndServe is called, so
// Handler can be used on its own in tests.
func New(svc Service, opts Options) *Server {
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.DiscardHandler)
	}
	if opts.Web == nil {
		// The embedded templates were parsed by the tests of package web;
		// failing here is a build mistake, as with template.Must.
		r, err := web.New(web.Options{Version: opts.Version})
		if err != nil {
			panic("httpd: the embedded pages do not load: " + err.Error())
		}
		opts.Web = r
	}
	s := &Server{
		svc:   svc,
		opts:  opts,
		log:   opts.Logger,
		hosts: newHostList(opts.Addr, opts.AllowHosts),
		web:   opts.Web,
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
				"If the name is yours, start ogre with --allow-host <name>.\n")
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
		s.serveReserved(w, r, path)
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

// serveInternal handles everything under "/-/": the cross-site guards first
// (for reads of the API, and for every write), then the API and health check,
// then the UI.
func (s *Server) serveInternal(w http.ResponseWriter, r *http.Request, path string, entry *logEntry) {
	// The inner ServeMux would answer an unclean path with a redirect to the
	// cleaned one, and "/-/../x" cleans to the go link "/x". Nothing under
	// "/-/" needs such paths, so they are simply not found.
	if !isCleanPath(path) {
		if strings.HasPrefix(path, internalPrefix+"api/") {
			writeError(w, r, http.StatusNotFound, codeNotFound, "There is nothing at this address.", nil)
		} else {
			s.notFoundPage(w, r)
		}
		return
	}
	// A read made by a page of another site, as a fetch, an image or a
	// frame, is refused like the same kind of request for a go link
	// (serveResolver). Such a page could never see the answer, since no CORS
	// header is sent; but the request alone makes the server do the work, and
	// for the resolve endpoint that is as much as a go link costs, without
	// the user seeing anything happen. Pages are refused too: whether
	// /-/links/<name> is a page or a 404 tells which links exist, the very
	// thing the resolver's refusal hides. A page opened in a tab from another
	// site, and everything without Fetch Metadata headers (curl, the command
	// line, other servers), is served as before. The health check and the
	// static files are exempt: they cost nothing and say nothing about the
	// links.
	if isRead(r.Method) && path != healthzPath && !strings.HasPrefix(path, web.StaticPrefix) && isCrossSiteSubresource(r) {
		if strings.HasPrefix(path, apiPrefix) {
			writeError(w, r, http.StatusForbidden, codeCrossSite,
				"This request came from another site, so it was refused.", nil)
		} else {
			writeText(w, r, http.StatusForbidden,
				"403 Forbidden: the Directory's pages can be opened, but not loaded into a page of another site.\n")
		}
		return
	}
	if !isSafeMethod(r.Method) {
		// Every method that is not a read is guarded, known to the router or
		// not: POST, PATCH and DELETE alike, and PUT although nothing accepts
		// it.
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
