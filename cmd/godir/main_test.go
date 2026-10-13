package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// noEnv is the environment of every test: empty. With no HOME there is no
// default data directory, so a test that forgot --db fails instead of
// touching a real Directory.
func noEnv(string) string { return "" }

// lockedBuffer is a bytes.Buffer that a running server and the test can use
// at the same time.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// freeAddr returns "localhost:<port>" for a port that was free a moment ago
// on both loopback addresses.
func freeAddr(t *testing.T) string {
	t.Helper()
	for range 20 {
		v4, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("find a free port: %v", err)
		}
		_, port, _ := net.SplitHostPort(v4.Addr().String())
		v6, err := net.Listen("tcp6", net.JoinHostPort("::1", port))
		v4.Close()
		if err != nil {
			continue // taken on IPv6 (or no IPv6 at all); try another
		}
		v6.Close()
		return net.JoinHostPort("localhost", port)
	}
	// No IPv6 loopback on this machine: any free IPv4 port will do.
	v4, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("find a free port: %v", err)
	}
	defer v4.Close()
	_, port, _ := net.SplitHostPort(v4.Addr().String())
	return net.JoinHostPort("localhost", port)
}

// failFast returns the context for a command that is expected to fail
// without serving. If it serves after all (the refusal under test stopped
// working), the deadline stops it within seconds: the command then exits 0
// and the test fails on that, instead of hanging with a listener open until
// the test binary times out.
func failFast(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestCommandsThatDoNotServe(t *testing.T) {
	neverDB := filepath.Join(t.TempDir(), "never", "godir.db")
	// A port nobody holds, so a refusal that broke would show as "it
	// started", not as a clash with whatever uses the default port here.
	_, port, _ := net.SplitHostPort(freeAddr(t))

	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout string // substring; "" means stdout must be empty
		wantStderr string // substring; "" means stderr must be empty
	}{
		{"version", []string{"version"}, 0, "godir " + version + "\n", ""},
		{"version with an argument", []string{"version", "extra"}, 1, "", `version takes no arguments, got "extra"`},
		{"help", []string{"help"}, 0, "--allow-unauthenticated-network", ""},
		{"help flag", []string{"--help"}, 0, "godir version", ""},
		{"help flag of serve", []string{"serve", "-h"}, 0, "--db file", ""},
		{"unknown command", []string{"bogus"}, 1, "", `unknown command "bogus"`},
		{"unknown flag", []string{"--bogus"}, 1, "", "flag provided but not defined: -bogus"},
		{"stray argument", []string{"serve", "extra"}, 1, "", `unexpected argument "extra"`},
		{"every interface", []string{"--addr", "0.0.0.0:" + port, "--db", neverDB}, 1, "", "refusing to listen beyond this computer"},
		{"no host", []string{"serve", "--addr", ":" + port, "--db", neverDB}, 1, "", "refusing to listen beyond this computer"},
		{"a LAN address", []string{"--addr", "192.168.1.10:" + port, "--db", neverDB}, 1, "", "--allow-unauthenticated-network"},
		{"no database path and no home directory", []string{"--addr", "localhost:" + port}, 1, "", "--db"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// None of these may serve; see failFast.
			var stdout, stderr bytes.Buffer
			code := run(failFast(t), tt.args, noEnv, &stdout, &stderr)

			if code != tt.wantCode {
				t.Errorf("exit code = %d, want %d (stderr: %s)", code, tt.wantCode, stderr.String())
			}
			checkOutput(t, "stdout", stdout.String(), tt.wantStdout)
			checkOutput(t, "stderr", stderr.String(), tt.wantStderr)
			if tt.wantCode != 0 {
				// A failure is one line: "godir: <what went wrong>".
				if got := stderr.String(); !strings.HasPrefix(got, "godir: ") || strings.Count(got, "\n") != 1 || !strings.HasSuffix(got, "\n") {
					t.Errorf("stderr is not one \"godir: ...\" line: %q", got)
				}
			}
		})
	}

	// A refused address is refused before anything is created.
	if _, err := os.Stat(filepath.Dir(neverDB)); !os.IsNotExist(err) {
		t.Errorf("a refused start created %s (stat error: %v)", filepath.Dir(neverDB), err)
	}
}

func checkOutput(t *testing.T, stream, got, want string) {
	t.Helper()
	switch {
	case want == "" && got != "":
		t.Errorf("%s = %q, want nothing", stream, got)
	case !strings.Contains(got, want):
		t.Errorf("%s = %q, want it to contain %q", stream, got, want)
	}
}

// running is a "godir serve" started by a test.
type running struct {
	url    string
	stderr *lockedBuffer
	stop   func() int // ends the server as a signal would and returns its exit code
}

// startServing runs serve on addr with the database at db and waits until it
// answers. The server is stopped when the test ends, if the test did not.
func startServing(t *testing.T, addr, db string) *running {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	r := &running{url: "http://" + addr, stderr: &lockedBuffer{}}
	exit := make(chan int, 1)
	go func() {
		exit <- run(ctx, []string{"serve", "--addr", addr, "--db", db}, noEnv, io.Discard, r.stderr)
	}()

	var once sync.Once
	code := -1
	r.stop = func() int {
		once.Do(func() {
			cancel()
			select {
			case code = <-exit:
			case <-time.After(10 * time.Second):
				t.Errorf("godir did not stop within 10s; its output:\n%s", r.stderr)
			}
		})
		return code
	}
	t.Cleanup(func() { r.stop() })

	deadline := time.Now().Add(10 * time.Second)
	for {
		select {
		case code = <-exit:
			once.Do(func() {})
			t.Fatalf("godir exited with code %d before it was ready; its output:\n%s", code, r.stderr)
		default:
		}
		resp, err := http.Get(r.url + "/-/healthz")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return r
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("godir did not answer %s/-/healthz within 10s; its output:\n%s", r.url, r.stderr)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// noRedirects is a client that reports a redirect instead of following it.
var noRedirects = &http.Client{
	Timeout:       5 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

// expectRedirect asserts that the go link at path answers 302 to destination,
// uncached.
func expectRedirect(t *testing.T, base, path, destination string) {
	t.Helper()
	resp, err := noRedirects.Get(base + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != destination {
		t.Errorf("GET %s = %d to %q, want 302 to %q", path, resp.StatusCode, resp.Header.Get("Location"), destination)
	}
	if got := resp.Header.Get("Cache-Control"); got != "no-store" {
		t.Errorf("GET %s Cache-Control = %q, want no-store", path, got)
	}
}

// The milestone's "you can now", minus the browser: create a link, follow it,
// stop, start again on the same database, and it is still there.
func TestServeCreateResolveRestart(t *testing.T) {
	addr := freeAddr(t)
	db := filepath.Join(t.TempDir(), "data", "godir.db")
	// Mixed case, an escape, a query and a fragment: a destination must come
	// back from the database and go out in the redirect byte for byte.
	const destination = "https://Docs.Example.com/Road-Map/1aB_cD?Tab=A%2Fb&X=Y#Frag"

	first := startServing(t, addr, db)
	for _, want := range []string{"godir is starting", "url=http://" + addr, db, "version=" + version} {
		if !strings.Contains(first.stderr.String(), want) {
			t.Errorf("the startup log does not mention %q:\n%s", want, first.stderr)
		}
	}

	body := `{"description":"Product roadmap","routes":[{"pattern":"Roadmap","destination":"` + destination + `"}]}`
	resp, err := http.Post(first.url+"/-/api/v1/links", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST /-/api/v1/links: %v", err)
	}
	created, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /-/api/v1/links = %d, want 201: %s", resp.StatusCode, created)
	}
	for _, want := range []string{`"name":"roadmap"`, `"displayName":"Local user"`, `"canEdit":true`, `"version":1`} {
		if !strings.Contains(string(created), want) {
			t.Errorf("the created link lacks %s: %s", want, created)
		}
	}

	expectRedirect(t, first.url, "/roadmap", destination)
	expectRedirect(t, first.url, "/ROADMAP", destination)

	// Names ignore case only (PRD Q5): "-" and "_" are part of a name.
	for _, path := range []string{"/road-map", "/road_map"} {
		resp, err := noRedirects.Get(first.url + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", path, resp.StatusCode)
		}
	}

	// A known name with segments no route has: a miss that still names the link.
	miss, err := noRedirects.Get(first.url + "/roadmap/Private-Segment?token=1")
	if err != nil {
		t.Fatalf("GET /roadmap/Private-Segment: %v", err)
	}
	miss.Body.Close()
	if miss.StatusCode != http.StatusNotFound || miss.Header.Get("Cache-Control") != "no-store" {
		t.Errorf("GET /roadmap/Private-Segment = %d, Cache-Control %q; want 404, no-store", miss.StatusCode, miss.Header.Get("Cache-Control"))
	}

	page, err := http.Get(first.url + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	html, _ := io.ReadAll(page.Body)
	page.Body.Close()
	if page.Request.URL.Path != "/-/" || !strings.Contains(string(html), version) {
		t.Errorf("GET / ended at %s without the version on the page: %s", page.Request.URL.Path, html)
	}

	if code := first.stop(); code != 0 {
		t.Errorf("exit code after a requested stop = %d, want 0; output:\n%s", code, first.stderr)
	}
	if !strings.Contains(first.stderr.String(), "godir has stopped") {
		t.Errorf("no stop line in the log:\n%s", first.stderr)
	}
	// NFR-17: the log names the link (for a miss, the requested name), never
	// the path as it was asked for, its further segments or its query.
	log := first.stderr.String()
	for _, raw := range []string{"ROADMAP", "Private-Segment", "token"} {
		if strings.Contains(log, raw) {
			t.Errorf("the log holds %q from a raw request path:\n%s", raw, log)
		}
	}
	if !strings.Contains(log, "decision=miss_known link=roadmap") {
		t.Errorf("the log does not name the link of the known-name miss:\n%s", log)
	}

	second := startServing(t, addr, db)
	expectRedirect(t, second.url, "/roadmap", destination)
}

func TestServeSaysWhenAnotherGodirHoldsThePort(t *testing.T) {
	addr := freeAddr(t)
	first := startServing(t, addr, filepath.Join(t.TempDir(), "godir.db"))

	otherDB := filepath.Join(t.TempDir(), "godir.db")
	var stdout, stderr bytes.Buffer
	code := run(failFast(t), []string{"--addr", addr, "--db", otherDB}, noEnv, &stdout, &stderr)

	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	lines := strings.Split(strings.TrimSuffix(stderr.String(), "\n"), "\n")
	last := lines[len(lines)-1]
	if !strings.HasPrefix(last, "godir: ") || !strings.Contains(last, "another godir") || !strings.Contains(last, version) {
		t.Errorf("the last line does not say another godir (version %s) holds the port: %q", version, last)
	}

	// One line and nothing else: it never got as far as starting.
	if len(lines) != 1 {
		t.Errorf("stderr has %d lines, want only the error:\n%s", len(lines), stderr.String())
	}
	// It did not touch a database it was never going to serve: the address
	// is claimed before the database is opened.
	if entries, err := os.ReadDir(filepath.Dir(otherDB)); err != nil || len(entries) != 0 {
		t.Errorf("the second godir left %d files beside its database path (%v); want none", len(entries), err)
	}

	// The first one is unharmed.
	resp, err := http.Get(first.url + "/-/healthz")
	if err != nil {
		t.Fatalf("the first godir stopped answering: %v", err)
	}
	resp.Body.Close()
}

// A second godir pointed at the database of the running one must leave it
// alone: no write, no migration, not even opening it.
func TestSecondGodirDoesNotTouchTheSharedDatabase(t *testing.T) {
	addr := freeAddr(t)
	db := filepath.Join(t.TempDir(), "godir.db")
	first := startServing(t, addr, db)

	before := snapshotDir(t, filepath.Dir(db))
	var stdout, stderr bytes.Buffer
	if code := run(failFast(t), []string{"--addr", addr, "--db", db}, noEnv, &stdout, &stderr); code != 1 {
		t.Fatalf("exit code = %d, want 1; stderr: %s", code, stderr.String())
	}
	if after := snapshotDir(t, filepath.Dir(db)); after != before {
		t.Errorf("the database files changed:\nbefore: %s\nafter:  %s", before, after)
	}

	// And the running one still saves and resolves.
	resp, err := http.Post(first.url+"/-/api/v1/links", "application/json",
		strings.NewReader(`{"routes":[{"pattern":"roadmap","destination":"https://example.com/"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Errorf("POST after the second godir failed to start = %d, want 201", resp.StatusCode)
	}
	expectRedirect(t, first.url, "/roadmap", "https://example.com/")
}

// snapshotDir describes every file in dir by name, size and modification
// time.
func snapshotDir(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&b, "%s %d %s; ", e.Name(), info.Size(), info.ModTime().Format(time.RFC3339Nano))
	}
	return b.String()
}

// A failure after the address was claimed gives the address back.
func TestServeFreesThePortWhenTheDatabaseCannotBeOpened(t *testing.T) {
	addr := freeAddr(t)
	// A directory where the database file should be.
	db := filepath.Join(t.TempDir(), "godir.db")
	if err := os.Mkdir(db, 0o700); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := run(failFast(t), []string{"--addr", addr, "--db", db}, noEnv, &stdout, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "godir: ") || !strings.Contains(stderr.String(), "not a regular file") {
		t.Errorf("exit code %d, stderr %q; want 1 and the reason", code, stderr.String())
	}
	_, port, _ := net.SplitHostPort(addr)
	ln, err := net.Listen("tcp4", "127.0.0.1:"+port)
	if err != nil {
		t.Fatalf("the port is still held after the failed start: %v", err)
	}
	ln.Close()
}

// --db is where the data goes: after serving, that file exists and nothing
// else was created beside it.
func TestServeUsesTheGivenDatabaseFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	db := filepath.Join(dir, "godir.db")
	srv := startServing(t, freeAddr(t), db)
	resp, err := http.Post(srv.url+"/-/api/v1/links", "application/json",
		strings.NewReader(`{"routes":[{"pattern":"roadmap","destination":"https://example.com/"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if code := srv.stop(); code != 0 {
		t.Fatalf("exit code = %d", code)
	}

	if info, err := os.Stat(db); err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		t.Fatalf("no database at the --db path %s: %v", db, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		switch e.Name() {
		case "godir.db", "godir.db-wal", "godir.db-shm":
		default:
			t.Errorf("unexpected file beside the database: %s", e.Name())
		}
	}
}

// serveWith runs serve with extra flags on a free port and returns the port
// and the log. It does not wait for readiness; statusFor does, by retrying.
func serveWith(t *testing.T, flags ...string) (port string, stderr *lockedBuffer) {
	t.Helper()
	addr := freeAddr(t)
	_, port, _ = net.SplitHostPort(addr)
	ctx, cancel := context.WithCancel(context.Background())
	stderr = &lockedBuffer{}
	exit := make(chan int, 1)
	args := append([]string{"--addr", addr, "--db", filepath.Join(t.TempDir(), "godir.db")}, flags...)
	go func() { exit <- run(ctx, args, noEnv, io.Discard, stderr) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-exit:
		case <-time.After(10 * time.Second):
			t.Error("godir did not stop")
		}
	})
	return port, stderr
}

// statusFor asks the godir on port for path under the given Host header.
func statusFor(t *testing.T, port, host, path string, stderr *lockedBuffer) int {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, "http://127.0.0.1:"+port+path, nil)
	req.Host = host
	deadline := time.Now().Add(10 * time.Second)
	for {
		resp, err := noRedirects.Do(req)
		if err == nil {
			resp.Body.Close()
			return resp.StatusCode
		}
		if time.Now().After(deadline) {
			t.Fatalf("godir did not answer: %v\n%s", err, stderr)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// --allow-host must reach the Host check of the running server.
func TestAllowHostIsHonoured(t *testing.T) {
	port, stderr := serveWith(t, "--allow-host", "Go")
	if got := statusFor(t, port, "go:"+port, "/nope", stderr); got != http.StatusNotFound {
		t.Errorf("Host go:%s = %d, want the resolver's 404", port, got)
	}
	if got := statusFor(t, port, "evil.example:"+port, "/nope", stderr); got != http.StatusForbidden {
		t.Errorf("Host evil.example:%s = %d, want 403", port, got)
	}
}

// --log-level must reach the logger.
func TestLogLevelIsHonoured(t *testing.T) {
	port, quiet := serveWith(t, "--log-level", "error")
	if got := statusFor(t, port, "localhost:"+port, "/-/healthz", quiet); got != http.StatusOK {
		t.Fatalf("healthz = %d", got)
	}
	if log := quiet.String(); log != "" {
		t.Errorf("--log-level error still logged:\n%s", log)
	}

	port, chatty := serveWith(t, "--log-level", "info")
	if got := statusFor(t, port, "localhost:"+port, "/-/healthz", chatty); got != http.StatusOK {
		t.Fatalf("healthz = %d", got)
	}
	// The request record is written after the response is sent.
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(chatty.String(), "msg=request") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	for _, want := range []string{"godir is starting", "msg=listening", "msg=request"} {
		if !strings.Contains(chatty.String(), want) {
			t.Errorf("--log-level info did not log %q:\n%s", want, chatty.String())
		}
	}
}

// A port typed with leading zeros is the same port: the server must answer
// under the address it really listens on.
func TestServeWithLeadingZerosInThePort(t *testing.T) {
	addr := freeAddr(t)
	host, port, _ := net.SplitHostPort(addr)
	ctx, cancel := context.WithCancel(context.Background())
	stderr := &lockedBuffer{}
	exit := make(chan int, 1)
	go func() {
		exit <- run(ctx, []string{"--addr", host + ":0" + port, "--db", filepath.Join(t.TempDir(), "godir.db")}, noEnv, io.Discard, stderr)
	}()
	t.Cleanup(func() {
		cancel()
		<-exit
	})
	if got := statusFor(t, port, "localhost:"+port, "/-/healthz", stderr); got != http.StatusOK {
		t.Errorf("GET /-/healthz with Host localhost:%s = %d, want 200", port, got)
	}
	if !strings.Contains(stderr.String(), "url=http://localhost:"+port+" ") {
		t.Errorf("the startup line does not print the address that works:\n%s", stderr.String())
	}
}

func TestServeSaysWhenAnotherProgramHoldsThePort(t *testing.T) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	srv := &http.Server{Handler: http.NotFoundHandler()}
	go srv.Serve(ln)
	defer srv.Close()

	var stdout, stderr bytes.Buffer
	code := run(failFast(t),
		[]string{"--addr", ln.Addr().String(), "--db", filepath.Join(t.TempDir(), "godir.db")}, noEnv, &stdout, &stderr)

	if code != 1 || !strings.Contains(stderr.String(), "in use by another program") {
		t.Errorf("exit code %d, stderr %q; want 1 and \"in use by another program\"", code, stderr.String())
	}
	if strings.Contains(stderr.String(), "another godir") {
		t.Errorf("a program that is not godir was taken for one: %q", stderr.String())
	}
}
