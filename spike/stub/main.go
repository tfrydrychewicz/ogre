// Command stub is the throwaway Directory stand-in for the L0 Chrome spike
// (docs/IMPLEMENTATION_PLAN.md, milestone L0 and Appendix B).
//
// It logs every request it is asked to resolve, exactly as it arrived, and
// answers 302, 200 or 404. With -role decoy it plays "some other machine that
// happens to be called go": every request gets the text "decoy".
//
// Build and run from spike/:
//
//	go build -o bin/stub stub/main.go
//	bin/stub -addr localhost:4655
package main

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html"
	"io"
	"io/fs"
	"log"
	"mime"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"
)

//go:embed manual.html
var manualPage []byte

const meta = `{"product":"godir-spike","version":"0.0.0","api":0}` + "\n"

// manualHit matches the paths the manual checklist asks a human to type.
var manualHit = regexp.MustCompile(`^/m[0-9]+`)

// entry is one logged request. Values are copied from the request without any
// normalization; requestURI is the request line's target as sent. (JSON can
// only carry valid UTF-8, which is all a browser ever sends here.)
type entry struct {
	Seq          int    `json:"seq"`
	Time         string `json:"time"`
	Method       string `json:"method"`
	Host         string `json:"host"`
	RequestURI   string `json:"requestURI"`
	EscapedPath  string `json:"escapedPath"`
	RawQuery     string `json:"rawQuery"`
	SecFetchSite string `json:"secFetchSite"`
	SecFetchMode string `json:"secFetchMode"`
	SecFetchDest string `json:"secFetchDest"`
	SecFetchUser string `json:"secFetchUser"`
	SecPurpose   string `json:"secPurpose"` // "prefetch" or "prefetch;prerender": not a navigation the user made
	Referer      string `json:"referer"`
	Origin       string `json:"origin"`
	UserAgent    string `json:"userAgent"`
}

type server struct {
	decoy      bool
	resultsDir string
	// manualConfig is what manual.sh knows and the checklist page cannot work
	// out by itself: the extension's status page address and how to restart.
	manualConfig map[string]string

	mu      sync.Mutex
	seq     int // never reset, so entries stay distinguishable across a clear
	entries []entry
	stdout  *json.Encoder
}

func newServer(decoy bool, resultsDir string, manualConfig map[string]string) *server {
	s := &server{decoy: decoy, resultsDir: resultsDir, manualConfig: manualConfig, entries: []entry{}, stdout: json.NewEncoder(os.Stdout)}
	s.stdout.SetEscapeHTML(false)
	return s
}

// ServeHTTP is the only handler. It dispatches on the escaped path itself
// rather than through http.ServeMux, which would clean "//" and ".." with a
// redirect and so hide what the browser really sent.
func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p := r.URL.EscapedPath()
	w.Header().Set("Cache-Control", "no-store")

	switch p {
	case "/-/spike/log":
		if allow(w, r, http.MethodGet) {
			s.mu.Lock()
			entries := s.entries // safe to read unlocked: the log is only ever appended to or replaced
			s.mu.Unlock()
			writeJSON(w, http.StatusOK, map[string][]entry{"entries": entries})
		}
		return
	case "/-/spike/log/clear":
		if allow(w, r, http.MethodPost) {
			s.mu.Lock()
			s.entries = []entry{}
			s.mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		}
		return
	}

	if s.decoy {
		s.record(r)
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		io.WriteString(w, "decoy")
		return
	}
	if strings.HasPrefix(p, "/-/") {
		s.serveInternal(w, r, p)
		return
	}

	s.record(r)
	switch {
	case p == "/":
		writePage(w, http.StatusOK, "home", "godir spike stub: home",
			`<h1>godir spike stub: home</h1>
<p>This is the stand-in for the Directory's home page.</p>
<p><a href="/-/spike/manual">Manual checklist</a></p>`)
	case strings.HasPrefix(p, "/hit/"):
		w.Header().Set("Location", "http://"+r.Host+"/-/spike/dest")
		w.WriteHeader(http.StatusFound)
	case manualHit.MatchString(p):
		writePage(w, http.StatusOK, "manual-hit", "reached the stub",
			`<h1>Check reached the stub: `+html.EscapeString(p)+`</h1>
<p>Address as this page sees it: <code id="href"></code></p>
<p>You can close this tab and go back to the checklist.</p>
<script>document.getElementById("href").textContent = location.href;</script>`)
	default:
		writePage(w, http.StatusNotFound, "miss", "miss", `<h1>miss: `+html.EscapeString(p)+`</h1>`)
	}
}

// serveInternal answers the "/-/" paths of the directory role. They are never logged.
func (s *server) serveInternal(w http.ResponseWriter, r *http.Request, p string) {
	switch p {
	case "/-/api/v1/meta":
		if allow(w, r, http.MethodGet) {
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, meta)
		}
	case "/-/spike/dest":
		if allow(w, r, http.MethodGet) {
			writePage(w, http.StatusOK, "dest", "spike destination",
				`<h1>spike destination</h1>
<p id="href"></p>
<script>document.getElementById("href").textContent = location.href;</script>`)
		}
	case "/-/spike/manual":
		if allow(w, r, http.MethodGet) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write(manualPage)
		}
	case "/-/spike/manual/config":
		if allow(w, r, http.MethodGet) {
			writeJSON(w, http.StatusOK, s.manualConfig)
		}
	case "/-/spike/manual/results":
		s.serveManualResults(w, r)
	default:
		http.Error(w, "no such spike endpoint", http.StatusNotFound)
	}
}

func (s *server) serveManualResults(w http.ResponseWriter, r *http.Request) {
	file := filepath.Join(s.resultsDir, "manual.json")
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		data, err := os.ReadFile(file)
		if errors.Is(err, fs.ErrNotExist) {
			http.Error(w, "no manual results saved yet", http.StatusNotFound)
			return
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(data)
	case http.MethodPost:
		// Insisting on a JSON content type keeps other web pages from writing
		// the file: a cross-origin page cannot send it without a CORS preflight,
		// which this server never approves.
		if ct, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); ct != "application/json" {
			http.Error(w, "Content-Type must be application/json", http.StatusUnsupportedMediaType)
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		if err != nil {
			http.Error(w, "body unreadable or larger than 1 MB", http.StatusBadRequest)
			return
		}
		var pretty bytes.Buffer
		if err := json.Indent(&pretty, body, "", "  "); err != nil {
			http.Error(w, "body is not valid JSON: "+err.Error(), http.StatusBadRequest)
			return
		}
		pretty.WriteByte('\n')
		if err := os.MkdirAll(s.resultsDir, 0o755); err == nil {
			err = os.WriteFile(file, pretty.Bytes(), 0o644)
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"written": file})
	default:
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// record appends the request to the log and prints it to stdout as one JSON line.
func (s *server) record(r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	e := entry{
		Seq:          s.seq,
		Time:         time.Now().Format("2006-01-02T15:04:05.000Z07:00"), // RFC 3339, with milliseconds
		Method:       r.Method,
		Host:         r.Host,
		RequestURI:   r.RequestURI,
		EscapedPath:  r.URL.EscapedPath(),
		RawQuery:     r.URL.RawQuery,
		SecFetchSite: r.Header.Get("Sec-Fetch-Site"),
		SecFetchMode: r.Header.Get("Sec-Fetch-Mode"),
		SecFetchDest: r.Header.Get("Sec-Fetch-Dest"),
		SecFetchUser: r.Header.Get("Sec-Fetch-User"),
		SecPurpose:   r.Header.Get("Sec-Purpose"),
		Referer:      r.Header.Get("Referer"),
		Origin:       r.Header.Get("Origin"),
		UserAgent:    r.Header.Get("User-Agent"),
	}
	s.entries = append(s.entries, e)
	s.stdout.Encode(e)
}

// allow reports whether the request uses the wanted method (HEAD counts as
// GET), answering 405 itself when it does not.
func allow(w http.ResponseWriter, r *http.Request, method string) bool {
	if r.Method == method || (method == http.MethodGet && r.Method == http.MethodHead) {
		return true
	}
	w.Header().Set("Allow", method)
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	return false
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.Encode(v)
}

// writePage sends a minimal HTML page. body must already be safe HTML. The
// empty icon stops the browser from asking for /favicon.ico, which would
// otherwise show up in the log as a miss after every page.
func writePage(w http.ResponseWriter, status int, spike, title, body string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	fmt.Fprintf(w, `<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><title>%s</title><link rel="icon" href="data:,"></head>
<body data-spike="%s">
%s
</body>
</html>
`, html.EscapeString(title), spike, body)
}

// listen opens the listening sockets for addr. The host "localhost" means
// both loopback addresses, because a browser may pick either one: IPv4 is
// required, IPv6 is skipped only on a machine that has no IPv6 loopback.
func listen(addr string) ([]net.Listener, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	if host != "localhost" {
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			return nil, err
		}
		return []net.Listener{ln}, nil
	}
	ln4, err := net.Listen("tcp4", net.JoinHostPort("127.0.0.1", port))
	if err != nil {
		return nil, err
	}
	_, port, _ = net.SplitHostPort(ln4.Addr().String()) // the real port, if 0 was asked for
	ln6, err := net.Listen("tcp6", net.JoinHostPort("::1", port))
	switch {
	case err == nil:
		return []net.Listener{ln4, ln6}, nil
	case errors.Is(err, syscall.EADDRINUSE):
		// Someone else answers on [::1]: requests to "localhost" could reach them instead.
		ln4.Close()
		return nil, err
	default:
		log.Printf("no IPv6 loopback, serving on IPv4 only: %v", err)
		return []net.Listener{ln4}, nil
	}
}

func main() {
	log.SetFlags(0)
	log.SetPrefix("stub: ")
	addr := flag.String("addr", "localhost:4655", "address to listen on; the host localhost means 127.0.0.1 and [::1]")
	role := flag.String("role", "directory", `"directory" or "decoy"`)
	resultsDir := flag.String("results-dir", "results", "directory that receives manual.json")
	statusURL := flag.String("status-url", "", "address of the extension's status page, shown on the manual checklist")
	restartCommand := flag.String("restart-command", "", "command that starts this stub again, shown on the manual checklist")
	flag.Parse()
	if flag.NArg() > 0 || (*role != "directory" && *role != "decoy") {
		flag.Usage()
		os.Exit(2)
	}
	dir, err := filepath.Abs(*resultsDir)
	if err != nil {
		log.Fatal(err)
	}

	lns, err := listen(*addr)
	if errors.Is(err, syscall.EADDRINUSE) {
		_, port, _ := net.SplitHostPort(*addr)
		log.Fatalf("%s is already in use. Is another stub still running? Find it with: lsof -nP -iTCP:%s -sTCP:LISTEN", *addr, port)
	}
	if err != nil {
		log.Fatal(err)
	}

	config := map[string]string{"statusUrl": *statusURL, "restartCommand": *restartCommand}
	srv := &http.Server{Handler: newServer(*role == "decoy", dir, config), ReadHeaderTimeout: 10 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	failed := make(chan error, len(lns))
	for _, ln := range lns {
		log.Printf("%s role listening on http://%s", *role, ln.Addr())
		go func(ln net.Listener) { failed <- srv.Serve(ln) }(ln)
	}

	select {
	case err := <-failed:
		log.Fatal(err)
	case <-ctx.Done():
	}
	stop() // a second Ctrl+C now ends the process at once
	shutdown, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdown); err != nil {
		srv.Close()
	}
	log.Print("stopped")
}
