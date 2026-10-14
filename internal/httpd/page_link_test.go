package httpd

import (
	"encoding/json"
	"errors"
	"html"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/tfrydrychewicz/ogre/internal/app"
	"github.com/tfrydrychewicz/ogre/internal/linkmodel"
)

// pageReportsLink is a link with variables in two routes, as scenario 6 of the
// PRD finds it.
func pageReportsLink() linkmodel.Link {
	at := time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)
	return linkmodel.Link{
		ID:          4,
		Name:        "reports",
		Description: "Daily reports",
		OwnerID:     1,
		Version:     3,
		Routes: []linkmodel.Route{
			{
				ID: 9, Pattern: "reports/{month:int}/{day:int}",
				Destination: "https://reports.example.com/daily?month={month}&day={day}",
				Variables: []linkmodel.VariableMeta{
					{Name: "month", Description: "Month, 1 to 12", Example: "10"},
					{Name: "day", Description: "Day of the month", Example: "13"},
				},
			},
			{ID: 10, Pattern: "reports", Destination: "https://reports.example.com/"},
		},
		CreatedAt: at,
		UpdatedAt: at.Add(time.Hour),
	}
}

// byName answers LinkByName with the given links, ignoring case, and with
// editable for all of them.
func byName(editable bool, links ...linkmodel.Link) func(string) (linkmodel.Link, bool, error) {
	return func(name string) (linkmodel.Link, bool, error) {
		for _, l := range links {
			if strings.EqualFold(l.Name, name) {
				return l, editable, nil
			}
		}
		return linkmodel.Link{}, false, app.ErrNotFound
	}
}

func TestLinkPage(t *testing.T) {
	svc := &fakeService{fakeQueries: fakeQueries{byNameFn: byName(true, pageReportsLink())}}
	h := newTestServer(svc, nil).Handler()

	rec := get(h, "/-/links/Reports")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200\n%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	assertObeysPolicy(t, body)
	for _, want := range []string{
		"<title>go/reports · Ogre</title>",
		`<h1 class="golink link-name">go/reports</h1>`,
		"<bdi>Daily reports</bdi>",
		// The pattern, its variables highlighted by the link model's tokens.
		`go/reports/<span class="pattern-var">{month:int}</span>/<span class="pattern-var">{day:int}</span>`,
		"https://reports.example.com/daily?month={month}&amp;day={day}",
		// The variables table: name, type, accepts, description, example.
		`<span class="pattern-var mono">month</span>`, `<span class="badge">Whole number</span>`,
		"<bdi>Month, 1 to 12</bdi>", "<bdi>10</bdi>",
		// Copy buttons for the short form and the full URL, hidden until
		// the script wires them.
		`data-copy="go/reports" hidden>Copy go link</button>`,
		`data-copy="http://localhost:4655/reports" hidden>Copy URL</button>`,
		// The owner's name, and Edit and Delete for an editable link.
		"<bdi>Local user</bdi>",
		`href="/-/links/reports/edit"`, `href="/-/links/reports/delete"`,
		// The launcher's mount point, with a fallback without JavaScript.
		`id="launch"`, `id="launcher"`, "<noscript>",
		`src="` + assetURL(t, "js/link.js") + `"`,
		"Ogre " + testVersion,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("link page lacks %q", want)
		}
	}
}

// The launcher's routes are the link JSON's routes plus the tokens of each
// pattern, in the validate endpoint's shape: offsets in UTF-16 code units.
func TestLinkPageLauncherRoutes(t *testing.T) {
	link := pageReportsLink()
	// A regex with a non-ASCII character before the second variable makes
	// byte and UTF-16 offsets differ.
	link.Routes[0].Pattern = "reports/{month:regex(^é$)}/{day:int}"
	svc := &fakeService{fakeQueries: fakeQueries{byNameFn: byName(true, link)}}
	rec := get(newTestServer(svc, nil).Handler(), "/-/links/reports")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	m := regexp.MustCompile(`data-routes="([^"]*)"`).FindStringSubmatch(rec.Body.String())
	if m == nil {
		t.Fatal("no data-routes attribute")
	}
	var routes []struct {
		ID        int64  `json:"id"`
		Pattern   string `json:"pattern"`
		Variables []struct {
			Name, Type, Accepts, Description, Example string
		} `json:"variables"`
		Tokens []tokenJSON `json:"tokens"`
	}
	if err := json.Unmarshal([]byte(html.UnescapeString(m[1])), &routes); err != nil {
		t.Fatalf("data-routes is not JSON: %v\n%s", err, m[1])
	}
	if len(routes) != 2 || routes[0].ID != 9 || routes[1].ID != 10 {
		t.Fatalf("routes = %+v", routes)
	}
	r := routes[0]
	if len(r.Variables) != 2 || r.Variables[0].Name != "month" || r.Variables[0].Description != "Month, 1 to 12" || r.Variables[1].Type != "int" {
		t.Errorf("variables = %+v", r.Variables)
	}
	want := []tokenJSON{
		{Kind: "literal", Start: 0, End: 7},
		{Kind: "variable", Start: 8, End: 26, Name: "month", Type: "regex"},
		{Kind: "variable", Start: 27, End: 36, Name: "day", Type: "int"},
	}
	if len(r.Tokens) != len(want) {
		t.Fatalf("tokens = %+v, want %+v", r.Tokens, want)
	}
	for i := range want {
		if r.Tokens[i] != want[i] {
			t.Errorf("token %d = %+v, want %+v", i, r.Tokens[i], want[i])
		}
	}
	// The browser reads the pattern by these offsets; they must cut it in
	// the same places in UTF-16.
	units := []rune(r.Pattern) // the pattern has no character outside the BMP
	if got := string(units[r.Tokens[2].Start:r.Tokens[2].End]); got != "{day:int}" {
		t.Errorf("the last token covers %q in UTF-16", got)
	}
}

// Edit and Delete are offered only to whoever may edit the link: the answer
// of the Service, which asks app.CanEdit.
func TestLinkPageNotEditable(t *testing.T) {
	svc := &fakeService{fakeQueries: fakeQueries{byNameFn: byName(false, pageReportsLink())}}
	body := get(newTestServer(svc, nil).Handler(), "/-/links/reports").Body.String()
	for _, banned := range []string{"/-/links/reports/edit", "/-/links/reports/delete"} {
		if strings.Contains(body, banned) {
			t.Errorf("a link the user may not edit offers %s", banned)
		}
	}
}

// An unknown name is a 404 page that offers to create the link, repeating
// the name only when it could be one.
func TestLinkPageNotFound(t *testing.T) {
	h := newTestServer(&fakeService{}, nil).Handler()
	tests := []struct {
		target string
		want   []string
		banned []string
	}{
		{
			target: "/-/links/Standup",
			want:   []string{"There is no <span class=\"golink\">go/standup</span>", `href="/-/new?pattern=standup"`, `href="/-/?q=standup"`},
		},
		{
			target: "/-/links/%3Cscript%3Ealert(1)%3C%2Fscript%3E",
			want:   []string{"Link not found"},
			banned: []string{"script&gt;", "alert", "/-/new?pattern="},
		},
		{
			target: "/-/links/a%2Fb",
			want:   []string{"Link not found"},
			banned: []string{"a/b", "a%2Fb"},
		},
	}
	for _, tt := range tests {
		rec := get(h, tt.target)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: status = %d, want 404", tt.target, rec.Code)
		}
		body := rec.Body.String()
		assertObeysPolicy(t, body)
		for _, want := range tt.want {
			if !strings.Contains(body, want) {
				t.Errorf("%s: page lacks %q", tt.target, want)
			}
		}
		for _, banned := range tt.banned {
			if strings.Contains(body, banned) {
				t.Errorf("%s: page repeats %q", tt.target, banned)
			}
		}
		if strings.Contains(body, "js/link.js") {
			t.Errorf("%s: the 404 page loads the launcher", tt.target)
		}
	}
}

// A failure to read the link is a logged 500 page that says nothing of it.
func TestLinkPageServiceError(t *testing.T) {
	svc := &fakeService{fakeQueries: fakeQueries{byNameFn: func(string) (linkmodel.Link, bool, error) {
		return linkmodel.Link{}, false, errors.New("disk on fire")
	}}}
	h := newTestServer(svc, nil).Handler()
	for _, target := range []string{"/-/links/reports", "/-/links/reports/delete"} {
		rec := get(h, target)
		if rec.Code != http.StatusInternalServerError {
			t.Errorf("%s: status = %d, want 500", target, rec.Code)
		}
		if strings.Contains(rec.Body.String(), "disk on fire") {
			t.Errorf("%s: the error leaked into the page", target)
		}
	}
}

func TestDeletePage(t *testing.T) {
	svc := &fakeService{fakeQueries: fakeQueries{byNameFn: byName(true, pageReportsLink())}}
	h := newTestServer(svc, nil).Handler()

	rec := get(h, "/-/links/reports/delete")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d\n%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	assertObeysPolicy(t, body)
	for _, want := range []string{
		"<title>Delete go/reports · Ogre</title>",
		`Delete <span class="golink">go/reports</span>?`,
		"All 2 of its routes go with it",
		`go/reports/<span class="pattern-var">{month:int}</span>/<span class="pattern-var">{day:int}</span>`,
		// The button carries what the DELETE needs and appears only with
		// JavaScript, which the page says it needs.
		`id="delete-confirm" type="button" data-link-id="4" data-version="3" data-name="reports" hidden>Delete go/reports</button>`,
		"<noscript>", `src="` + assetURL(t, "js/delete.js") + `"`,
		`href="/-/links/reports">Cancel</a>`,
		// The link's version must not hide the Directory's in the footer.
		"Ogre " + testVersion,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("delete page lacks %q", want)
		}
	}
	if svc.calls() != 0 {
		t.Errorf("rendering the confirmation changed something: %d calls", svc.calls())
	}

	// Someone who may not delete the link is told so, and given no button.
	svc = &fakeService{owner: "Ana", fakeQueries: fakeQueries{byNameFn: byName(false, pageReportsLink())}}
	rec = get(newTestServer(svc, nil).Handler(), "/-/links/reports/delete")
	if rec.Code != http.StatusForbidden {
		t.Errorf("a link the user may not delete: status = %d, want 403, as for its edit form", rec.Code)
	}
	body = rec.Body.String()
	if !strings.Contains(body, `role="note"`) || strings.Contains(body, `role="alert"`) {
		t.Error("the refusal, there when the page loads, is not a note")
	}
	if strings.Contains(body, "delete-confirm") || strings.Contains(body, "js/delete.js") {
		t.Error("a link the user may not delete has a delete button")
	}
	if !strings.Contains(body, "Only the owner of go/reports (<bdi>Ana</bdi>) or an administrator can delete it.") {
		t.Errorf("the refusal does not name the owner:\n%s", body)
	}

	if rec := get(h, "/-/links/nothing/delete"); rec.Code != http.StatusNotFound {
		t.Errorf("deleting an unknown link: status = %d, want 404", rec.Code)
	}
}

// hostileText holds what an attacker would put in any text field: markup, a
// quote that ends an attribute, a script URL, and a right-to-left override
// that would reorder the text around it.
var hostileText = []string{
	`<x-hostile>alert(1)</x-hostile>`,
	`" data-injected="1`,
	`' data-injected='1`,
	`javascript:alert(1)`,
	"\u202etxt.exe",
}

// hostileLink is a link with hostile text in every field a page shows. The
// name is hostile too, although the link model never stores such a name,
// so the pages are safe whatever the store holds.
func hostileLink() linkmodel.Link {
	all := strings.Join(hostileText, " ")
	l := pageReportsLink()
	l.Name = `rep<x-hostile>"orts`
	l.Description = all
	l.Routes[0].Variables[0].Description = all
	l.Routes[0].Variables[0].Example = all
	l.Routes[1].Destination = "javascript:alert(1)//" + all
	l.Routes = append(l.Routes, linkmodel.Route{ID: 11, Pattern: "reports/" + all, Destination: "https://x.example/" + all})
	return l
}

// inPageFragment is a link to a fixed place on the same page, such as the
// skip link's "#main" or the setup page's "#shortcut".
var inPageFragment = regexp.MustCompile(`^#[A-Za-z][A-Za-z0-9_-]*$`)

// assertSafePage fails if hostile text came through unescaped: as markup,
// as an attribute, as a URL a link or resource would follow, or as a
// direction override outside the <bdi> that isolates it.
//
// It applies the policy checks of assertObeysPolicy except the search for
// "javascript:" anywhere: hostile text may say that, as text. A URL that
// says it is caught by the check of every href and src.
func assertSafePage(t *testing.T, body string) {
	t.Helper()
	for _, tag := range scriptTag.FindAllString(body, -1) {
		if !strings.Contains(tag, " src=") {
			t.Errorf("inline script: %s", tag)
		}
	}
	for name, re := range map[string]*regexp.Regexp{"event handler attribute": handlerAttr, "style attribute": styleAttr, "remote resource": remoteLoad} {
		if m := re.FindString(body); m != "" {
			t.Errorf("%s: %s", name, m)
		}
	}
	if strings.Contains(strings.ToLower(body), "<style") {
		t.Error("page contains a style element")
	}
	for _, banned := range []string{"<x-hostile", `data-injected="`, `data-injected='`} {
		if strings.Contains(body, banned) {
			t.Errorf("hostile text came through unescaped: %q", banned)
		}
	}
	for _, m := range regexp.MustCompile(`\s(href|src|action)="([^"]*)"`).FindAllStringSubmatch(body, -1) {
		u := html.UnescapeString(m[2])
		safe := inPageFragment.MatchString(u) || (strings.HasPrefix(u, "/") && !strings.HasPrefix(u, "//") && !strings.HasPrefix(u, `/\`))
		if !safe {
			t.Errorf("unsafe %s=%q", m[1], u)
		}
	}
	assertBidiIsolated(t, body)
}

// assertBidiIsolated fails if a direction control character appears in the
// text of a page outside a <bdi> element. Inside attributes it is harmless;
// in the title the browser shows it alone.
func assertBidiIsolated(t *testing.T, body string) {
	t.Helper()
	inTag, depth := false, 0
	for i := 0; i < len(body); {
		switch {
		case strings.HasPrefix(body[i:], "<bdi"):
			depth++
		case strings.HasPrefix(body[i:], "</bdi>"):
			depth--
		case strings.HasPrefix(body[i:], "<title>"):
			end := strings.Index(body[i:], "</title>")
			if end > 0 {
				i += end
				continue
			}
		}
		switch body[i] {
		case '<':
			inTag = true
		case '>':
			inTag = false
		}
		r, size := rune(body[i]), 1
		if body[i] >= 0x80 {
			r, size = decodeRune(body[i:])
		}
		if !inTag && depth == 0 && isBidiControl(r) {
			start := max(0, i-60)
			t.Errorf("direction control U+%04X outside <bdi>: …%s…", r, body[start:i])
		}
		i += size
	}
}

func decodeRune(s string) (rune, int) {
	for _, r := range s {
		return r, len(string(r))
	}
	return 0, 1
}

func isBidiControl(r rune) bool {
	return (r >= 0x202A && r <= 0x202E) || (r >= 0x2066 && r <= 0x2069) || r == 0x200E || r == 0x200F
}

// NFR-11: whatever a link holds, the link page and the delete page show it
// as text.
func TestLinkPagesEscapeHostileData(t *testing.T) {
	l := hostileLink()
	for _, editable := range []bool{true, false} {
		svc := &fakeService{owner: strings.Join(hostileText, " "), fakeQueries: fakeQueries{byNameFn: func(string) (linkmodel.Link, bool, error) {
			return l, editable, nil
		}}}
		h := newTestServer(svc, nil).Handler()
		for _, target := range []string{"/-/links/reports", "/-/links/reports/delete"} {
			rec := get(h, target)
			want := http.StatusOK
			if !editable && strings.HasSuffix(target, "/delete") {
				want = http.StatusForbidden
			}
			if rec.Code != want {
				t.Fatalf("%s: status = %d, want %d", target, rec.Code, want)
			}
			assertSafePage(t, rec.Body.String())
		}
	}
}
