package httpd

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/tfrydrychewicz/godir/internal/app"
)

func TestResolverResponses(t *testing.T) {
	tests := []struct {
		name         string
		decision     app.Decision
		wantStatus   int
		wantLocation string
		wantInBody   string
	}{
		{
			name:         "hit",
			decision:     app.Decision{Kind: app.DecisionHit, LinkName: "roadmap", Destination: "https://docs.example.com/roadmap?a=1&b=2#top"},
			wantStatus:   http.StatusFound,
			wantLocation: "https://docs.example.com/roadmap?a=1&b=2#top",
		},
		{
			name:       "miss, unknown name",
			decision:   app.Decision{Kind: app.DecisionMissUnknown, LinkName: "raodmap"},
			wantStatus: http.StatusNotFound,
			wantInBody: "go/raodmap",
		},
		{
			name:       "miss, known name",
			decision:   app.Decision{Kind: app.DecisionMissKnown, LinkName: "roadmap"},
			wantStatus: http.StatusNotFound,
			wantInBody: "go/roadmap",
		},
		{
			name:       "bad request",
			decision:   app.Decision{Kind: app.DecisionBadRequest},
			wantStatus: http.StatusBadRequest,
		},
		{
			name:         "home (a path of only slashes)",
			decision:     app.Decision{Kind: app.DecisionHome},
			wantStatus:   http.StatusFound,
			wantLocation: "/-/",
		},
		{
			name:       "a decision kind this server does not know",
			decision:   app.Decision{Kind: app.DecisionKind(99)},
			wantStatus: http.StatusInternalServerError,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &fakeService{resolveFn: func(string) app.Decision { return tt.decision }}
			h := newTestServer(svc, nil).Handler()

			getRec := request{method: http.MethodGet, target: "/whatever/path"}.send(h)
			headRec := request{method: http.MethodHead, target: "/whatever/path"}.send(h)

			for method, rec := range map[string]*httptest.ResponseRecorder{"GET": getRec, "HEAD": headRec} {
				resp := rec.Result()
				if resp.StatusCode != tt.wantStatus {
					t.Errorf("%s status = %d, want %d", method, resp.StatusCode, tt.wantStatus)
				}
				if got := resp.Header.Get("Location"); got != tt.wantLocation {
					t.Errorf("%s Location = %q, want %q", method, got, tt.wantLocation)
				}
				if got := resp.Header.Get("Cache-Control"); got != "no-store" {
					t.Errorf("%s Cache-Control = %q, want no-store", method, got)
				}
				if got := resp.Header.Get("Content-Type"); got != "text/plain; charset=utf-8" {
					t.Errorf("%s Content-Type = %q, want plain text", method, got)
				}
			}

			if !strings.Contains(getRec.Body.String(), tt.wantInBody) {
				t.Errorf("GET body %q does not contain %q", getRec.Body.String(), tt.wantInBody)
			}
			if getRec.Body.Len() == 0 {
				t.Error("GET has no body")
			}
			if headRec.Body.Len() != 0 {
				t.Errorf("HEAD wrote a body: %q", headRec.Body.String())
			}
			if !reflect.DeepEqual(getRec.Header(), headRec.Header()) {
				t.Errorf("HEAD headers differ from GET:\n GET  %v\n HEAD %v", getRec.Header(), headRec.Header())
			}
			if want := []string{"/whatever/path", "/whatever/path"}; !reflect.DeepEqual(svc.resolvedPaths(), want) {
				t.Errorf("resolved %v, want %v", svc.resolvedPaths(), want)
			}
		})
	}
}

func TestResolverAcceptsOnlyGetAndHead(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions, "PROPFIND"} {
		t.Run(method, func(t *testing.T) {
			svc := &fakeService{resolveFn: func(string) app.Decision {
				return app.Decision{Kind: app.DecisionHit, LinkName: "roadmap", Destination: "https://docs.example.com/"}
			}}
			h := newTestServer(svc, nil).Handler()

			// Even a request that would pass the write guards is refused:
			// the resolver is read-only.
			rec := request{
				method: method, target: "/roadmap",
				headers: map[string]string{"Content-Type": "application/json"}, body: "{}",
			}.send(h)

			if rec.Code != http.StatusMethodNotAllowed {
				t.Errorf("status = %d, want 405", rec.Code)
			}
			if got := rec.Header().Get("Allow"); got != "GET, HEAD" {
				t.Errorf("Allow = %q, want \"GET, HEAD\"", got)
			}
			if got := rec.Header().Get("Location"); got != "" {
				t.Errorf("Location = %q, want none", got)
			}
			if got := svc.resolvedPaths(); len(got) != 0 {
				t.Errorf("resolver was called: %v", got)
			}
			assertNoCORS(t, rec.Header())
		})
	}
}

func TestRootRedirectsToUI(t *testing.T) {
	tests := []struct {
		method     string
		target     string
		wantStatus int
	}{
		{http.MethodGet, "/", http.StatusFound},
		{http.MethodHead, "/", http.StatusFound},
		{http.MethodGet, "/?q=1", http.StatusFound},
		{http.MethodGet, "/-", http.StatusFound},
		{http.MethodPost, "/", http.StatusMethodNotAllowed},
		{http.MethodDelete, "/-", http.StatusMethodNotAllowed},
	}
	for _, tt := range tests {
		t.Run(tt.method+" "+tt.target, func(t *testing.T) {
			svc := &fakeService{}
			rec := request{method: tt.method, target: tt.target}.send(newTestServer(svc, nil).Handler())

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			wantLocation := ""
			if tt.wantStatus == http.StatusFound {
				wantLocation = "/-/"
			}
			if got := rec.Header().Get("Location"); got != wantLocation {
				t.Errorf("Location = %q, want %q", got, wantLocation)
			}
			if got := svc.resolvedPaths(); len(got) != 0 {
				t.Errorf("resolver was called: %v", got)
			}
		})
	}
}

func TestReservedPathsAreNeverResolved(t *testing.T) {
	paths := []string{
		"/favicon.ico",
		"/robots.txt",
		"/sitemap.xml",
		"/apple-touch-icon.png",
		"/apple-touch-icon-precomposed.png",
		"/apple-touch-icon-120x120.png",
		"/.well-known/anything",
		"/.well-known/acme-challenge/token",
		"/.well-known",
		"/FAVICON.ICO",
		"/favicon.ico?v=2",
		"/favicon.ico/extra",
		"//favicon.ico",
	}
	for _, path := range paths {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			t.Run(method+" "+path, func(t *testing.T) {
				svc := &fakeService{resolveFn: func(string) app.Decision {
					return app.Decision{Kind: app.DecisionHit, Destination: "https://wrong.example/"}
				}}
				rec := request{method: method, target: path}.send(newTestServer(svc, nil).Handler())

				if rec.Code != http.StatusNotFound {
					t.Errorf("status = %d, want 404", rec.Code)
				}
				// A browser that cached this 404 would keep asking nobody.
				if got := rec.Header().Get("Cache-Control"); got != "no-store" {
					t.Errorf("Cache-Control = %q, want no-store", got)
				}
				if got := svc.resolvedPaths(); len(got) != 0 {
					t.Errorf("resolver was called: %v", got)
				}
			})
		}
	}
}

func TestNearReservedPathsAreResolved(t *testing.T) {
	for _, path := range []string{"/favicon", "/robots", "/apple-touch-icon", "/well-known/x", "/a/favicon.ico", "/-x", "/--/x"} {
		t.Run(path, func(t *testing.T) {
			svc := &fakeService{}
			get(newTestServer(svc, nil).Handler(), path)
			if want := []string{path}; !reflect.DeepEqual(svc.resolvedPaths(), want) {
				t.Errorf("resolved %v, want %v", svc.resolvedPaths(), want)
			}
		})
	}
}

// The resolver receives r.URL.EscapedPath() exactly as it arrived, leading
// slash included. Nothing is cleaned, merged, decoded or redirected on the
// way, and the query string is left behind.
func TestRawPathReachesResolverUntouched(t *testing.T) {
	tests := []struct {
		target string
		want   string
	}{
		{"//double", "//double"},
		{"/a/../b", "/a/../b"},
		{"//a/../b", "//a/../b"},
		{"/a/./b", "/a/./b"},
		{"/a//b/", "/a//b/"},
		{"/a%2Fb", "/a%2Fb"},
		{"/a%2fb", "/a%2fb"},
		{"/a%20b", "/a%20b"},
		{"/a+b", "/a+b"},
		{"/Mixed/Case", "/Mixed/Case"},
		{"/gh/caf%C3%A9", "/gh/caf%C3%A9"},
		{"/%2D/x", "/%2D/x"},
		{"/a%00b", "/a%00b"},
		{"/a%FFb", "/a%FFb"},
		{"/x?secret=1", "/x"},
		{"/x/?a=/../b", "/x/"},
		{"/a/../-/api/v1/meta", "/a/../-/api/v1/meta"},
	}
	for _, tt := range tests {
		t.Run(tt.target, func(t *testing.T) {
			svc := &fakeService{}
			rec := get(newTestServer(svc, nil).Handler(), tt.target)

			if want := []string{tt.want}; !reflect.DeepEqual(svc.resolvedPaths(), want) {
				t.Errorf("resolved %q, want %q", svc.resolvedPaths(), want)
			}
			// The fake answers "miss": anything else means the server
			// answered by itself, for example with a cleaning redirect.
			if rec.Code != http.StatusNotFound {
				t.Errorf("status = %d, want the resolver's 404", rec.Code)
			}
			if got := rec.Header().Get("Location"); got != "" {
				t.Errorf("Location = %q, want none", got)
			}
		})
	}
}

// A go link clicked on another site arrives cross-site (L0 spike, check 4),
// so the write guards must not apply to the resolver.
func TestResolverAllowsCrossSiteNavigation(t *testing.T) {
	svc := &fakeService{resolveFn: func(string) app.Decision {
		return app.Decision{Kind: app.DecisionHit, LinkName: "roadmap", Destination: "https://docs.example.com/roadmap"}
	}}
	h := newTestServer(svc, nil).Handler()

	for _, method := range []string{http.MethodGet, http.MethodHead} {
		rec := request{
			method: method, target: "/roadmap",
			headers: map[string]string{
				"Sec-Fetch-Site": "cross-site",
				"Sec-Fetch-Mode": "navigate",
				"Sec-Fetch-Dest": "document",
				"Sec-Fetch-User": "?1",
				"Origin":         "https://other.example",
				"Referer":        "https://other.example/page",
			},
		}.send(h)

		if rec.Code != http.StatusFound {
			t.Errorf("%s status = %d, want 302", method, rec.Code)
		}
		if got := rec.Header().Get("Location"); got != "https://docs.example.com/roadmap" {
			t.Errorf("%s Location = %q", method, got)
		}
		assertNoCORS(t, rec.Header())
	}
}

// A page of another site must not be able to learn which go links exist by
// loading them as images or frames: a hit would redirect and a miss would
// not, and the page can tell (its own content security policy reports a
// redirect that leaves the Directory). Such requests get one fixed answer
// before the link is even looked up. A navigation of the whole tab, which is
// a go link being clicked or typed, is served as always.
func TestResolverRefusesCrossSiteSubresources(t *testing.T) {
	tests := []struct {
		name       string
		headers    map[string]string
		wantServed bool
	}{
		// Not a page's doing, or the user's own navigation.
		{"no fetch metadata (curl, old browsers)", nil, true},
		{"typed in the address bar", map[string]string{"Sec-Fetch-Site": "none", "Sec-Fetch-Mode": "navigate", "Sec-Fetch-Dest": "document", "Sec-Fetch-User": "?1"}, true},
		{"link clicked on another site", map[string]string{"Sec-Fetch-Site": "cross-site", "Sec-Fetch-Mode": "navigate", "Sec-Fetch-Dest": "document", "Sec-Fetch-User": "?1"}, true},
		{"link clicked on another localhost port", map[string]string{"Sec-Fetch-Site": "same-site", "Sec-Fetch-Mode": "navigate", "Sec-Fetch-Dest": "document"}, true},
		{"link clicked on the Directory's own page", map[string]string{"Sec-Fetch-Site": "same-origin", "Sec-Fetch-Mode": "navigate", "Sec-Fetch-Dest": "document"}, true},
		{"the Directory's own page loads it", map[string]string{"Sec-Fetch-Site": "same-origin", "Sec-Fetch-Mode": "no-cors", "Sec-Fetch-Dest": "image"}, true},
		{"the Directory's own script fetches it", map[string]string{"Sec-Fetch-Site": "same-origin", "Sec-Fetch-Mode": "cors", "Sec-Fetch-Dest": "empty"}, true},

		// Another site's page probing.
		{"image", map[string]string{"Sec-Fetch-Site": "cross-site", "Sec-Fetch-Mode": "no-cors", "Sec-Fetch-Dest": "image"}, false},
		{"image from another localhost port", map[string]string{"Sec-Fetch-Site": "same-site", "Sec-Fetch-Mode": "no-cors", "Sec-Fetch-Dest": "image"}, false},
		{"iframe", map[string]string{"Sec-Fetch-Site": "cross-site", "Sec-Fetch-Mode": "navigate", "Sec-Fetch-Dest": "iframe"}, false},
		{"frame", map[string]string{"Sec-Fetch-Site": "cross-site", "Sec-Fetch-Mode": "navigate", "Sec-Fetch-Dest": "frame"}, false},
		{"embed", map[string]string{"Sec-Fetch-Site": "cross-site", "Sec-Fetch-Mode": "navigate", "Sec-Fetch-Dest": "embed"}, false},
		{"object", map[string]string{"Sec-Fetch-Site": "cross-site", "Sec-Fetch-Mode": "navigate", "Sec-Fetch-Dest": "object"}, false},
		{"script", map[string]string{"Sec-Fetch-Site": "cross-site", "Sec-Fetch-Mode": "no-cors", "Sec-Fetch-Dest": "script"}, false},
		{"stylesheet", map[string]string{"Sec-Fetch-Site": "cross-site", "Sec-Fetch-Mode": "no-cors", "Sec-Fetch-Dest": "style"}, false},
		{"fetch", map[string]string{"Sec-Fetch-Site": "cross-site", "Sec-Fetch-Mode": "cors", "Sec-Fetch-Dest": "empty", "Origin": "https://evil.example"}, false},
		{"fetch without CORS", map[string]string{"Sec-Fetch-Site": "cross-site", "Sec-Fetch-Mode": "no-cors", "Sec-Fetch-Dest": "empty"}, false},
		{"document that is not a navigation", map[string]string{"Sec-Fetch-Site": "cross-site", "Sec-Fetch-Mode": "no-cors", "Sec-Fetch-Dest": "document"}, false},
		{"cross-site with nothing else", map[string]string{"Sec-Fetch-Site": "cross-site"}, false},
		{"a value this server does not know", map[string]string{"Sec-Fetch-Site": "something-new", "Sec-Fetch-Mode": "no-cors", "Sec-Fetch-Dest": "image"}, false},
		{"an empty value", map[string]string{"Sec-Fetch-Site": "", "Sec-Fetch-Mode": "no-cors", "Sec-Fetch-Dest": "image"}, false},
	}
	decisions := map[string]app.Decision{
		"/bank":         {Kind: app.DecisionHit, LinkName: "bank", Destination: "https://bank.example/login"},
		"/bank/nothing": {Kind: app.DecisionMissKnown, LinkName: "bank"},
		"/no-such-link": {Kind: app.DecisionMissUnknown, LinkName: "no-such-link"},
		"/%ff":          {Kind: app.DecisionBadRequest},
	}
	wantServed := map[string]int{"/bank": 302, "/bank/nothing": 404, "/no-such-link": 404, "/%ff": 400}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &fakeService{resolveFn: func(path string) app.Decision { return decisions[path] }}
			logger, logs := captureLog()
			h := newTestServer(svc, func(o *Options) { o.Logger = logger }).Handler()

			var refusals []string
			for _, method := range []string{http.MethodGet, http.MethodHead} {
				for path, served := range wantServed {
					req := httptest.NewRequest(method, path, nil)
					req.Host = testHost
					for name, value := range tt.headers {
						req.Header[name] = []string{value} // also sends an empty value
					}
					rec := httptest.NewRecorder()
					h.ServeHTTP(rec, req)

					if tt.wantServed {
						if rec.Code != served {
							t.Errorf("%s %s status = %d, want %d", method, path, rec.Code, served)
						}
						continue
					}
					if rec.Code != http.StatusForbidden {
						t.Errorf("%s %s status = %d, want 403", method, path, rec.Code)
					}
					if got := rec.Header().Get("Location"); got != "" {
						t.Errorf("%s %s: a refusal carries Location %q", method, path, got)
					}
					if got := rec.Header().Get("Cache-Control"); got != "no-store" {
						t.Errorf("%s %s Cache-Control = %q, want no-store", method, path, got)
					}
					assertNoCORS(t, rec.Header())
					// Everything a page could tell apart must be the same
					// for a link that exists and one that does not.
					rec.Header().Del("Date")
					refusals = append(refusals, fmt.Sprint(rec.Code, rec.Header(), rec.Body.String()))
				}
			}
			if tt.wantServed {
				return
			}
			if len(svc.resolvedPaths()) != 0 {
				t.Errorf("a refused request was resolved: %q", svc.resolvedPaths())
			}
			gets, heads := refusals[:len(wantServed)], refusals[len(wantServed):]
			for _, group := range [][]string{gets, heads} {
				for _, r := range group {
					if r != group[0] {
						t.Errorf("refusals differ between paths:\n%s\n%s", group[0], r)
					}
				}
			}
			// The log says a request was refused, and nothing about what
			// was asked for.
			for _, e := range logEntries(t, logs) {
				if e["decision"] != "refused_cross_site" || e["area"] != "resolver" || e["link"] != nil {
					t.Errorf("log record = %v, want a refusal that names no link", e)
				}
			}
			for _, leaked := range []string{"bank", "no-such-link"} {
				if strings.Contains(logs.String(), leaked) {
					t.Errorf("the log of refused requests contains %q:\n%s", leaked, logs.String())
				}
			}
		})
	}
}

// A header a browser sends once, sent twice: the request is judged as not a
// plain navigation, whichever copy says what.
func TestResolverJudgesRepeatedFetchMetadataAsAProbe(t *testing.T) {
	svc := &fakeService{resolveFn: func(string) app.Decision {
		return app.Decision{Kind: app.DecisionHit, LinkName: "bank", Destination: "https://bank.example/"}
	}}
	h := newTestServer(svc, nil).Handler()
	tests := []struct {
		name   string
		header http.Header
	}{
		{"site: none then cross-site", http.Header{"Sec-Fetch-Site": {"none", "cross-site"}, "Sec-Fetch-Mode": {"no-cors"}, "Sec-Fetch-Dest": {"image"}}},
		{"site: same-origin twice", http.Header{"Sec-Fetch-Site": {"same-origin", "same-origin"}, "Sec-Fetch-Mode": {"no-cors"}, "Sec-Fetch-Dest": {"image"}}},
		{"mode: navigate then no-cors", http.Header{"Sec-Fetch-Site": {"cross-site"}, "Sec-Fetch-Mode": {"navigate", "no-cors"}, "Sec-Fetch-Dest": {"document"}}},
		{"dest: document then image", http.Header{"Sec-Fetch-Site": {"cross-site"}, "Sec-Fetch-Mode": {"navigate"}, "Sec-Fetch-Dest": {"document", "image"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/bank", nil)
			req.Host = testHost
			req.Header = tt.header
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusForbidden || rec.Header().Get("Location") != "" {
				t.Errorf("status = %d, Location = %q; want 403 and no Location", rec.Code, rec.Header().Get("Location"))
			}
		})
	}
}

// An unknown-name miss repeats the requested name only when the use case
// passed one on; without it the page says nothing about the request.
func TestMissWithoutANameRepeatsNothing(t *testing.T) {
	svc := &fakeService{resolveFn: func(string) app.Decision { return app.Decision{Kind: app.DecisionMissUnknown} }}
	logger, logs := captureLog()
	h := newTestServer(svc, func(o *Options) { o.Logger = logger }).Handler()

	for _, target := range []string{
		"/Secret-Name%2Fsecret-value?token=abc",
		"/x%0D%0ASet-Cookie:%20a=b",
		"/%3Cscript%3Ealert(1)%3C%2Fscript%3E",
		"/" + strings.Repeat("a", 5000),
	} {
		rec := get(h, target)
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %.40s status = %d, want 404", target, rec.Code)
		}
		if got := rec.Body.String(); got != "404: that is not a go link.\n" {
			t.Errorf("GET %.40s body = %q, want the fixed line", target, got)
		}
	}
	for _, leaked := range []string{"secret", "Secret", "token", "Set-Cookie", "script", "aaaa"} {
		if strings.Contains(logs.String(), leaked) {
			t.Errorf("the log contains %q:\n%.600s", leaked, logs.String())
		}
	}
	for _, e := range logEntries(t, logs) {
		if e["decision"] != "miss_unknown" || e["link"] != nil {
			t.Errorf("log record = %v, want a miss that names no link", e)
		}
	}
}

func TestRedirectSendsDestinationVerbatim(t *testing.T) {
	svc := &fakeService{resolveFn: func(string) app.Decision {
		return app.Decision{Kind: app.DecisionHit, LinkName: "roadmap", Destination: verbatimDestination}
	}}
	rec := get(newTestServer(svc, nil).Handler(), "/roadmap")
	if got := rec.Header().Get("Location"); got != verbatimDestination {
		t.Errorf("Location = %q, want %q byte for byte", got, verbatimDestination)
	}
}

// A header value must be ASCII to mean the same to every client (RES-6), so
// the non-ASCII bytes of a destination are percent-encoded, as a browser
// would encode them, and nothing else is touched.
func TestRedirectLocationIsASCII(t *testing.T) {
	tests := []struct {
		destination string
		want        string
	}{
		{"https://example.com/żółć?q=ü#frag", "https://example.com/%C5%BC%C3%B3%C5%82%C4%87?q=%C3%BC#frag"},
		{"https://example.com/日本語", "https://example.com/%E6%97%A5%E6%9C%AC%E8%AA%9E"},
		{"https://example.com/a%20b%2Fc?x=%C3%BC&Y=Z", "https://example.com/a%20b%2Fc?x=%C3%BC&Y=Z"},
		{"https://User:Secret@Example.com:8443/P?Q=1#F", "https://User:Secret@Example.com:8443/P?Q=1#F"},
		{"/-/", "/-/"},
	}
	for _, tt := range tests {
		svc := &fakeService{resolveFn: func(string) app.Decision {
			return app.Decision{Kind: app.DecisionHit, LinkName: "x", Destination: tt.destination}
		}}
		rec := get(newTestServer(svc, nil).Handler(), "/x")
		got := rec.Header().Get("Location")
		if got != tt.want {
			t.Errorf("Location for %q = %q, want %q", tt.destination, got, tt.want)
		}
		for i := 0; i < len(got); i++ {
			if got[i] <= ' ' || got[i] >= 0x7f {
				t.Errorf("Location %q has the non-ASCII or control byte %#x", got, got[i])
			}
		}
		if !strings.Contains(rec.Body.String(), tt.want) {
			t.Errorf("body %q does not name the address sent", rec.Body.String())
		}
	}
	// Defence in depth: the link model refuses these, and even so nothing
	// that could split a header or hide in it goes out raw.
	if got := asciiURL("https://example.com/a b\r\nX: y\x00\x7f"); got != "https://example.com/a%20b%0D%0AX:%20y%00%7F" {
		t.Errorf("asciiURL of a hostile string = %q", got)
	}
}
