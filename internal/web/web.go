// Package web holds the pages of the Directory: the templates, the
// stylesheets and browser modules, and the code that renders the one and
// serves the others under "/-/static/".
//
// Like httpd, this package holds no rules about links. Templates and browser
// code show what the server says (tokens, problems with offsets, reasons);
// they never parse a pattern or judge a value themselves (NFR-22).
//
// # Templates
//
// templates/layout.html is the page shell. It defines "layout", which every
// page is rendered through, and the blocks a page fills:
//
//   - "title": the document title; by default Base.Title followed by "Ogre"
//   - "head": extra tags in <head>, such as a page's stylesheet or module
//   - "main": the page's content, inside <main id="main">
//
// A page that has a search box of its own (home, the miss page) sets
// Base.HideHeaderSearch, and the header leaves out its own.
//
// Every other file templates/<page>.html is a page, rendered by
// Render(w, status, "<page>", data). Files whose name starts with "_" are
// partials: they are parsed into every page and may define templates any
// page can call. Page data must embed Base.
//
// Templates may call asset "css/site.css", which returns the content-hash
// URL of a file below static/ and fails for a file that does not exist.
//
// # Static files
//
// Everything below static/ is served at StaticPrefix + <hash> + "/" + path,
// where the hash covers every static file (see assetSet), with a cache
// lifetime of a year. No inline script or style is possible under
// ContentSecurityPolicy, so all of it lives here.
//
// # --dev
//
// With Options.Dir set, templates and static files are read from that
// directory on every request, so editing them needs no restart. SourceDir
// finds the directory in a source checkout.
package web

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
)

// embedded holds the templates and static files compiled into the binary.
// "all:" keeps the partials: a plain directory pattern leaves out files whose
// name starts with "_".
//
//go:embed all:templates static
var embedded embed.FS

// Base is the part of every page's data that the layout reads. A page's own
// data type embeds it.
type Base struct {
	// Version is the Directory's version, shown in the footer.
	Version string
	// Title is the page's name, the first part of the document title.
	Title string
	// Query fills the header's search box, so a search results page shows
	// what was searched for.
	Query string
	// Current names the navigation entry of this page ("new" or "setup"),
	// which the header marks with aria-current. Empty marks none.
	Current string
	// Dev is true in --dev mode; the footer says so.
	Dev bool
	// HideHeaderSearch leaves the header's search form out, for a page that
	// has its own search box: two with the same label would confuse.
	HideHeaderSearch bool
}

// ErrorPage is the data of templates/error.html, the page of a 404, 403 or
// 500 inside "/-/", and of any short message page. Heading and Message are
// plain text, escaped like everything else; neither should repeat anything
// from the request.
type ErrorPage struct {
	Base
	Status  int
	Heading string
	Message string
}

// Options configures a Renderer.
type Options struct {
	// Version is shown in every page's footer.
	Version string
	// Dir, when set, is a directory laid out like this package (templates/
	// and static/), read on every request instead of the files compiled into
	// the binary: the --dev mode. SourceDir finds it in a source checkout.
	Dir string
}

// Renderer renders pages and serves static files.
//
// Without --dev, templates and static files are read and parsed once, in
// New: a broken template is a build mistake and stops the program at
// startup, not on the first request. With --dev they are read again on every
// request, and a mistake shows up as an error on the page that has it.
type Renderer struct {
	version string
	dev     bool
	files   fs.FS // the root holding templates/ and static/

	// Fixed when not in --dev mode.
	assets *assetSet
	pages  map[string]*template.Template

	devMu sync.Mutex // in --dev mode, one reload at a time
}

// New builds a Renderer, from the embedded files or from opts.Dir.
func New(opts Options) (*Renderer, error) {
	r := &Renderer{version: opts.Version, files: embedded}
	if opts.Dir != "" {
		r.dev = true
		r.files = os.DirFS(opts.Dir)
	}
	// Loaded now even in --dev mode, so a wrong directory is reported at
	// startup.
	assets, pages, err := load(r.files)
	if err != nil {
		if r.dev {
			return nil, fmt.Errorf("--dev: %s: %w", opts.Dir, err)
		}
		return nil, err
	}
	if !r.dev {
		r.assets, r.pages = assets, pages
	}
	return r, nil
}

// Dev reports whether the Renderer reads its files from disk.
func (r *Renderer) Dev() bool { return r.dev }

// Base returns the Base of a page with the given title.
func (r *Renderer) Base(title string) Base {
	return Base{Version: r.version, Title: title, Dev: r.dev}
}

// Render renders templates/<page>.html inside the layout and writes it with
// the given status and the page headers (SetPageHeaders), Content-Length
// included.
//
// The page is rendered into memory first. An error therefore means that
// nothing at all was written, and the caller can still answer with an error
// page; a failure to write to the client is not reported.
//
// Render writes the body for every method. For HEAD, net/http discards it;
// a caller that writes to anything else must drop it itself.
func (r *Renderer) Render(w http.ResponseWriter, status int, page string, data any) error {
	t, err := r.page(page)
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, "layout", data); err != nil {
		return fmt.Errorf("rendering page %q: %w", page, err)
	}
	h := w.Header()
	SetPageHeaders(h)
	h.Set("Content-Length", strconv.Itoa(buf.Len()))
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
	return nil
}

// page returns the parsed template set of one page.
func (r *Renderer) page(name string) (*template.Template, error) {
	pages := r.pages
	if r.dev {
		var err error
		if _, pages, err = r.reload(); err != nil {
			return nil, err
		}
	}
	t, ok := pages[name]
	if !ok {
		return nil, fmt.Errorf("there is no page %q (templates/%s.html)", name, name)
	}
	return t, nil
}

// currentAssets returns the static files: fixed, or read again in --dev mode.
func (r *Renderer) currentAssets() (*assetSet, error) {
	if !r.dev {
		return r.assets, nil
	}
	assets, _, err := r.reload()
	return assets, err
}

func (r *Renderer) reload() (*assetSet, map[string]*template.Template, error) {
	r.devMu.Lock()
	defer r.devMu.Unlock()
	return load(r.files)
}

// load reads the static files, then parses every page against them.
func load(files fs.FS) (*assetSet, map[string]*template.Template, error) {
	assets, err := loadAssets(files)
	if err != nil {
		return nil, nil, err
	}
	pages, err := parsePages(files, assets)
	if err != nil {
		return nil, nil, err
	}
	return assets, pages, nil
}

// layoutFile is the page shell; it is not a page itself.
const layoutFile = "layout.html"

// parsePages parses one template set per page: the layout, every partial,
// and the page's own file, which defines the layout's blocks.
func parsePages(files fs.FS, assets *assetSet) (map[string]*template.Template, error) {
	entries, err := fs.ReadDir(files, "templates")
	if err != nil {
		return nil, fmt.Errorf("reading the templates: %w", err)
	}
	var partials, pageFiles []string
	hasLayout := false
	for _, e := range entries {
		name := e.Name()
		switch {
		case e.IsDir() || path.Ext(name) != ".html" || strings.HasPrefix(name, "."):
		case name == layoutFile:
			hasLayout = true
		case strings.HasPrefix(name, "_"):
			partials = append(partials, "templates/"+name)
		default:
			pageFiles = append(pageFiles, name)
		}
	}
	if !hasLayout {
		return nil, fmt.Errorf("templates/%s is missing", layoutFile)
	}
	funcs := template.FuncMap{"asset": assets.url}
	pages := make(map[string]*template.Template, len(pageFiles))
	for _, file := range pageFiles {
		patterns := append([]string{"templates/" + layoutFile}, partials...)
		patterns = append(patterns, "templates/"+file)
		t, err := template.New(layoutFile).Funcs(funcs).ParseFS(files, patterns...)
		if err != nil {
			return nil, err
		}
		pages[strings.TrimSuffix(file, ".html")] = t
	}
	return pages, nil
}
