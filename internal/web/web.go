// Package web serves the pages of the Directory under "/-/".
//
// In L1 that is one placeholder page, enough to see that the server is up and
// which version it is. The real pages arrive in L3. Like httpd, this package
// holds no rules about links.
package web

import (
	"bytes"
	"embed"
	"html/template"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static
var staticFS embed.FS

const (
	rootPath     = "/-/"
	staticPrefix = "/-/static/"
)

// Handler serves the placeholder page at "/-/" and its stylesheet under
// "/-/static/", and answers 404 for anything else.
//
// Templates and styles are embedded and contain no inline script or style,
// so the pages work under the server's content security policy. Templates are
// parsed here, once: a broken template is a build mistake and should stop the
// program at startup, not on the first request.
func Handler(version string) http.Handler {
	h := &handler{
		version: version,
		pages:   template.Must(template.ParseFS(templateFS, "templates/*.html")),
	}
	return h
}

type handler struct {
	version string
	pages   *template.Template
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "405 Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	switch path := r.URL.Path; {
	case path == rootPath:
		h.page(w, r, "placeholder.html", struct{ Version string }{h.version})
	case strings.HasPrefix(path, staticPrefix):
		serveStatic(w, r, strings.TrimPrefix(path, staticPrefix))
	default:
		http.NotFound(w, r)
	}
}

// page renders a template into memory first, so a failure becomes a clean 500
// instead of half a page.
func (h *handler) page(w http.ResponseWriter, r *http.Request, name string, data any) {
	var buf bytes.Buffer
	if err := h.pages.ExecuteTemplate(&buf, name, data); err != nil {
		http.Error(w, "500 Internal Server Error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(buf.Len()))
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(buf.Bytes())
	}
}

// serveStatic serves one embedded file. Directories are not found, so there
// is no generated listing; fs.ValidPath refuses "..", so nothing outside
// static/ can be named.
func serveStatic(w http.ResponseWriter, r *http.Request, name string) {
	full := "static/" + name
	if name == "" || !fs.ValidPath(full) {
		http.NotFound(w, r)
		return
	}
	info, err := fs.Stat(staticFS, full)
	if err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}
	http.ServeFileFS(w, r, staticFS, full)
}
