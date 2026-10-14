package httpd

import (
	"html"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/tfrydrychewicz/ogre/internal/app"
	"github.com/tfrydrychewicz/ogre/internal/linkmodel"
	"github.com/tfrydrychewicz/ogre/internal/web"
)

var (
	missMainRe  = regexp.MustCompile(`(?s)<main\b[^>]*>(.*)</main>`)
	missTagRe   = regexp.MustCompile(`(?s)<[^>]*>`)
	missInline  = regexp.MustCompile(`(?i)</?(?:a|span|bdi|strong|code|kbd)\b[^>]*>`)
	missSpaceRe = regexp.MustCompile(`\s+`)
	missHrefRe  = regexp.MustCompile(`\shref="([^"]*)"`)
	missFirstRe = regexp.MustCompile(`<a [^>]*autofocus[^>]*>`)
)

// missText is the visible text of a page's <main>, as a user reads it:
// tags dropped, entities decoded, white space collapsed.
func missText(body string) string {
	m := missMainRe.FindStringSubmatch(body)
	if m == nil {
		return ""
	}
	// Inline tags join their text to the words around them, as a browser
	// shows it; every other tag separates.
	text := missInline.ReplaceAllString(m[1], "")
	text = html.UnescapeString(missTagRe.ReplaceAllString(text, " "))
	return strings.TrimSpace(missSpaceRe.ReplaceAllString(text, " "))
}

// missHrefs are the decoded href values of every link on a page.
func missHrefs(body string) []string {
	var hrefs []string
	for _, m := range missHrefRe.FindAllStringSubmatch(body, -1) {
		hrefs = append(hrefs, html.UnescapeString(m[1]))
	}
	return hrefs
}

// assertSafeHrefs fails unless every link on the page stays on this
// Directory: a path from its root, or a fragment. No scheme, so no
// javascript: or data:, and no "//host", which would be another site.
func assertSafeHrefs(t *testing.T, body string) {
	t.Helper()
	for _, href := range missHrefs(body) {
		switch {
		case strings.HasPrefix(href, "#"):
		case strings.HasPrefix(href, "/") && !strings.HasPrefix(href, "//") && !strings.HasPrefix(href, `/\`):
		default:
			t.Errorf("unsafe href %q", href)
		}
	}
}

// The miss pages (L3.8): a 404 page at the address that missed, built from
// the decision, complete without JavaScript, with the headers of a page plus
// no-referrer and noindex. HEAD answers the same headers and no body.
func TestMissPages(t *testing.T) {
	tests := []struct {
		name       string
		decision   app.Decision
		target     string
		wantStatus int
		// wantText must appear in the visible text of <main>, in this order.
		wantText []string
		// wantHTML must appear in the page's markup.
		wantHTML []string
		// notText must not appear in the visible text.
		notText []string
		// wantFirst is the href of the link that has focus on load; empty
		// when nothing should.
		wantFirst string
	}{
		{
			name:       "unknown name with suggestions (PRD scenario 4)",
			decision:   missUnknownDecision(),
			target:     "/reprots/10/13",
			wantStatus: http.StatusNotFound,
			wantText: []string{
				"404 · Not found", "No link go/reprots/10/13",
				"Did you mean", "go/reports/10/13", "Daily reports", "Opens https://reports.example.com/daily?month=10&day=13",
				"go/report", "Press Enter to open the first suggestion.",
				"Not what you meant?", "Create go/reprots/10/13", "Search go links",
			},
			wantHTML: []string{
				`href="/reports/10/13" autofocus data-miss-first`,
				`href="/-/links/report"`,
				`href="/-/new?pattern=reprots%2F10%2F13"`,
				`id="miss-search-q" type="search" name="q" value="reprots"`,
				`<script type="module" src="` + assetURL(t, "js/miss.js") + `"></script>`,
				`<meta name="robots" content="noindex">`,
			},
			wantFirst: "/reports/10/13",
		},
		{
			// PRD 5.6: a suggestion the rest of the address does not fit
			// opens the link's usage help, its page, and lists its routes
			// right here.
			name:       "a suggestion the typed values do not fit",
			decision:   missUnknownWithUsage(),
			target:     "/ghh/a/b",
			wantStatus: http.StatusNotFound,
			wantText: []string{
				"No link go/ghh/a/b", "go/gh", "GitHub", "doesn't fit this link",
				"go/gh", "go/gh/{repo}", "go/gh/{repo}/pr/{num:int}",
				"go/gha/a/b", "Opens https://example.com/actions/a/b",
			},
			wantHTML: []string{
				`href="/-/links/gh" autofocus`, `href="/gha/a/b"`,
				`<li class="pattern"><bdi>go/gh/<span class="pattern-var">{repo}</span>/pr/<span class="pattern-var">{num:int}</span></bdi></li>`,
			},
			wantFirst: "/-/links/gh",
		},
		{
			name:       "unknown name, nothing close",
			decision:   app.Decision{Kind: app.DecisionMissUnknown, LinkName: "zzz", RequestedName: "zzz"},
			target:     "/zzz/",
			wantStatus: http.StatusNotFound,
			wantText:   []string{"No link go/zzz", "No link has this name or one close to it.", "What next", "Create go/zzz"},
			wantHTML:   []string{`class="button button-primary miss-create" href="/-/new?pattern=zzz"`, `value="zzz"`},
			notText:    []string{"Did you mean", "Press Enter"},
		},
		{
			name:       "an address that cannot be a link",
			decision:   app.Decision{Kind: app.DecisionMissUnknown},
			target:     "/%3Cscript%3E/x",
			wantStatus: http.StatusNotFound,
			wantText:   []string{"That is not a go link", "Create a link", "Search go links"},
			wantHTML:   []string{`href="/-/new"`},
			notText:    []string{"script", "go/", "Did you mean"},
		},
		{
			name:       "known name, a reason per route (PRD scenario 5)",
			decision:   missKnownDecision(),
			target:     "/reports/13/01",
			wantStatus: http.StatusNotFound,
			wantText: []string{
				"404 · No route fits", "go/reports can't open this address", "You typed go/reports/13/01.",
				"Daily reports", "How go/reports is used",
				"go/reports/{month:regex(^(0?[1-9]|1[0-2])$)}/{day:int}",
				`Why it doesn't fit: month doesn't accept "13". It accepts text matching ^(0?[1-9]|1[0-2])$.`,
				"go/reports", "Why it doesn't fit: This route takes nothing after reports, but the address has 2 more parts.",
				// MISS-3 on a known name too: what was typed can become a
				// route of the link (the create form offers that, DIR-4), and
				// the search box holds the name.
				"View go/reports", "Create go/reports/13/01", "Search go links",
			},
			wantHTML: []string{
				`<bdi>go/reports/<span class="pattern-var">{month:regex(^(0?[1-9]|1[0-2])$)}</span>/<span class="pattern-var">{day:int}</span>`,
				`class="button button-primary" href="/-/links/reports"`,
				`class="button miss-create" href="/-/new?pattern=reports%2F13%2F01"`,
				`id="miss-search-q" type="search" name="q" value="reports"`,
			},
			notText: []string{"Did you mean"},
		},
		{
			// The corrected address keeps every value exactly as it was
			// encoded: an encoded "/", "?", "#" or "%" in a value must not
			// become a separator, a query or a fragment of the suggestion.
			name: "a suggestion whose values hold encoded separators",
			decision: app.Decision{
				Kind: app.DecisionMissUnknown, LinkName: "reprots", RequestedName: "reprots",
				Suggestions: []app.Suggestion{{Name: "reports", Path: "reports/a%2Fb%3Fc%23d%25", Edits: 1, Resolves: true,
					Destination: "https://reports.example.com/?q=a%2Fb%3Fc%23d%25"}},
			},
			target:     "/reprots/a%2Fb%3Fc%23d%25",
			wantStatus: http.StatusNotFound,
			wantText:   []string{"No link go/reprots/a%2Fb%3Fc%23d%25", "go/reports/a%2Fb%3Fc%23d%25"},
			wantHTML:   []string{`href="/reports/a%2Fb%3Fc%23d%25" autofocus data-miss-first`},
			wantFirst:  "/reports/a%2Fb%3Fc%23d%25",
		},
		{
			name:       "known name, nothing else known",
			decision:   app.Decision{Kind: app.DecisionMissKnown, LinkName: "reports"},
			target:     "/reports/x",
			wantStatus: http.StatusNotFound,
			wantText:   []string{"go/reports can't open this address", "You typed go/reports/x.", "View go/reports"},
			notText:    []string{"How go/reports is used"},
		},
		{
			name:       "bad request",
			decision:   app.Decision{Kind: app.DecisionBadRequest},
			target:     "/reports/%C3%28",
			wantStatus: http.StatusBadRequest,
			wantText:   []string{"Error 400", "Not a go link", "not valid text"},
			notText:    []string{"%C3", "reports"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &fakeService{resolveFn: func(string) app.Decision { return tt.decision }}
			h := newTestServer(svc, nil).Handler()

			getRec := get(h, tt.target)
			headRec := request{method: http.MethodHead, target: tt.target}.send(h)
			for method, rec := range map[string]*httptest.ResponseRecorder{"GET": getRec, "HEAD": headRec} {
				hdr := rec.Header()
				if rec.Code != tt.wantStatus {
					t.Errorf("%s status = %d, want %d", method, rec.Code, tt.wantStatus)
				}
				for name, want := range map[string]string{
					"Content-Type":            "text/html; charset=utf-8",
					"Cache-Control":           "no-store",
					"Content-Security-Policy": web.ContentSecurityPolicy,
					"X-Content-Type-Options":  "nosniff",
					"X-Frame-Options":         "DENY",
					"Referrer-Policy":         "no-referrer",
					"X-Robots-Tag":            "noindex",
					"Location":                "",
				} {
					if got := hdr.Get(name); got != want {
						t.Errorf("%s %s = %q, want %q", method, name, got, want)
					}
				}
				assertNoCORS(t, hdr)
			}
			if headRec.Body.Len() != 0 {
				t.Errorf("HEAD wrote a body of %d bytes", headRec.Body.Len())
			}
			if !reflect.DeepEqual(getRec.Header(), headRec.Header()) {
				t.Errorf("HEAD headers differ from GET:\n GET  %v\n HEAD %v", getRec.Header(), headRec.Header())
			}

			body := getRec.Body.String()
			assertObeysPolicy(t, body)
			assertSafeHrefs(t, body)
			text := missText(body)
			at := 0
			for _, want := range tt.wantText {
				i := strings.Index(text[at:], want)
				if i < 0 {
					t.Errorf("the page lacks %q after position %d:\n%s", want, at, text)
					continue
				}
				at += i + len(want)
			}
			for _, want := range tt.wantHTML {
				if !strings.Contains(body, want) {
					t.Errorf("the markup lacks %s", want)
				}
			}
			for _, not := range tt.notText {
				if strings.Contains(text, not) {
					t.Errorf("the page says %q:\n%s", not, text)
				}
			}
			first := missFirstRe.FindAllString(body, -1)
			switch {
			case tt.wantFirst == "" && len(first) > 0:
				t.Errorf("something has autofocus: %v", first)
			case tt.wantFirst != "" && (len(first) != 1 || !strings.Contains(first[0], `href="`+tt.wantFirst+`"`)):
				t.Errorf("autofocus on %v, want exactly the link to %s", first, tt.wantFirst)
			}
			// One search box, in the page: the header's is left out.
			if n := strings.Count(body, `role="search"`); tt.wantStatus == http.StatusNotFound && n != 1 {
				t.Errorf("%d search forms, want 1", n)
			}
		})
	}
}

// The typed path is shown as the address it is: segments decoded where
// that only helps reading, and percent-encoded where decoding would show a
// different address or hide something.
func TestMissShowPath(t *testing.T) {
	tests := map[string]string{
		"/reprots/10/13":                  "reprots/10/13",
		"reprots/10/13":                   "reprots/10/13",
		"/a//b/":                          "a/b",
		"/wiki/Z%C3%BCrich":               "wiki/Zürich",
		"/wiki/%E6%9D%B1%E4%BA%AC":        "wiki/東京",
		"/jira/ABC-1_2.3~":                "jira/ABC-1_2.3~",
		"/q/a%20b":                        "q/a%20b",
		"/q/a%2Fb":                        "q/a%2Fb",
		"/q/100%25":                       "q/100%25",
		"/q/%3Cscript%3E":                 "q/%3Cscript%3E",
		"/q/%22x%22":                      "q/%22x%22",
		"/q/a%0Ab":                        "q/a%0Ab",
		"/q/%E2%80%AEevil":                "q/%E2%80%AEevil",
		"/q/a%E2%80%8Bb":                  "q/a%E2%80%8Bb", // zero-width space
		"/q/%3Fx%23y":                     "q/%3Fx%23y",
		"/q/javascript:alert(1)":          "q/javascript:alert(1)",
		"/q/%ZZ":                          "q/%ZZ", // never reaches the page; shown as it came
		"/" + strings.Repeat("ab/", 3):    "ab/ab/ab",
		"/x?query=never-part-of-the-path": "x",
	}
	for in, want := range tests {
		if got := missShowPath(in); got != want {
			t.Errorf("missShowPath(%q) = %q, want %q", in, got, want)
		}
	}
}

// patternParts cuts a pattern for every page that shows one (home, the link
// page, the delete page, the miss page): variables apart, everything else
// literal, the parts always adding up to the pattern.
func TestPatternParts(t *testing.T) {
	tests := map[string][]patternPart{
		"roadmap":   {{Text: "roadmap"}},
		"gh/{repo}": {{Text: "gh/"}, {Text: "{repo}", Var: true}},
		"gh/{repo}/pr/{n:int}": {
			{Text: "gh/"}, {Text: "{repo}", Var: true}, {Text: "/pr/"}, {Text: "{n:int}", Var: true},
		},
		"r/{x:regex(^a/{2}$)}/y": {{Text: "r/"}, {Text: "{x:regex(^a/{2}$)}", Var: true}, {Text: "/y"}},
		"broken/{x":              {{Text: "broken/{x"}},
		"":                       nil,
	}
	for in, want := range tests {
		if got := patternParts(in); !reflect.DeepEqual(got, want) {
			t.Errorf("patternParts(%q) = %+v, want %+v", in, got, want)
		}
	}
}

// Hostile text from every source the page shows — the typed path, a
// suggestion's path, description and destination, a known link's
// description, patterns, reasons, and variable notes — is escaped, cannot
// add a tag, an attribute or a link, and cannot reorder the page around it.
func TestMissPageEscapesHostileText(t *testing.T) {
	const (
		hostile = "<script>alert(1)</script>\"'><img src=x onerror=alert(1)> javascript:alert(1) \u202egnp.exe"
		evilURL = "https://evil.example/?q=\"><script>alert(1)</script>"
	)
	link := &linkmodel.Link{ID: 7, Name: "reports", Description: hostile, Routes: []linkmodel.Route{{
		ID: 1, Pattern: "reports/{month:int}", Destination: evilURL,
		Variables: []linkmodel.VariableMeta{{Name: "month", Description: hostile, Example: hostile}},
	}}}
	unknown := app.Decision{
		Kind: app.DecisionMissUnknown, LinkName: "reprots", RequestedName: "reprots",
		Suggestions: []app.Suggestion{
			{Name: "reports", Path: "reports/%3Cscript%3E%22%27/%E2%80%AE", Edits: 1, Resolves: true,
				Description: hostile, Destination: evilURL, Link: link},
			{Name: "report", Path: "report", Edits: 2, Description: hostile,
				Link: &linkmodel.Link{Name: "report", Routes: []linkmodel.Route{{Pattern: "report/<b>{x}" + hostile}}}},
			// A path that would leave the Directory if it were used as is.
			{Name: "evil", Path: "/evil.example/x", Edits: 2, Resolves: true, Destination: evilURL},
		},
	}
	known := app.Decision{
		Kind: app.DecisionMissKnown, LinkName: "reports", Link: link,
		Reasons: []linkmodel.Reason{{RouteID: 1, Pattern: "reports/{month:int}", Code: "value_rejected",
			Variable: "month", Value: hostile, Message: "month doesn't accept " + hostile}},
	}
	const typed = "/reprots/%3Cscript%3Ealert(1)%3C%2Fscript%3E/%22onmouseover=%22x/javascript:alert(1)/%E2%80%AE%E2%80%8B"

	for name, d := range map[string]app.Decision{"unknown": unknown, "known": known} {
		t.Run(name, func(t *testing.T) {
			svc := &fakeService{resolveFn: func(string) app.Decision { return d }}
			body := get(newTestServer(svc, nil).Handler(), typed).Body.String()

			assertSafeHrefs(t, body)
			for _, tag := range scriptTag.FindAllString(body, -1) {
				if !strings.Contains(tag, ` src="/-/static/`) {
					t.Errorf("a script that is not the page's own: %s", tag)
				}
			}
			for _, raw := range []string{"<img", "<b>", `"><script`, "'><img"} {
				if strings.Contains(body, raw) {
					t.Errorf("hostile markup reached the page unescaped: %q", raw)
				}
			}
			for name, re := range map[string]*regexp.Regexp{"event handler attribute": handlerAttr, "style attribute": styleAttr} {
				if m := re.FindString(body); m != "" {
					t.Errorf("%s: %s", name, m)
				}
			}
			if strings.Contains(body, "evil.example/x") && strings.Contains(body, `href="/evil.example`) {
				t.Error("a suggestion path starting with / became a link")
			}
			// Every direction-changing character comes from owner text and
			// sits inside a <bdi>; the typed path's own is shown encoded.
			if n, isolated := strings.Count(body, "\u202e"), len(regexp.MustCompile(`<bdi>[^<]*\x{202e}`).FindAllString(body, -1)); n != isolated {
				t.Errorf("%d U+202E characters, %d of them isolated in <bdi>", n, isolated)
			}
			if !strings.Contains(missText(body), "%E2%80%AE%E2%80%8B") {
				t.Errorf("the typed path's invisible characters are not shown encoded:\n%s", missText(body))
			}
			if !strings.Contains(missText(body), hostile[:30]) {
				t.Errorf("the hostile text is not shown as text, so the test proves nothing:\n%s", missText(body))
			}
		})
	}
}

// A typed path longer than any pattern is cut on the page, and the create
// form is offered the name alone, which could be saved.
func TestMissPageLongPath(t *testing.T) {
	long := "/reprots/" + strings.Repeat("x", 5000)
	svc := &fakeService{resolveFn: func(string) app.Decision {
		return app.Decision{Kind: app.DecisionMissUnknown, LinkName: "reprots", RequestedName: "reprots"}
	}}
	body := get(newTestServer(svc, nil).Handler(), long).Body.String()
	if strings.Contains(body, strings.Repeat("x", missTypedMaxRunes)) {
		t.Error("the page shows the whole long path")
	}
	if !strings.Contains(missText(body), "No link go/reprots/"+strings.Repeat("x", missTypedMaxRunes-len("reprots/"))+"…") {
		t.Errorf("the path is not cut at %d characters:\n%.400s", missTypedMaxRunes, missText(body))
	}
	if !strings.Contains(body, `href="/-/new?pattern=reprots"`) {
		t.Error("the create form is not offered the name alone")
	}
}

// The miss page never logs the typed path, a suggestion or a reason, even
// when rendering fails (NFR-17): the failure is logged by page name.
func TestMissPageRenderFailureLogsNothingOfTheRequest(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{
		"templates/layout.html": `{{define "layout"}}{{block "main" .}}{{end}}{{end}}`,
		"templates/miss.html":   `{{define "main"}}{{.Typed}} {{.NoSuchField}}{{end}}`,
		"static/css/site.css":   "body{}",
	} {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	r, err := web.New(web.Options{Version: testVersion, Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	logger, logs := captureLog()
	svc := &fakeService{resolveFn: func(string) app.Decision { return missUnknownDecision() }}
	rec := get(newTestServer(svc, func(o *Options) { o.Logger = logger; o.Web = r }).Handler(), "/reprots/S3cret-Value")
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
	for _, leaked := range []string{"S3cret", "reports/10/13", "reprots/"} {
		if strings.Contains(logs.String(), leaked) {
			t.Errorf("the log contains %q:\n%s", leaked, logs.String())
		}
	}
	if !strings.Contains(logs.String(), `"page":"miss"`) {
		t.Errorf("the failure is not logged by page:\n%s", logs.String())
	}
}
