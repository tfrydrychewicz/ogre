package httpd

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/tfrydrychewicz/ogre/internal/app"
	"github.com/tfrydrychewicz/ogre/internal/linkmodel"
)

// syncBuffer is a bytes.Buffer safe to write from server goroutines.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// captureLog returns a logger at debug level, so nothing is filtered out, and
// the buffer its JSON lines go to.
func captureLog() (*slog.Logger, *syncBuffer) {
	buf := &syncBuffer{}
	return slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})), buf
}

// requestRecord returns the one "request" record in the log.
func requestRecord(t *testing.T, log string) map[string]any {
	t.Helper()
	var found map[string]any
	for _, line := range strings.Split(strings.TrimSpace(log), "\n") {
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("log line is not JSON: %q", line)
		}
		if rec["msg"] == "request" {
			if found != nil {
				t.Fatalf("more than one request record:\n%s", log)
			}
			found = rec
		}
	}
	if found == nil {
		t.Fatalf("no request record in:\n%s", log)
	}
	return found
}

// logEntries returns every "request" record in the log, oldest first.
func logEntries(t *testing.T, logs *syncBuffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
		if line == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("log line is not JSON: %q", line)
		}
		if rec["msg"] == "request" {
			out = append(out, rec)
		}
	}
	return out
}

// NFR-17: variable values and missed paths are not recorded. The log names
// the link and never the raw path, the query, or the destination (which will
// carry variable values from L2 on).
func TestRequestLogNeverContainsPathOrQuery(t *testing.T) {
	const (
		pathSecret  = "S3cr3t-Path-Value"
		querySecret = "QUERY-SECRET-TOKEN"
		destSecret  = "dest-secret.example"
	)
	hit := app.Decision{Kind: app.DecisionHit, LinkName: "roadmap", Destination: "https://" + destSecret + "/" + pathSecret}

	tests := []struct {
		name     string
		req      request
		decision app.Decision
		want     map[string]any // attributes the record must have
		absent   []string       // attributes it must not have
	}{
		{
			name:     "hit",
			req:      request{target: "/RoadMap/" + pathSecret + "?token=" + querySecret},
			decision: hit,
			want:     map[string]any{"method": "GET", "status": 302.0, "area": "resolver", "decision": "hit", "link": "roadmap", "level": "INFO"},
			absent:   []string{"route", "host"},
		},
		{
			name:     "hit, HEAD",
			req:      request{method: http.MethodHead, target: "/roadmap/" + pathSecret + "?token=" + querySecret},
			decision: hit,
			want:     map[string]any{"method": "HEAD", "status": 302.0, "decision": "hit", "link": "roadmap"},
		},
		{
			name:     "miss, known link",
			req:      request{target: "/roadmap/" + pathSecret + "?token=" + querySecret},
			decision: app.Decision{Kind: app.DecisionMissKnown, LinkName: "roadmap"},
			want:     map[string]any{"status": 404.0, "decision": "miss_known", "link": "roadmap"},
		},
		{
			name:     "miss, unknown name",
			req:      request{target: "/raodmap/" + pathSecret + "?token=" + querySecret},
			decision: app.Decision{Kind: app.DecisionMissUnknown, LinkName: "raodmap"},
			want:     map[string]any{"status": 404.0, "decision": "miss_unknown", "link": "raodmap"},
		},
		{
			name:     "bad request",
			req:      request{target: "/roadmap/" + pathSecret + "%FF?token=" + querySecret},
			decision: app.Decision{Kind: app.DecisionBadRequest},
			want:     map[string]any{"status": 400.0, "decision": "bad_request"},
			absent:   []string{"link"},
		},
		{
			name:   "refused method on a go link",
			req:    request{method: http.MethodPost, target: "/roadmap/" + pathSecret + "?token=" + querySecret},
			want:   map[string]any{"method": "POST", "status": 405.0, "area": "resolver"},
			absent: []string{"link", "decision"},
		},
		{
			name:   "reserved path",
			req:    request{target: "/.well-known/" + pathSecret + "?token=" + querySecret},
			want:   map[string]any{"status": 404.0, "area": "reserved"},
			absent: []string{"link", "decision", "route"},
		},
		{
			name:   "home",
			req:    request{target: "/?token=" + querySecret},
			want:   map[string]any{"status": 302.0, "area": "home"},
			absent: []string{"link", "decision", "route"},
		},
		{
			name:   "API read",
			req:    request{target: "/-/api/v1/links?q=" + querySecret},
			want:   map[string]any{"status": 200.0, "area": "internal", "route": "/-/api/v1/links"},
			absent: []string{"link", "decision"},
		},
		{
			name: "API write",
			req: request{method: http.MethodPost, target: "/-/api/v1/links?x=" + querySecret,
				headers: map[string]string{"Content-Type": "application/json"}, body: validBody},
			want: map[string]any{"method": "POST", "status": 201.0, "area": "internal", "route": "/-/api/v1/links"},
		},
		{
			name: "refused write",
			req: request{method: http.MethodPost, target: "/-/api/v1/links?x=" + querySecret,
				headers: map[string]string{"Origin": "https://evil.example"}, body: validBody},
			want:   map[string]any{"status": 403.0, "area": "internal"},
			absent: []string{"route"},
		},
		{
			name: "UI page",
			req:  request{target: "/-/links/" + pathSecret + "/edit?pattern=" + querySecret},
			want: map[string]any{"area": "internal", "route": "/-/links/{name}/edit"},
		},
		{
			name: "UI 404",
			req:  request{target: "/-/nothing/" + pathSecret + "?pattern=" + querySecret},
			want: map[string]any{"status": 404.0, "area": "internal", "route": "/-/"},
		},
		{
			name: "unclean internal path",
			req:  request{target: "/-/../" + pathSecret + "?token=" + querySecret},
			want: map[string]any{"status": 404.0, "area": "internal"},
		},
		{
			name:   "refused host",
			req:    request{target: "/roadmap/" + pathSecret + "?token=" + querySecret, host: "evil.example:4655"},
			want:   map[string]any{"status": 403.0, "area": "host_rejected", "host": "evil.example:4655", "level": "WARN"},
			absent: []string{"link", "decision"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger, buf := captureLog()
			svc := &fakeService{resolveFn: func(string) app.Decision { return tt.decision }}
			// Headers can carry a go path too: the page a link was clicked
			// on, or a cookie. None of them belongs in the log either.
			req := tt.req
			req.headers = map[string]string{
				"Referer":    "http://localhost:4655/roadmap/" + pathSecret + "?token=" + querySecret,
				"Cookie":     "session=" + querySecret,
				"User-Agent": "agent-" + pathSecret,
				"X-Anything": querySecret,
			}
			for name, value := range tt.req.headers {
				req.headers[name] = value
			}
			req.send(newTestServer(svc, func(o *Options) { o.Logger = logger }).Handler())

			log := buf.String()
			for _, secret := range []string{pathSecret, strings.ToLower(pathSecret), querySecret, destSecret, "RoadMap", "token=", "?"} {
				if strings.Contains(log, secret) {
					t.Errorf("log contains %q:\n%s", secret, log)
				}
			}
			rec := requestRecord(t, log)
			for key, want := range tt.want {
				if rec[key] != want {
					t.Errorf("%s = %v, want %v\n%s", key, rec[key], want, log)
				}
			}
			for _, key := range tt.absent {
				if _, ok := rec[key]; ok {
					t.Errorf("record has %q = %v, want it absent\n%s", key, rec[key], log)
				}
			}
			if _, ok := rec["duration"].(float64); !ok {
				t.Errorf("record has no duration:\n%s", log)
			}
		})
	}
}

func TestRequestLogTruncatesLongNames(t *testing.T) {
	logger, buf := captureLog()
	long := strings.Repeat("a", 5000)
	svc := &fakeService{resolveFn: func(string) app.Decision {
		return app.Decision{Kind: app.DecisionMissUnknown, LinkName: long}
	}}
	get(newTestServer(svc, func(o *Options) { o.Logger = logger }).Handler(), "/"+long)

	link, _ := requestRecord(t, buf.String())["link"].(string)
	if len(link) > maxLoggedName+3 || !strings.HasPrefix(link, "aaaa") {
		t.Errorf("logged name has %d bytes, want at most %d", len(link), maxLoggedName+3)
	}
}

func TestServiceErrorIsLoggedNotReturned(t *testing.T) {
	logger, buf := captureLog()
	svc := &fakeService{createFn: func(app.Draft) (linkmodel.Link, error) {
		return linkmodel.Link{}, errors.New("disk is full")
	}}
	rec := postJSON(newTestServer(svc, func(o *Options) { o.Logger = logger }).Handler(), "/-/api/v1/links", validBody)

	if strings.Contains(rec.Body.String(), "disk is full") {
		t.Errorf("response reveals the internal error: %s", rec.Body.String())
	}
	log := buf.String()
	if !strings.Contains(log, "disk is full") {
		t.Errorf("the error is not in the log:\n%s", log)
	}
	if got := requestRecord(t, log); got["level"] != "ERROR" || got["status"] != 500.0 {
		t.Errorf("request record = %v, want level ERROR and status 500", got)
	}
}

func TestPanicBecomes500(t *testing.T) {
	logger, buf := captureLog()
	svc := &fakeService{resolveFn: func(string) app.Decision { panic("boom") }}
	rec := get(newTestServer(svc, func(o *Options) { o.Logger = logger }).Handler(), "/roadmap/S3cr3t-Path-Value?token=QUERY-SECRET")

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("the 500 lacks the security headers")
	}
	log := buf.String()
	if !strings.Contains(log, "boom") {
		t.Errorf("the panic is not in the log:\n%s", log)
	}
	for _, secret := range []string{"S3cr3t-Path-Value", "QUERY-SECRET"} {
		if strings.Contains(log, secret) {
			t.Errorf("log contains %q:\n%s", secret, log)
		}
	}
	if got := requestRecord(t, log); got["status"] != 500.0 || got["level"] != "ERROR" {
		t.Errorf("request record = %v, want status 500 at level ERROR", got)
	}
}

func TestNilLoggerIsFine(t *testing.T) {
	rec := get(newTestServer(&fakeService{}, func(o *Options) { o.Logger = nil }).Handler(), "/roadmap")
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}
