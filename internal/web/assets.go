package web

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io/fs"
	"net/http"
	"path"
	"slices"
	"strings"
	"time"
)

// StaticPrefix is where static files are served. A file's URL is
// StaticPrefix + <hash> + "/" + <path below static/>.
const StaticPrefix = "/-/static/"

// hashLen is the length of the hash segment, in hex digits. 64 bits keep an
// accidental collision between two versions of the files out of reach.
const hashLen = 16

// immutable is the caching of a static file under a content-hash URL: the
// URL changes whenever any file does, so a browser may keep a file for a
// year without asking again.
const immutable = "public, max-age=31536000, immutable"

// contentTypes maps the extensions a static file may have to the type it is
// served with. The table is explicit, not mime.TypeByExtension, because that
// one also reads the operating system's tables and could serve a module
// script with a type a browser refuses under nosniff. A file with any other
// extension is not served at all; a test checks that none is embedded.
var contentTypes = map[string]string{
	".css":   "text/css; charset=utf-8",
	".js":    "text/javascript; charset=utf-8",
	".svg":   "image/svg+xml",
	".ico":   "image/x-icon",
	".png":   "image/png",
	".json":  "application/json",
	".txt":   "text/plain; charset=utf-8",
	".woff2": "font/woff2",
}

// assetSet is one version of the static files, held in memory, and the hash
// that names that version.
//
// There is one hash for the whole set rather than one per file because the
// browser modules import each other by relative URL ("./lib/api.js"). A
// module loaded from /-/static/<hash>/js/home.js asks for
// /-/static/<hash>/js/lib/api.js, which only works if every file shares the
// prefix. Any change to any file therefore changes every URL; with a few
// dozen small files that costs nothing worth saving.
type assetSet struct {
	hash  string
	files map[string][]byte // path below static/ -> content
}

// loadAssets reads every servable file below static/ in fsys and hashes them
// all. Hidden files and files of an unknown type are skipped, so an editor's
// swap file in --dev mode is neither served nor able to break the page.
func loadAssets(fsys fs.FS) (*assetSet, error) {
	set := &assetSet{files: map[string][]byte{}}
	err := fs.WalkDir(fsys, "static", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if strings.HasPrefix(d.Name(), ".") && p != "static" {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() || contentTypes[path.Ext(p)] == "" {
			return nil
		}
		content, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		set.files[strings.TrimPrefix(p, "static/")] = content
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("reading the static files: %w", err)
	}
	set.hash = hashFiles(set.files)
	return set, nil
}

// hashFiles hashes names and contents together, in name order, each part
// prefixed with its length so that no two different sets can produce the
// same byte stream.
func hashFiles(files map[string][]byte) string {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	slices.Sort(names)
	h := sha256.New()
	var n [8]byte
	for _, name := range names {
		binary.BigEndian.PutUint64(n[:], uint64(len(name)))
		h.Write(n[:])
		h.Write([]byte(name))
		binary.BigEndian.PutUint64(n[:], uint64(len(files[name])))
		h.Write(n[:])
		h.Write(files[name])
	}
	return hex.EncodeToString(h.Sum(nil))[:hashLen]
}

// url returns the content-hash URL of a static file, or an error naming the
// file when there is none: a template that names a missing file fails to
// render, and the tests that render every page catch the typo.
func (s *assetSet) url(name string) (string, error) {
	if _, ok := s.files[name]; !ok {
		return "", fmt.Errorf("there is no static file %q", name)
	}
	return StaticPrefix + s.hash + "/" + name, nil
}

// AssetURL returns the content-hash URL of a file below static/, as the
// template function "asset" does: for a page that builds a URL in Go, and
// for tests.
func (r *Renderer) AssetURL(name string) (string, error) {
	set, err := r.currentAssets()
	if err != nil {
		return "", err
	}
	return set.url(name)
}

// ServeStatic serves a file under StaticPrefix. The request path must carry
// the current hash: any other hash is 404, so a page cached from an older
// version never gets a mix of old and new files under one name.
//
// In --dev mode any well-formed hash is accepted and nothing is cached, so a
// module that a page loaded just before a file was edited still finds its
// imports.
func (r *Renderer) ServeStatic(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet && req.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		plainText(w, req, http.StatusMethodNotAllowed, "405 Method Not Allowed\n")
		return
	}
	set, err := r.currentAssets()
	if err != nil {
		plainText(w, req, http.StatusInternalServerError, "500 Internal Server Error\n")
		return
	}
	rest, ok := strings.CutPrefix(req.URL.Path, StaticPrefix)
	hash, name, found := strings.Cut(rest, "/")
	if !ok || !found || !validHash(hash) || (!r.dev && hash != set.hash) {
		notFound(w, req)
		return
	}
	// The map holds files only, by clean relative path, so a directory, a
	// template, "..", or anything outside static/ is simply not in it.
	content, ok := set.files[name]
	if !ok {
		notFound(w, req)
		return
	}
	cacheControl := immutable
	if r.dev {
		cacheControl = "no-store"
	}
	serveBytes(w, req, name, content, cacheControl, `"`+set.hash+`"`)
}

// ServeFavicon answers "/favicon.ico", which browsers request on their own
// whatever the page says. Its URL never changes, so it is cached for a day,
// not for ever.
func (r *Renderer) ServeFavicon(w http.ResponseWriter, req *http.Request) {
	set, err := r.currentAssets()
	if err != nil {
		plainText(w, req, http.StatusInternalServerError, "500 Internal Server Error\n")
		return
	}
	content, ok := set.files["favicon.ico"]
	if !ok {
		notFound(w, req)
		return
	}
	serveBytes(w, req, "favicon.ico", content, "public, max-age=86400", `"`+set.hash+`"`)
}

// serveBytes serves one file through http.ServeContent, which handles HEAD,
// conditional requests and ranges. The type is set first so ServeContent
// never sniffs one.
func serveBytes(w http.ResponseWriter, req *http.Request, name string, content []byte, cacheControl, etag string) {
	h := w.Header()
	h.Set("Content-Type", contentTypes[path.Ext(name)])
	h.Set("Cache-Control", cacheControl)
	h.Set("ETag", etag)
	http.ServeContent(w, req, name, time.Time{}, bytes.NewReader(content))
}

func validHash(s string) bool {
	if len(s) != hashLen {
		return false
	}
	for i := 0; i < len(s); i++ {
		if c := s[i]; !('0' <= c && c <= '9' || 'a' <= c && c <= 'f') {
			return false
		}
	}
	return true
}

// notFound is the 404 of a static file. It is not cached: the file may
// exist under the next hash.
func notFound(w http.ResponseWriter, req *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	plainText(w, req, http.StatusNotFound, "404 Not Found\n")
}

// plainText writes a short plain-text answer. HEAD gets the same headers and
// no body.
func plainText(w http.ResponseWriter, req *http.Request, status int, body string) {
	h := w.Header()
	h.Set("Content-Type", "text/plain; charset=utf-8")
	h.Set("Content-Length", fmt.Sprint(len(body)))
	w.WriteHeader(status)
	if req.Method != http.MethodHead {
		_, _ = w.Write([]byte(body))
	}
}
