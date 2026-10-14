package httpd

import (
	"net/http"

	"github.com/tfrydrychewicz/ogre/internal/web"
)

// The pages of the web UI (plan section 3.6). This file holds the routing
// and the helpers every page shares; each page's handler lives in its own
// file (page_home.go, page_link.go, page_form.go, page_setup.go), and the miss
// page is rendered by the resolver (resolver.go).
//
// Every page accepts GET and HEAD only. A page never changes anything: the
// forms and buttons of the UI write through the JSON API from the browser,
// behind the cross-site guards of serveInternal.

// addPageRoutes adds the pages, the static files and the 404 page of "/-/"
// to the internal mux. The API and health check patterns are more specific,
// so they win over the catch-all.
func (s *Server) addPageRoutes(mux *http.ServeMux) {
	mux.Handle(internalPrefix+"{$}", pageOnly(s.homePage))
	mux.Handle(internalPrefix+"links/{name}", pageOnly(s.linkPage))
	mux.Handle(internalPrefix+"links/{name}/edit", pageOnly(s.editPage))
	mux.Handle(internalPrefix+"links/{name}/delete", pageOnly(s.linkDeletePage))
	mux.Handle(internalPrefix+"new", pageOnly(s.newPage))
	mux.Handle(internalPrefix+"setup", pageOnly(s.setupPage))
	mux.HandleFunc(web.StaticPrefix, s.web.ServeStatic)
	// Everything else under "/-/" that is not the API: a page that does not
	// exist.
	mux.HandleFunc(internalPrefix, s.notFoundPage)
}

// pageOnly lets GET and HEAD through to a page and answers 405 to the rest.
func pageOnly(page http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isRead(r.Method) {
			methodNotAllowedText(w, r)
			return
		}
		page(w, r)
	})
}

// render writes a page: templates/<page>.html inside the layout, with the
// page headers (web.SetPageHeaders). data must embed web.Base; build it
// with s.base.
//
// A page that fails to render is logged and answered with a bare 500, so a
// template mistake never shows half a page or anything of the data.
func (s *Server) render(w http.ResponseWriter, r *http.Request, status int, page string, data any) {
	out := w
	if r.Method == http.MethodHead {
		out = headWriter{w}
	}
	if err := s.web.Render(out, status, page, data); err != nil {
		s.log.Error("rendering a page failed", "page", page, "error", err)
		w.Header().Set("Cache-Control", "no-store")
		writeText(w, r, http.StatusInternalServerError, "500 Internal Server Error\n")
	}
}

// base is the web.Base of a page with the given title (the first part of
// the document title). A page sets Query and Current on it as needed.
func (s *Server) base(title string) web.Base {
	return s.web.Base(title)
}

// errorPage renders templates/error.html. heading and message are fixed
// sentences: an error page repeats nothing from the request.
func (s *Server) errorPage(w http.ResponseWriter, r *http.Request, status int, heading, message string) {
	s.render(w, r, status, "error", web.ErrorPage{
		Base: s.base(heading), Status: status, Heading: heading, Message: message,
	})
}

// notFoundPage answers a path under "/-/" that is no page. It does not echo
// the path: there is nothing in it worth showing, and a crafted one could
// only mislead.
func (s *Server) notFoundPage(w http.ResponseWriter, r *http.Request) {
	s.errorPage(w, r, http.StatusNotFound, "Page not found",
		"There is no page at this address in the Directory. Go links are typed without the /-/ part.")
}

// headWriter drops the body of a HEAD response but keeps every header,
// Content-Length included. net/http drops it on its own; this keeps tests
// and any other writer honest.
type headWriter struct{ http.ResponseWriter }

func (headWriter) Write(b []byte) (int, error) { return len(b), nil }

// serveReserved answers the paths browsers request on their own (plan
// section 3.3, linkmodel.ReservedName). They never reach the resolver and are
// never logged as misses. favicon.ico and robots.txt have an answer; every
// other reserved path, and anything below one, is a plain 404 that is not
// cached, since a browser that cached it would keep asking nobody.
func (s *Server) serveReserved(w http.ResponseWriter, r *http.Request, escapedPath string) {
	if !isRead(r.Method) {
		methodNotAllowedText(w, r)
		return
	}
	switch escapedPath {
	case "/favicon.ico":
		s.web.ServeFavicon(w, r)
	case "/robots.txt":
		// A Directory's pages and links are nobody's to index.
		w.Header().Set("Cache-Control", "public, max-age=86400")
		writeText(w, r, http.StatusOK, "User-agent: *\nDisallow: /\n")
	default:
		w.Header().Set("Cache-Control", "no-store")
		writeText(w, r, http.StatusNotFound, "404 Not Found\n")
	}
}
