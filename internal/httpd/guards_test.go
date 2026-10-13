package httpd

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tfrydrychewicz/ogre/internal/app"
	"github.com/tfrydrychewicz/ogre/internal/web"
)

// A header value of "-" in these tables means "do not send the header".
func TestWriteGuards(t *testing.T) {
	tests := []struct {
		name         string
		contentType  string
		origin       string
		secFetchSite string
		wantStatus   int
		wantCode     string
	}{
		// What a script or curl sends: no browser headers at all.
		{"curl", "application/json", "-", "-", http.StatusCreated, ""},

		// Content-Type: a cross-site form cannot send JSON.
		{"no content type", "-", "-", "-", http.StatusUnsupportedMediaType, codeUnsupportedMediaType},
		{"form content type", "application/x-www-form-urlencoded", "-", "-", http.StatusUnsupportedMediaType, codeUnsupportedMediaType},
		{"plain text content type", "text/plain", "-", "-", http.StatusUnsupportedMediaType, codeUnsupportedMediaType},
		{"multipart content type", "multipart/form-data; boundary=x", "-", "-", http.StatusUnsupportedMediaType, codeUnsupportedMediaType},
		{"almost JSON", "application/jsonx", "-", "-", http.StatusUnsupportedMediaType, codeUnsupportedMediaType},
		{"JSON suffix type", "application/problem+json", "-", "-", http.StatusUnsupportedMediaType, codeUnsupportedMediaType},
		{"two types", "application/json, text/plain", "-", "-", http.StatusUnsupportedMediaType, codeUnsupportedMediaType},
		{"JSON with charset", "application/json; charset=utf-8", "-", "-", http.StatusCreated, ""},
		{"JSON upper case", "Application/JSON", "-", "-", http.StatusCreated, ""},

		// Origin, when present, must be our own.
		{"own origin", "application/json", "http://localhost:4655", "-", http.StatusCreated, ""},
		{"own origin upper case", "application/json", "HTTP://LOCALHOST:4655", "-", http.StatusCreated, ""},
		{"other site", "application/json", "https://evil.example", "-", http.StatusForbidden, codeCrossSite},
		{"our name on another port", "application/json", "http://localhost:3000", "-", http.StatusForbidden, codeCrossSite},
		{"our name with another scheme", "application/json", "https://localhost:4655", "-", http.StatusForbidden, codeCrossSite},
		{"our other loopback name", "application/json", "http://127.0.0.1:4655", "-", http.StatusForbidden, codeCrossSite},
		{"our origin as a prefix", "application/json", "http://localhost:4655.evil.example", "-", http.StatusForbidden, codeCrossSite},
		{"opaque origin", "application/json", "null", "-", http.StatusForbidden, codeCrossSite},
		{"extension origin", "application/json", "chrome-extension://abcdefghijklmnop", "-", http.StatusForbidden, codeCrossSite},

		// Sec-Fetch-Site, when present, must be same-origin or none.
		{"fetch from our own page", "application/json", "http://localhost:4655", "same-origin", http.StatusCreated, ""},
		{"user-initiated", "application/json", "-", "none", http.StatusCreated, ""},
		{"cross-site fetch", "application/json", "-", "cross-site", http.StatusForbidden, codeCrossSite},
		{"same-site fetch (another port)", "application/json", "-", "same-site", http.StatusForbidden, codeCrossSite},
		{"unknown value", "application/json", "-", "something-new", http.StatusForbidden, codeCrossSite},
		{"cross-site wins over own origin", "application/json", "http://localhost:4655", "cross-site", http.StatusForbidden, codeCrossSite},
		{"other origin wins over same-origin", "application/json", "https://evil.example", "same-origin", http.StatusForbidden, codeCrossSite},

		// A cross-site form: refused as cross-site before the content type
		// is even looked at.
		{"cross-site form", "application/x-www-form-urlencoded", "https://evil.example", "cross-site", http.StatusForbidden, codeCrossSite},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &fakeService{}
			headers := map[string]string{}
			for name, value := range map[string]string{"Content-Type": tt.contentType, "Origin": tt.origin, "Sec-Fetch-Site": tt.secFetchSite} {
				if value != "-" {
					headers[name] = value
				}
			}
			rec := request{method: http.MethodPost, target: "/-/api/v1/links", headers: headers, body: validBody}.
				send(newTestServer(svc, nil).Handler())

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body: %s", rec.Code, tt.wantStatus, rec.Body.String())
			}
			assertNoCORS(t, rec.Header())
			if tt.wantStatus == http.StatusCreated {
				if len(svc.createdDrafts()) != 1 {
					t.Errorf("link was not created")
				}
				return
			}
			if got := errorCode(t, rec); got != tt.wantCode {
				t.Errorf("error code = %q, want %q", got, tt.wantCode)
			}
			if len(svc.createdDrafts()) != 0 {
				t.Errorf("a refused request created a link")
			}
		})
	}
}

// A browser sends each of these headers once. A request that repeats one, or
// sends it empty, is refused whichever copy is the acceptable one: the guard
// must not depend on the order in which something in front of Ogre happened
// to forward the headers.
func TestWriteGuardsJudgeEachHeaderAsAWhole(t *testing.T) {
	const own = "http://localhost:4655"
	tests := []struct {
		name       string
		header     http.Header
		wantStatus int
		wantCode   string
	}{
		{"plain", http.Header{"Content-Type": {"application/json"}}, http.StatusCreated, ""},
		{"own origin once", http.Header{"Content-Type": {"application/json"}, "Origin": {own}}, http.StatusCreated, ""},

		{"Origin: own, then another site", http.Header{"Content-Type": {"application/json"}, "Origin": {own, "https://evil.example"}}, http.StatusForbidden, codeCrossSite},
		{"Origin: another site, then own", http.Header{"Content-Type": {"application/json"}, "Origin": {"https://evil.example", own}}, http.StatusForbidden, codeCrossSite},
		{"Origin: own twice", http.Header{"Content-Type": {"application/json"}, "Origin": {own, own}}, http.StatusForbidden, codeCrossSite},
		{"Origin: empty", http.Header{"Content-Type": {"application/json"}, "Origin": {""}}, http.StatusForbidden, codeCrossSite},
		{"Origin: two in one value", http.Header{"Content-Type": {"application/json"}, "Origin": {own + ", https://evil.example"}}, http.StatusForbidden, codeCrossSite},

		{"Sec-Fetch-Site: same-origin, then cross-site", http.Header{"Content-Type": {"application/json"}, "Sec-Fetch-Site": {"same-origin", "cross-site"}}, http.StatusForbidden, codeCrossSite},
		{"Sec-Fetch-Site: cross-site, then same-origin", http.Header{"Content-Type": {"application/json"}, "Sec-Fetch-Site": {"cross-site", "same-origin"}}, http.StatusForbidden, codeCrossSite},
		{"Sec-Fetch-Site: none twice", http.Header{"Content-Type": {"application/json"}, "Sec-Fetch-Site": {"none", "none"}}, http.StatusForbidden, codeCrossSite},
		{"Sec-Fetch-Site: empty", http.Header{"Content-Type": {"application/json"}, "Sec-Fetch-Site": {""}}, http.StatusForbidden, codeCrossSite},
		{"Sec-Fetch-Site: two in one value", http.Header{"Content-Type": {"application/json"}, "Sec-Fetch-Site": {"same-origin, cross-site"}}, http.StatusForbidden, codeCrossSite},

		{"Content-Type: JSON, then plain text", http.Header{"Content-Type": {"application/json", "text/plain"}}, http.StatusUnsupportedMediaType, codeUnsupportedMediaType},
		{"Content-Type: plain text, then JSON", http.Header{"Content-Type": {"text/plain", "application/json"}}, http.StatusUnsupportedMediaType, codeUnsupportedMediaType},
		{"Content-Type: JSON twice", http.Header{"Content-Type": {"application/json", "application/json"}}, http.StatusUnsupportedMediaType, codeUnsupportedMediaType},
		{"Content-Type: empty", http.Header{"Content-Type": {""}}, http.StatusUnsupportedMediaType, codeUnsupportedMediaType},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &fakeService{}
			req := httptest.NewRequest(http.MethodPost, "/-/api/v1/links", strings.NewReader(validBody))
			req.Host = testHost
			req.Header = tt.header
			rec := httptest.NewRecorder()
			newTestServer(svc, nil).Handler().ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body: %s", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if tt.wantStatus == http.StatusCreated {
				if len(svc.createdDrafts()) != 1 {
					t.Error("link was not created")
				}
				return
			}
			if got := errorCode(t, rec); got != tt.wantCode {
				t.Errorf("error code = %q, want %q", got, tt.wantCode)
			}
			if len(svc.createdDrafts()) != 0 {
				t.Error("a refused request created a link")
			}
		})
	}
}

// The guards cover every write under "/-/", including paths the UI handler
// serves and methods no endpoint uses yet.
func TestWriteGuardsCoverEveryInternalPath(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		for _, target := range []string{"/-/", "/-/new", "/-/api/v1/links", "/-/api/v1/links/12", "/-/healthz"} {
			t.Run(method+" "+target, func(t *testing.T) {
				reached := false
				ui := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached = true })
				svc := &fakeService{}
				h := newTestServer(svc, func(o *Options) { o.UI = ui }).Handler()

				crossSite := request{method: method, target: target, body: "{}", headers: map[string]string{
					"Content-Type": "application/json", "Origin": "https://evil.example", "Sec-Fetch-Site": "cross-site",
				}}.send(h)
				if crossSite.Code != http.StatusForbidden {
					t.Errorf("cross-site status = %d, want 403", crossSite.Code)
				}

				form := request{method: method, target: target, body: "a=b", headers: map[string]string{
					"Content-Type": "application/x-www-form-urlencoded",
				}}.send(h)
				if form.Code != http.StatusUnsupportedMediaType {
					t.Errorf("form status = %d, want 415", form.Code)
				}

				if reached || len(svc.createdDrafts()) != 0 {
					t.Errorf("a refused write reached a handler")
				}
			})
		}
	}
}

// Reads are not guarded: what protects them is the absence of CORS headers,
// which stops a page on another site from seeing the answer.
func TestReadsAreNotGuardedAndCarryNoCORS(t *testing.T) {
	h := newTestServer(&fakeService{}, nil).Handler()
	for _, target := range []string{"/-/api/v1/links", "/-/api/v1/meta", "/-/healthz"} {
		rec := request{target: target, headers: map[string]string{
			"Origin": "https://other.example", "Sec-Fetch-Site": "cross-site",
		}}.send(h)
		if rec.Code != http.StatusOK {
			t.Errorf("%s status = %d, want 200", target, rec.Code)
		}
		assertNoCORS(t, rec.Header())
	}
}

func TestPreflightIsNeverApproved(t *testing.T) {
	for _, target := range []string{"/-/api/v1/links", "/-/api/v1/meta", "/-/healthz", "/-/", "/-/static/site.css", "/roadmap", "/"} {
		t.Run(target, func(t *testing.T) {
			svc := &fakeService{}
			h := newTestServer(svc, func(o *Options) { o.UI = web.Handler(testVersion) }).Handler()
			rec := request{method: http.MethodOptions, target: target, headers: map[string]string{
				"Origin":                         "https://other.example",
				"Access-Control-Request-Method":  "POST",
				"Access-Control-Request-Headers": "content-type",
				"Sec-Fetch-Site":                 "cross-site",
				"Sec-Fetch-Mode":                 "cors",
			}}.send(h)

			if rec.Code != http.StatusMethodNotAllowed && rec.Code != http.StatusForbidden {
				t.Errorf("status = %d, want 405 or 403", rec.Code)
			}
			assertNoCORS(t, rec.Header())
			if len(svc.resolvedPaths()) != 0 || len(svc.createdDrafts()) != 0 {
				t.Errorf("a preflight reached the service")
			}
		})
	}
}

func TestSecurityHeadersOnEveryResponse(t *testing.T) {
	hit := func(string) app.Decision {
		return app.Decision{Kind: app.DecisionHit, LinkName: "roadmap", Destination: "https://docs.example.com/"}
	}
	tests := []struct {
		name            string
		req             request
		wantStatus      int
		wantContentType string
	}{
		{"HTML page", request{target: "/-/"}, http.StatusOK, "text/html; charset=utf-8"},
		{"stylesheet", request{target: "/-/static/site.css"}, http.StatusOK, "text/css; charset=utf-8"},
		{"UI 404", request{target: "/-/nothing"}, http.StatusNotFound, "text/plain; charset=utf-8"},
		{"JSON", request{target: "/-/api/v1/meta"}, http.StatusOK, "application/json"},
		{"JSON error", request{target: "/-/api/v1/nothing"}, http.StatusNotFound, "application/json"},
		{"health check", request{target: "/-/healthz"}, http.StatusOK, "text/plain; charset=utf-8"},
		{"hit", request{target: "/roadmap"}, http.StatusFound, "text/plain; charset=utf-8"},
		{"home redirect", request{target: "/"}, http.StatusFound, "text/plain; charset=utf-8"},
		{"reserved path", request{target: "/favicon.ico"}, http.StatusNotFound, "text/plain; charset=utf-8"},
		{"refused host", request{target: "/-/", host: "evil.example:4655"}, http.StatusForbidden, "text/plain; charset=utf-8"},
		{"refused method", request{method: http.MethodPost, target: "/roadmap"}, http.StatusMethodNotAllowed, "text/plain; charset=utf-8"},
		{"refused write", request{method: http.MethodPost, target: "/-/api/v1/links"}, http.StatusUnsupportedMediaType, "application/json"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &fakeService{resolveFn: hit}
			h := newTestServer(svc, func(o *Options) { o.UI = web.Handler(testVersion) }).Handler()
			rec := tt.req.send(h)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			hdr := rec.Header()
			if got := hdr.Get("Content-Type"); got != tt.wantContentType {
				t.Errorf("Content-Type = %q, want %q", got, tt.wantContentType)
			}
			if got := hdr.Get("X-Content-Type-Options"); got != "nosniff" {
				t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
			}
			if got := hdr.Get("Referrer-Policy"); got != "same-origin" {
				t.Errorf("Referrer-Policy = %q, want same-origin", got)
			}
			csp := hdr.Get("Content-Security-Policy")
			for _, want := range []string{"default-src 'self'", "frame-ancestors 'none'"} {
				if !strings.Contains(csp, want) {
					t.Errorf("Content-Security-Policy %q lacks %q", csp, want)
				}
			}
			for _, banned := range []string{"unsafe-inline", "unsafe-eval", "*", "data:", "http:", "https:"} {
				if strings.Contains(csp, banned) {
					t.Errorf("Content-Security-Policy %q contains %q", csp, banned)
				}
			}
			if len(hdr.Values("Content-Security-Policy")) != 1 {
				t.Errorf("want exactly one Content-Security-Policy header, got %q", hdr.Values("Content-Security-Policy"))
			}
			assertNoCORS(t, hdr)
		})
	}
}

// The placeholder page must work under the policy the server sends with it.
func TestPlaceholderPageObeysThePolicy(t *testing.T) {
	h := newTestServer(&fakeService{}, func(o *Options) { o.UI = web.Handler(testVersion) }).Handler()
	body := strings.ToLower(get(h, "/-/").Body.String())

	if !strings.Contains(body, testVersion) {
		t.Errorf("page does not show the version:\n%s", body)
	}
	for _, banned := range []string{"<script", "<style", "style=", "http://", "https://", "//cdn"} {
		if strings.Contains(body, banned) {
			t.Errorf("page contains %q, which the policy would block", banned)
		}
	}
}
