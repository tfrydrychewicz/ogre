package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func serve(t *testing.T, h http.Handler, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, target, nil))
	return rec
}

func TestPlaceholderPage(t *testing.T) {
	rec := serve(t, Handler("1.2.3-test"), http.MethodGet, "/-/")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q", got)
	}
	body := rec.Body.String()
	for _, want := range []string{"<!doctype html>", "Ogre is running", "1.2.3-test", `href="/-/static/site.css"`} {
		if !strings.Contains(body, want) {
			t.Errorf("page does not contain %q", want)
		}
	}
	// The server's content security policy forbids all of these.
	for _, banned := range []string{"<script", "<style", "style=", "onclick=", "onload=", "javascript:"} {
		if strings.Contains(strings.ToLower(body), banned) {
			t.Errorf("page contains %q, which the content security policy forbids", banned)
		}
	}
}

func TestPlaceholderPageEscapesVersion(t *testing.T) {
	// The version comes from the build (-ldflags), not from a user, but the
	// page must not depend on that.
	rec := serve(t, Handler(`<script>alert(1)</script>`), http.MethodGet, "/-/")

	body := rec.Body.String()
	if strings.Contains(body, "<script>") {
		t.Errorf("version was not escaped:\n%s", body)
	}
	if !strings.Contains(body, "&lt;script&gt;") {
		t.Errorf("escaped version not found:\n%s", body)
	}
}

func TestHeadHasHeadersAndNoBody(t *testing.T) {
	h := Handler("v")
	get := serve(t, h, http.MethodGet, "/-/")
	head := serve(t, h, http.MethodHead, "/-/")

	if head.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", head.Code)
	}
	if head.Body.Len() != 0 {
		t.Errorf("HEAD wrote a body of %d bytes", head.Body.Len())
	}
	for _, name := range []string{"Content-Type", "Content-Length"} {
		if g, h := get.Header().Get(name), head.Header().Get(name); g != h || g == "" {
			t.Errorf("%s: GET %q, HEAD %q", name, g, h)
		}
	}
}

func TestRouting(t *testing.T) {
	tests := []struct {
		name            string
		method          string
		target          string
		wantStatus      int
		wantContentType string // prefix; empty means not checked
	}{
		{"page", http.MethodGet, "/-/", http.StatusOK, "text/html"},
		{"page with query", http.MethodGet, "/-/?x=1", http.StatusOK, "text/html"},
		{"stylesheet", http.MethodGet, "/-/static/site.css", http.StatusOK, "text/css"},
		{"stylesheet HEAD", http.MethodHead, "/-/static/site.css", http.StatusOK, "text/css"},
		{"unknown page", http.MethodGet, "/-/links/x", http.StatusNotFound, ""},
		{"no trailing slash", http.MethodGet, "/-", http.StatusNotFound, ""},
		{"outside the prefix", http.MethodGet, "/", http.StatusNotFound, ""},
		{"static directory has no listing", http.MethodGet, "/-/static/", http.StatusNotFound, ""},
		{"static without slash", http.MethodGet, "/-/static", http.StatusNotFound, ""},
		{"missing static file", http.MethodGet, "/-/static/nope.css", http.StatusNotFound, ""},
		{"escape from static", http.MethodGet, "/-/static/../templates/placeholder.html", http.StatusNotFound, ""},
		{"encoded escape from static", http.MethodGet, "/-/static/%2e%2e/templates/placeholder.html", http.StatusNotFound, ""},
		{"templates are not served", http.MethodGet, "/-/templates/placeholder.html", http.StatusNotFound, ""},
		{"POST", http.MethodPost, "/-/", http.StatusMethodNotAllowed, ""},
		{"DELETE static", http.MethodDelete, "/-/static/site.css", http.StatusMethodNotAllowed, ""},
		{"OPTIONS", http.MethodOptions, "/-/", http.StatusMethodNotAllowed, ""},
	}
	h := Handler("v")
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := serve(t, h, tt.method, tt.target)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, tt.wantContentType) {
				t.Errorf("Content-Type = %q, want prefix %q", got, tt.wantContentType)
			}
			if tt.wantStatus == http.StatusMethodNotAllowed {
				if got := rec.Header().Get("Allow"); got != "GET, HEAD" {
					t.Errorf("Allow = %q, want \"GET, HEAD\"", got)
				}
			}
			for name := range rec.Header() {
				if strings.HasPrefix(name, "Access-Control-") {
					t.Errorf("response carries the CORS header %s", name)
				}
			}
		})
	}
}

func TestStylesheetIsNotEmpty(t *testing.T) {
	rec := serve(t, Handler("v"), http.MethodGet, "/-/static/site.css")
	if rec.Body.Len() == 0 {
		t.Error("stylesheet is empty")
	}
}
