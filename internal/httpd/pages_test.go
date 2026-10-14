package httpd

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/tfrydrychewicz/ogre/internal/app"
	"github.com/tfrydrychewicz/ogre/internal/linkmodel"
	"github.com/tfrydrychewicz/ogre/internal/web"
)

// assetURL is the content-hash URL of a static file, as a page links it.
func assetURL(t *testing.T, name string) string {
	t.Helper()
	r, err := web.New(web.Options{Version: testVersion})
	if err != nil {
		t.Fatal(err)
	}
	u, err := r.AssetURL(name)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// pagePaths are every page of the UI, as a user would open them.
var pagePaths = []string{
	"/-/",
	"/-/?q=roadmap",
	"/-/links/roadmap",
	"/-/links/roadmap/edit",
	"/-/links/roadmap/delete",
	"/-/new",
	"/-/new?pattern=jira/{key}&destination=https://jira.example.com/browse/{key}",
	"/-/setup",
	"/-/nothing-here",
}

var (
	scriptTag   = regexp.MustCompile(`(?i)<script\b[^>]*>`)
	handlerAttr = regexp.MustCompile(`(?i)<[^>]*\son[a-z]+\s*=`)
	styleAttr   = regexp.MustCompile(`(?i)<[^>]*\sstyle\s*=`)
	remoteLoad  = regexp.MustCompile(`(?i)<(?:script|img|link|iframe|source)\b[^>]*\s(?:src|href)\s*=\s*"(?:https?:)?//`)
)

// assertObeysPolicy fails if a page uses anything the content security
// policy blocks: inline script, a style element or attribute, an event
// handler attribute, a javascript: URL, or a resource from elsewhere. The
// browser would block these silently; here they fail the build.
func assertObeysPolicy(t *testing.T, body string) {
	t.Helper()
	for _, tag := range scriptTag.FindAllString(body, -1) {
		if !strings.Contains(tag, " src=") {
			t.Errorf("inline script: %s", tag)
		}
	}
	lower := strings.ToLower(body)
	for _, banned := range []string{"<style", "javascript:"} {
		if strings.Contains(lower, banned) {
			t.Errorf("page contains %q", banned)
		}
	}
	for name, re := range map[string]*regexp.Regexp{"event handler attribute": handlerAttr, "style attribute": styleAttr, "remote resource": remoteLoad} {
		if m := re.FindString(body); m != "" {
			t.Errorf("%s: %s", name, m)
		}
	}
}

// Every page is rendered through the layout, carries the page headers and
// obeys the policy it is sent with. HEAD has the same headers and no body.
func TestPagesRenderThroughTheLayout(t *testing.T) {
	h := newTestServer(&fakeService{}, nil).Handler()
	for _, path := range pagePaths {
		t.Run(path, func(t *testing.T) {
			rec := get(h, path)
			hdr := rec.Header()
			if got := hdr.Get("Content-Type"); got != "text/html; charset=utf-8" {
				t.Fatalf("Content-Type = %q; status %d; body %s", got, rec.Code, rec.Body.String())
			}
			if got := hdr.Get("Cache-Control"); got != "no-store" {
				t.Errorf("Cache-Control = %q, want no-store", got)
			}
			if got := hdr.Get("Content-Security-Policy"); got != web.ContentSecurityPolicy {
				t.Errorf("Content-Security-Policy = %q", got)
			}
			if got, want := hdr.Get("Content-Length"), strconv.Itoa(rec.Body.Len()); got != want {
				t.Errorf("Content-Length = %s, body has %s bytes", got, want)
			}
			body := rec.Body.String()
			for _, want := range []string{
				"<!doctype html>", `<html lang="en">`, `<a class="skip-link" href="#main">`,
				`<main id="main"`, `<nav class="site-nav" aria-label="Main">`, `href="/-/new"`,
				"Ogre " + testVersion, `href="` + assetURL(t, "css/site.css") + `"`,
			} {
				if !strings.Contains(body, want) {
					t.Errorf("page lacks %q", want)
				}
			}
			assertObeysPolicy(t, body)

			head := request{method: http.MethodHead, target: path}.send(h)
			if head.Code != rec.Code || head.Body.Len() != 0 {
				t.Errorf("HEAD = %d with %d bytes of body, want %d and none", head.Code, head.Body.Len(), rec.Code)
			}
			for _, name := range []string{"Content-Type", "Content-Length", "Cache-Control", "Content-Security-Policy"} {
				if g, h := hdr.Get(name), head.Header().Get(name); g != h {
					t.Errorf("%s: GET %q, HEAD %q", name, g, h)
				}
			}
		})
	}
}

// A path under "/-/" that is no page gets the 404 page, which repeats
// nothing of the address: not the path, not the query.
func TestUnknownPageIs404WithoutEcho(t *testing.T) {
	const secret = "s3cr3t-value"
	h := newTestServer(&fakeService{}, nil).Handler()
	for _, target := range []string{
		"/-/" + secret,
		"/-/links/x/" + secret,
		"/-/%3Cscript%3E" + secret,
		"/-/x?q=" + secret,
		"/-/../" + secret,
	} {
		rec := get(h, target)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: status = %d, want 404", target, rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
			t.Errorf("%s: Content-Type = %q", target, ct)
		}
		body := rec.Body.String()
		if strings.Contains(body, secret) || strings.Contains(body, "<script>") {
			t.Errorf("%s: the 404 page repeats the address:\n%s", target, body)
		}
		if !strings.Contains(body, "Page not found") {
			t.Errorf("%s: not the 404 page:\n%s", target, body)
		}
	}
}

// A page loaded by another site as a frame, an image or a fetch is refused,
// like a go link: whether /-/links/<name> is a page or a 404 would tell that
// site which links exist. Opening the page in a tab from another site, the
// health check and the static files stay as they were.
func TestCrossSitePageLoadsAreRefused(t *testing.T) {
	subresource := map[string]string{"Sec-Fetch-Site": "cross-site", "Sec-Fetch-Mode": "no-cors", "Sec-Fetch-Dest": "iframe"}
	navigation := map[string]string{"Sec-Fetch-Site": "cross-site", "Sec-Fetch-Mode": "navigate", "Sec-Fetch-Dest": "document"}
	h := newTestServer(&fakeService{}, nil).Handler()

	for _, target := range []string{"/-/", "/-/links/roadmap", "/-/new", "/-/setup", "/-/nothing"} {
		rec := request{target: target, headers: subresource}.send(h)
		if rec.Code != http.StatusForbidden {
			t.Errorf("cross-site load of %s = %d, want 403", target, rec.Code)
		}
		if strings.Contains(rec.Body.String(), "<") {
			t.Errorf("cross-site load of %s answered with markup: %s", target, rec.Body.String())
		}
		opened := request{target: target, headers: navigation}.send(h)
		if opened.Code == http.StatusForbidden {
			t.Errorf("opening %s from another site was refused", target)
		}
	}
	for _, target := range []string{"/-/healthz", assetURL(t, "css/site.css")} {
		rec := request{target: target, headers: subresource}.send(h)
		if rec.Code != http.StatusOK {
			t.Errorf("cross-site load of %s = %d, want 200", target, rec.Code)
		}
	}
}

// A page whose template fails is a logged 500 with no part of the page.
func TestBrokenPageIsA500(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"templates/layout.html": `{{define "layout"}}<p>{{block "main" .}}{{end}}</p>{{end}}`,
		"templates/error.html":  `{{define "main"}}partial {{.NoSuchField}}{{end}}`,
		"static/css/site.css":   "body{}",
	}
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	renderer, err := web.New(web.Options{Version: testVersion, Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	h := newTestServer(&fakeService{}, func(o *Options) {
		o.Web = renderer
		o.Logger = slog.New(slog.NewTextHandler(&logs, nil))
	}).Handler()

	rec := get(h, "/-/nothing")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if body := rec.Body.String(); strings.Contains(body, "partial") || strings.Contains(body, "<p>") {
		t.Errorf("half a page was sent: %q", body)
	}
	if !strings.Contains(logs.String(), "rendering a page failed") {
		t.Errorf("the failure was not logged:\n%s", logs.String())
	}
}

// NFR-11 across every page at once: hostile text in everything a page can
// show (a link's every field, its owner, search results, the home lists) and
// in everything a request can carry (the link name in the path, and q,
// pattern and destination in the query) comes out as text. The pages have
// tests of their own for their parts; this one makes sure no page, the
// setup and error pages included, was left out.
func TestEveryPageEscapesHostileInput(t *testing.T) {
	l := hostileLink()
	all := strings.Join(hostileText, " ")
	svc := &fakeService{
		owner: all,
		fakeQueries: fakeQueries{
			byNameFn: func(string) (linkmodel.Link, bool, error) { return l, true, nil },
			searchFn: func(string, int) ([]app.SearchResult, error) {
				return []app.SearchResult{{Link: l, Rank: app.RankMisspelled, Edits: 1}, {Link: l, Rank: all}}, nil
			},
			yourLinks:   []linkmodel.Link{l},
			recentLinks: []linkmodel.Link{l},
		},
	}
	h := newTestServer(svc, nil).Handler()

	query := url.Values{"q": {all}, "pattern": {"reports/" + all, all}, "destination": {"javascript:alert(1)//" + all}}.Encode()
	name := url.PathEscape(`rep<x-hostile>"orts` + all)
	targets := []string{"/-/links/" + name, "/-/links/" + name + "/edit", "/-/links/" + name + "/delete"}
	for _, p := range pagePaths {
		path, _, _ := strings.Cut(p, "?")
		targets = append(targets, path+"?"+query)
	}
	for _, target := range targets {
		t.Run(target, func(t *testing.T) {
			rec := get(h, target)
			if rec.Code != http.StatusOK && rec.Code != http.StatusNotFound {
				t.Fatalf("status = %d; body:\n%s", rec.Code, rec.Body.String())
			}
			if got := rec.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
				t.Errorf("Content-Type = %q", got)
			}
			assertSafePage(t, rec.Body.String())
		})
	}
}
