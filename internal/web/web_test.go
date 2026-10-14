package web

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
)

const testVersion = "1.2.3-test"

func newRenderer(t *testing.T, opts Options) *Renderer {
	t.Helper()
	if opts.Version == "" {
		opts.Version = testVersion
	}
	r, err := New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return r
}

func serve(h http.HandlerFunc, method, target string, headers ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

func render(t *testing.T, r *Renderer, page string, data any) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	if err := r.Render(rec, http.StatusOK, page, data); err != nil {
		t.Fatalf("Render(%q): %v", page, err)
	}
	return rec
}

// The layout's values come from the request (the search box repeats the
// query) and from link data (titles name links). Every one of them must
// arrive as text, in the element and in attributes alike.
func TestLayoutEscapesHostileValues(t *testing.T) {
	hostile := []string{
		`<script>alert(1)</script>`,
		`"><script>alert(1)</script>`,
		`' onfocus='alert(1)' autofocus='`,
		`</title><script>alert(1)</script>`,
		`javascript:alert(1)`,
		`{{.Version}}`,
	}
	r := newRenderer(t, Options{})
	for _, value := range hostile {
		t.Run(value, func(t *testing.T) {
			data := ErrorPage{Base: r.Base(value), Status: 404, Heading: value, Message: value}
			data.Query = value
			body := render(t, r, "error", data).Body.String()

			// Text that looks like a template stays text: it is never
			// executed, so it appears as written.
			// The layout has no script of its own, so any script tag is
			// the payload.
			for _, raw := range []string{"<script", "onfocus='", "</title><", `value=""`} {
				if strings.Contains(body, raw) {
					t.Errorf("%q reached the page unescaped:\n%s", raw, body)
				}
			}
			if strings.Contains(value, "<script>") && !strings.Contains(body, "&lt;script&gt;") {
				t.Errorf("escaped text not found:\n%s", body)
			}
			// The search box holds the query as its value, escaped as an
			// attribute.
			if !strings.Contains(body, `name="q" value="`) {
				t.Errorf("search box lost its value attribute:\n%s", body)
			}
		})
	}
}

func TestVersionIsEscaped(t *testing.T) {
	r := newRenderer(t, Options{Version: `<script>alert(1)</script>`})
	body := render(t, r, "error", ErrorPage{Base: r.Base("x")}).Body.String()
	if strings.Contains(body, "<script>") || !strings.Contains(body, "&lt;script&gt;") {
		t.Errorf("version was not escaped:\n%s", body)
	}
}

func TestRenderWritesThePageWithItsHeaders(t *testing.T) {
	r := newRenderer(t, Options{})
	rec := httptest.NewRecorder()
	if err := r.Render(rec, http.StatusNotFound, "error", ErrorPage{Base: r.Base("Gone"), Status: 404, Heading: "Gone"}); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
	want := map[string]string{
		"Content-Type":            "text/html; charset=utf-8",
		"Cache-Control":           "no-store",
		"Content-Security-Policy": ContentSecurityPolicy,
		"X-Content-Type-Options":  "nosniff",
		"Referrer-Policy":         "same-origin",
		"X-Frame-Options":         "DENY",
		"Content-Length":          strconv.Itoa(rec.Body.Len()),
	}
	for name, value := range want {
		if got := rec.Header().Get(name); got != value {
			t.Errorf("%s = %q, want %q", name, got, value)
		}
	}
	body := rec.Body.String()
	for _, s := range []string{"<title>Gone · Ogre</title>", "Ogre " + testVersion, `<main id="main"`, "Error 404"} {
		if !strings.Contains(body, s) {
			t.Errorf("page lacks %q", s)
		}
	}
}

// An error means nothing was written, so the caller can still answer.
func TestRenderFailureWritesNothing(t *testing.T) {
	r := newRenderer(t, Options{})
	for name, data := range map[string]any{
		"no such page":      nil,
		"data without Base": struct{ X int }{1},
	} {
		page := "error"
		if name == "no such page" {
			page = "no-such-page"
		}
		rec := httptest.NewRecorder()
		if err := r.Render(rec, http.StatusOK, page, data); err == nil {
			t.Errorf("%s: no error", name)
		}
		if len(rec.Header()) != 0 || rec.Body.Len() != 0 || rec.Code != http.StatusOK || rec.Flushed {
			t.Errorf("%s: something was written: %v %q", name, rec.Header(), rec.Body.String())
		}
	}
}

func TestAssetFunctionRefusesMissingFiles(t *testing.T) {
	files := fstest.MapFS{
		"templates/layout.html": {Data: []byte(`{{define "layout"}}<link href="{{asset "css/nope.css"}}">{{end}}`)},
		"templates/page.html":   {Data: []byte(``)},
		"static/css/site.css":   {Data: []byte(`body{}`)},
	}
	assets, pages, err := load(files)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := assets.url("css/site.css"); err != nil {
		t.Errorf("existing file: %v", err)
	}
	var sb strings.Builder
	if err := pages["page"].ExecuteTemplate(&sb, "layout", nil); err == nil || !strings.Contains(err.Error(), "css/nope.css") {
		t.Errorf("a missing static file rendered: err = %v", err)
	}
}

func TestStaticFiles(t *testing.T) {
	r := newRenderer(t, Options{})
	css, _ := r.AssetURL("css/site.css")
	js, _ := r.AssetURL("js/lib/api.js")
	svg, _ := r.AssetURL("favicon.svg")
	hash := strings.Split(strings.TrimPrefix(css, StaticPrefix), "/")[0]

	tests := []struct {
		name, method, target string
		wantStatus           int
		wantType             string
	}{
		{"stylesheet", "GET", css, 200, "text/css; charset=utf-8"},
		{"module", "GET", js, 200, "text/javascript; charset=utf-8"},
		{"svg", "GET", svg, 200, "image/svg+xml"},
		{"HEAD", "HEAD", js, 200, "text/javascript; charset=utf-8"},
		{"wrong hash", "GET", StaticPrefix + "0123456789abcdef/css/site.css", 404, "text/plain; charset=utf-8"},
		{"upper-case hash", "GET", StaticPrefix + strings.ToUpper(hash) + "/css/site.css", 404, "text/plain; charset=utf-8"},
		{"short hash", "GET", StaticPrefix + hash[:8] + "/css/site.css", 404, "text/plain; charset=utf-8"},
		{"no hash", "GET", StaticPrefix + "css/site.css", 404, "text/plain; charset=utf-8"},
		{"directory", "GET", StaticPrefix + hash + "/css/", 404, "text/plain; charset=utf-8"},
		{"directory without slash", "GET", StaticPrefix + hash + "/css", 404, "text/plain; charset=utf-8"},
		{"hash alone", "GET", StaticPrefix + hash + "/", 404, "text/plain; charset=utf-8"},
		{"missing file", "GET", StaticPrefix + hash + "/css/nope.css", 404, "text/plain; charset=utf-8"},
		{"template", "GET", StaticPrefix + hash + "/../templates/layout.html", 404, "text/plain; charset=utf-8"},
		{"encoded escape", "GET", StaticPrefix + hash + "/%2e%2e/templates/layout.html", 404, "text/plain; charset=utf-8"},
		{"POST", "POST", css, 405, "text/plain; charset=utf-8"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := serve(r.ServeStatic, tt.method, tt.target)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if got := rec.Header().Get("Content-Type"); got != tt.wantType {
				t.Errorf("Content-Type = %q, want %q", got, tt.wantType)
			}
			cache := rec.Header().Get("Cache-Control")
			switch tt.wantStatus {
			case 200:
				if cache != "public, max-age=31536000, immutable" {
					t.Errorf("Cache-Control = %q, want a year, immutable", cache)
				}
				if tt.method == "HEAD" {
					if rec.Body.Len() != 0 || rec.Header().Get("Content-Length") == "" {
						t.Errorf("HEAD: %d bytes of body, Content-Length %q", rec.Body.Len(), rec.Header().Get("Content-Length"))
					}
				} else if rec.Body.Len() == 0 {
					t.Error("empty file")
				}
			case 404:
				if cache != "no-store" {
					t.Errorf("Cache-Control = %q, want no-store: the file may exist under the next hash", cache)
				}
			case 405:
				if got := rec.Header().Get("Allow"); got != "GET, HEAD" {
					t.Errorf("Allow = %q", got)
				}
			}
		})
	}

	// A browser that asks again anyway (a forced reload) gets a 304.
	etag := serve(r.ServeStatic, "GET", css).Header().Get("ETag")
	if rec := serve(r.ServeStatic, "GET", css, "If-None-Match", etag); etag == "" || rec.Code != http.StatusNotModified {
		t.Errorf("If-None-Match %q: status %d, want 304", etag, rec.Code)
	}
}

func TestHashFollowsContent(t *testing.T) {
	base := fstest.MapFS{
		"static/css/site.css": {Data: []byte("body{}")},
		"static/js/a.js":      {Data: []byte("export {}")},
	}
	hashOf := func(files fstest.MapFS) string {
		t.Helper()
		set, err := loadAssets(files)
		if err != nil {
			t.Fatal(err)
		}
		if !validHash(set.hash) {
			t.Fatalf("hash %q is not %d hex digits", set.hash, hashLen)
		}
		return set.hash
	}
	clone := func() fstest.MapFS {
		out := fstest.MapFS{}
		for k, v := range base {
			out[k] = &fstest.MapFile{Data: append([]byte(nil), v.Data...)}
		}
		return out
	}
	original := hashOf(base)
	if again := hashOf(clone()); again != original {
		t.Errorf("same files, different hash: %s, %s", original, again)
	}

	changed := clone()
	changed["static/js/a.js"].Data = []byte("export {};")
	added := clone()
	added["static/js/b.js"] = &fstest.MapFile{Data: []byte("")}
	renamed := fstest.MapFS{"static/css/site.css": base["static/css/site.css"], "static/js/c.js": base["static/js/a.js"]}
	// Moving bytes from one file to the next must not hash the same.
	shifted := fstest.MapFS{"static/css/site.css": {Data: []byte("body{}e")}, "static/js/a.js": {Data: []byte("xport {}")}}
	for name, files := range map[string]fstest.MapFS{"changed": changed, "added": added, "renamed": renamed, "shifted": shifted} {
		if hashOf(files) == original {
			t.Errorf("%s: the hash did not change", name)
		}
	}

	// Editor leftovers and unknown types are ignored, so they change nothing.
	junk := clone()
	junk["static/js/.a.js.swp"] = &fstest.MapFile{Data: []byte("x")}
	junk["static/js/a.js~"] = &fstest.MapFile{Data: []byte("x")}
	if hashOf(junk) != original {
		t.Error("hidden or unknown files changed the hash")
	}
}

// Every embedded static file is served: none has a type the server does
// not know, and none is hidden.
func TestEmbeddedStaticFilesAreServable(t *testing.T) {
	count := 0
	err := fs.WalkDir(embedded, "static", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		count++
		if strings.HasPrefix(d.Name(), ".") || contentTypes[path.Ext(p)] == "" {
			t.Errorf("%s would not be served", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	set, err := loadAssets(embedded)
	if err != nil {
		t.Fatal(err)
	}
	if len(set.files) != count || count == 0 {
		t.Errorf("served %d of %d files", len(set.files), count)
	}
	for _, need := range []string{"css/site.css", "js/lib/api.js", "js/lib/dom.js", "js/lib/copy.js", "favicon.svg", "favicon.ico"} {
		if _, ok := set.files[need]; !ok {
			t.Errorf("%s is missing", need)
		}
	}
}

func TestFavicon(t *testing.T) {
	r := newRenderer(t, Options{})
	rec := serve(r.ServeFavicon, "GET", "/favicon.ico")
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "image/x-icon" || rec.Body.Len() == 0 {
		t.Errorf("favicon: %d %q, %d bytes", rec.Code, rec.Header().Get("Content-Type"), rec.Body.Len())
	}
	if got := rec.Header().Get("Cache-Control"); got != "public, max-age=86400" {
		t.Errorf("Cache-Control = %q", got)
	}
}

// copyTree copies the embedded templates and static files to a directory,
// the way a source checkout holds them.
func copyTree(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	err := fs.WalkDir(embedded, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		target := filepath.Join(dir, filepath.FromSlash(p))
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := fs.ReadFile(embedded, p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(name)), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// --dev reads templates and static files on every request: an edit shows
// on the next request, with a new hash, and nothing is cached.
func TestDevModeReadsFromDisk(t *testing.T) {
	dir := copyTree(t)
	r := newRenderer(t, Options{Dir: dir})
	if !r.Dev() || !r.Base("x").Dev {
		t.Fatal("not in dev mode")
	}
	before, _ := r.AssetURL("css/site.css")

	write(t, dir, "static/css/site.css", "body { color: red }\n")
	after, _ := r.AssetURL("css/site.css")
	if after == before {
		t.Fatalf("the hash did not change after an edit: %s", after)
	}
	for _, url := range []string{after, before} {
		// The old hash still serves the current file in dev mode: a page
		// loaded just before the edit still finds its modules.
		rec := serve(r.ServeStatic, "GET", url)
		if rec.Code != 200 || rec.Body.String() != "body { color: red }\n" {
			t.Errorf("%s: %d %q", url, rec.Code, rec.Body.String())
		}
		if got := rec.Header().Get("Cache-Control"); got != "no-store" {
			t.Errorf("%s: Cache-Control = %q, want no-store", url, got)
		}
	}
	if rec := serve(r.ServeStatic, "GET", StaticPrefix+"not-a-hash/css/site.css"); rec.Code != 404 {
		t.Errorf("a malformed hash: %d, want 404", rec.Code)
	}

	write(t, dir, "templates/error.html", `{{define "main"}}<p>edited {{.Heading}}</p>{{end}}`)
	body := render(t, r, "error", ErrorPage{Base: r.Base("t"), Heading: "now"}).Body.String()
	if !strings.Contains(body, "<p>edited now</p>") || !strings.Contains(body, after) {
		t.Errorf("the edit did not show:\n%s", body)
	}
	if !strings.Contains(body, "development mode") {
		t.Error("the footer does not say dev mode")
	}

	// A broken template is an error on the page that has it, not a crash.
	write(t, dir, "templates/error.html", `{{define "main"}}{{end`)
	if err := r.Render(httptest.NewRecorder(), 200, "error", ErrorPage{Base: r.Base("t")}); err == nil {
		t.Error("a broken template rendered")
	}
}

func TestNewRefusesAnIncompleteDirectory(t *testing.T) {
	dir := t.TempDir()
	if _, err := New(Options{Dir: dir}); err == nil {
		t.Error("an empty directory was accepted")
	}
	if err := os.MkdirAll(filepath.Join(dir, "static"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := New(Options{Dir: dir}); err == nil || !strings.Contains(err.Error(), "layout.html") {
		t.Errorf("a directory without the layout: err = %v", err)
	}
}

// The tests run inside the checkout, so SourceDir must find this very
// directory.
func TestSourceDirFindsTheCheckout(t *testing.T) {
	got, err := SourceDir()
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("SourceDir = %q, want %q", got, want)
	}
}

func TestSecurityHeaders(t *testing.T) {
	h := http.Header{}
	SetSecurityHeaders(h)
	SetSecurityHeaders(h)
	for name, want := range map[string]string{
		"Content-Security-Policy": ContentSecurityPolicy,
		"X-Content-Type-Options":  "nosniff",
		"Referrer-Policy":         "same-origin",
		"X-Frame-Options":         "DENY",
	} {
		if got := h.Values(name); len(got) != 1 || got[0] != want {
			t.Errorf("%s = %q, want exactly %q", name, got, want)
		}
	}
	for _, banned := range []string{"unsafe-inline", "unsafe-eval", "*", "http:", "https:"} {
		if strings.Contains(ContentSecurityPolicy, banned) {
			t.Errorf("the policy contains %q", banned)
		}
	}
}
