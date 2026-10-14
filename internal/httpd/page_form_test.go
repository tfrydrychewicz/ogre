package httpd

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"html"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/tfrydrychewicz/ogre/internal/app"
	"github.com/tfrydrychewicz/ogre/internal/linkmodel"
)

// hostileTexts are what a user, another page's link or a stored link could
// put into the form: markup, quotes, a script URL, a closing template tag,
// and Unicode bidi controls that would reorder what is shown.
var hostileTexts = []string{
	`"><script>alert(1)</script>`,
	`javascript:alert(1)`,
	`'single' "double" & <b>bold</b>`,
	"\u202Egnp.exe\u202C \u2066x\u2069",
	`</template><script src="https://evil.example/x.js"></script>`,
	`{{.Name}} ${x} </noscript><img src=x onerror=alert(1)>`,
}

var (
	formInitialAttr = regexp.MustCompile(`data-initial="([^"]*)"`)
	formHref        = regexp.MustCompile(`(?i)\shref="([^"]*)"`)
)

// formInitial decodes the draft a form page hands its module.
func formInitial(t *testing.T, body string) formDraftJSON {
	t.Helper()
	m := formInitialAttr.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no data-initial in the page:\n%s", body)
	}
	raw, err := base64.StdEncoding.DecodeString(m[1])
	if err != nil {
		t.Fatalf("data-initial is not base64: %v\n%s", err, m[1])
	}
	var draft formDraftJSON
	if err := json.Unmarshal(raw, &draft); err != nil {
		t.Fatalf("data-initial is not JSON: %v\n%s", err, raw)
	}
	return draft
}

// assertFormPageSafe checks what every form page must hold whatever the data:
// the policy is obeyed, no hostile text appears unescaped, every link stays
// on the Directory, and the page explains itself without JavaScript.
func assertFormPageSafe(t *testing.T, body string) {
	t.Helper()
	assertObeysPolicy(t, body)
	for _, raw := range []string{"<script>alert", "<img src=x", "<b>bold</b>", "</template><script", `"><script`} {
		if strings.Contains(body, raw) {
			t.Errorf("the page holds %q unescaped", raw)
		}
	}
	for _, m := range formHref.FindAllStringSubmatch(body, -1) {
		href := html.UnescapeString(m[1])
		if strings.HasPrefix(href, "#") {
			continue // within the page: the skip link
		}
		if !strings.HasPrefix(href, "/") || strings.HasPrefix(href, "//") {
			t.Errorf("href %q does not stay on the Directory", href)
		}
	}
	// Without JavaScript: the form stays hidden, and the page says why.
	if !strings.Contains(body, "<noscript>") || !strings.Contains(body, "needs JavaScript") {
		t.Errorf("the page has no <noscript> explanation")
	}
	if !regexp.MustCompile(`<form id="link-form"[^>]*\shidden[\s>]`).MatchString(body) {
		t.Errorf("the form is not hidden until its module runs")
	}
	// The type selector's choices are the link model's type names.
	for _, typ := range []string{linkmodel.TypeString, linkmodel.TypeInt, linkmodel.TypeNumber, linkmodel.TypeRegex} {
		if !strings.Contains(body, `<option value="`+typ+`">`) {
			t.Errorf("the type selector lacks %q", typ)
		}
	}
	for _, asset := range []string{"js/form.js", "css/form.css", "css/link.css"} {
		if !strings.Contains(body, assetURL(t, asset)) {
			t.Errorf("the page does not load %s", asset)
		}
	}
}

func TestNewForm(t *testing.T) {
	h := newTestServer(&fakeService{}, nil).Handler()
	rec := get(h, "/-/new")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	assertFormPageSafe(t, body)
	for _, want := range []string{
		"<h1>New link</h1>", `href="/-/new" aria-current="page"`, `data-mode="new"`, ">Create link</button>",
		// The form's limits and type labels are the link model's, read by
		// the module rather than copied into it.
		`data-max-routes="20"`, `<option value="int">Whole number</option>`, `<option value="regex">Regular expression</option>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	got := formInitial(t, body)
	want := formDraftJSON{Routes: []formRouteJSON{{Variables: map[string]variableRequest{}}}}
	if !jsonEqual(t, got, want) {
		t.Errorf("initial draft = %+v, want one empty route", got)
	}
}

// DIR-7: the form opens prefilled from the address, whatever the address
// holds; the text reaches the form's fields only, as data.
func TestNewFormPrefilledWithHostileText(t *testing.T) {
	h := newTestServer(&fakeService{}, nil).Handler()
	for _, text := range hostileTexts {
		t.Run(text, func(t *testing.T) {
			q := url.Values{"pattern": {text}, "destination": {text}}
			rec := get(h, "/-/new?"+q.Encode())
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d", rec.Code)
			}
			body := rec.Body.String()
			assertFormPageSafe(t, body)
			got := formInitial(t, body)
			if len(got.Routes) != 1 || got.Routes[0].Pattern != text || got.Routes[0].Destination != text {
				t.Errorf("initial routes = %+v, want the text as typed", got.Routes)
			}
			// Nowhere else on the page, escaped or not: not in the title,
			// the heading, or the header's search box.
			if strings.Contains(body, text) || strings.Contains(body, html.EscapeString(text)) {
				t.Errorf("the text appears in the page outside the encoded draft")
			}
		})
	}
}

func TestNewFormPrefilledRoutes(t *testing.T) {
	h := newTestServer(&fakeService{}, nil).Handler()
	tests := []struct {
		name  string
		query url.Values
		want  []formRouteJSON
	}{
		{"pattern only", url.Values{"pattern": {"reprots/10/13"}},
			[]formRouteJSON{{Pattern: "reprots/10/13"}}},
		{"destination only, from the extension", url.Values{"destination": {"https://example.com/a?b=c"}},
			[]formRouteJSON{{Destination: "https://example.com/a?b=c"}}},
		{"pairs by position", url.Values{"pattern": {"gh/a", "gh/b"}, "destination": {"https://a.example/"}},
			[]formRouteJSON{{Pattern: "gh/a", Destination: "https://a.example/"}, {Pattern: "gh/b"}}},
		{"empty values", url.Values{"pattern": {""}, "destination": {""}},
			[]formRouteJSON{{}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formInitial(t, get(h, "/-/new?"+tt.query.Encode()).Body.String())
			for i := range tt.want {
				tt.want[i].Variables = map[string]variableRequest{}
			}
			if !jsonEqual(t, got.Routes, tt.want) {
				t.Errorf("routes = %+v, want %+v", got.Routes, tt.want)
			}
		})
	}

	// More routes than a link can have are cut to that number.
	q := url.Values{}
	for i := 0; i < linkmodel.MaxRoutes+5; i++ {
		q.Add("pattern", "a")
	}
	if got := formInitial(t, get(h, "/-/new?"+q.Encode()).Body.String()); len(got.Routes) != linkmodel.MaxRoutes {
		t.Errorf("%d routes asked for: the form got %d, want %d", linkmodel.MaxRoutes+5, len(got.Routes), linkmodel.MaxRoutes)
	}
}

// formLink is a stored link with hostile text wherever a link has free text.
func formLink() linkmodel.Link {
	l := sampleLink()
	l.Description = hostileTexts[0] + hostileTexts[3]
	l.Version = 7
	l.Routes = []linkmodel.Route{
		{ID: 31, Pattern: "roadmap", Destination: "https://docs.example.com/roadmap?q=" + url.QueryEscape(hostileTexts[0])},
		{ID: 32, Pattern: "roadmap/{when:regex(^(now|next)$)}", Destination: "https://docs.example.com/{when}",
			Variables: []linkmodel.VariableMeta{{Name: "when", Description: hostileTexts[4], Example: hostileTexts[5]}}},
		{ID: 33, Pattern: "roadmap/{n:int}", Destination: "https://docs.example.com/{n}",
			Variables: []linkmodel.VariableMeta{{Name: "n"}}},
	}
	return l
}

func TestEditForm(t *testing.T) {
	link := formLink()
	var asked []string
	svc := &fakeService{fakeQueries: fakeQueries{byNameFn: func(name string) (linkmodel.Link, bool, error) {
		asked = append(asked, name)
		return link, true, nil
	}}}
	h := newTestServer(svc, nil).Handler()
	rec := get(h, "/-/links/RoadMap/edit")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if len(asked) != 1 || asked[0] != "RoadMap" {
		t.Errorf("LinkByName asked for %q, want the name from the address", asked)
	}
	body := rec.Body.String()
	assertFormPageSafe(t, body)
	for _, want := range []string{
		`<h1>Edit <span class="golink">go/roadmap</span></h1>`, `<title>Edit go/roadmap · Ogre</title>`,
		`data-mode="edit"`, `href="/-/links/roadmap">View link</a>`, ">Save changes</button>",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	got := formInitial(t, body)
	want := formDraftJSON{
		LinkID: 12, Version: 7, Name: "roadmap", Description: link.Description,
		Routes: []formRouteJSON{
			{ID: 31, Pattern: link.Routes[0].Pattern, Destination: link.Routes[0].Destination, Variables: map[string]variableRequest{}},
			{ID: 32, Pattern: link.Routes[1].Pattern, Destination: link.Routes[1].Destination,
				Variables: map[string]variableRequest{"when": {Description: hostileTexts[4], Example: hostileTexts[5]}}},
			// A variable with nothing stored is left out, as the API would.
			{ID: 33, Pattern: link.Routes[2].Pattern, Destination: link.Routes[2].Destination, Variables: map[string]variableRequest{}},
		},
	}
	if !jsonEqual(t, got, want) {
		t.Errorf("initial draft =\n %+v\nwant\n %+v", got, want)
	}
}

// DIR-4: the create form sends a user whose name is taken to the existing
// link's edit form with the routes to add; they arrive at the end.
func TestEditFormAppendsRoutes(t *testing.T) {
	link := formLink()
	svc := &fakeService{fakeQueries: fakeQueries{byNameFn: func(string) (linkmodel.Link, bool, error) { return link, true, nil }}}
	h := newTestServer(svc, nil).Handler()
	q := url.Values{"pattern": {"roadmap/" + hostileTexts[0]}, "destination": {hostileTexts[1]}}
	rec := get(h, "/-/links/roadmap/edit?"+q.Encode())
	body := rec.Body.String()
	assertFormPageSafe(t, body)
	got := formInitial(t, body)
	if got.Appended != 1 || len(got.Routes) != 4 {
		t.Fatalf("appended %d, %d routes; want 1 and 4", got.Appended, len(got.Routes))
	}
	if r := got.Routes[3]; r.ID != 0 || r.Pattern != "roadmap/"+hostileTexts[0] || r.Destination != hostileTexts[1] {
		t.Errorf("appended route = %+v", r)
	}

	// Never more routes than a link can have.
	many := url.Values{}
	for i := 0; i < linkmodel.MaxRoutes; i++ {
		many.Add("pattern", "roadmap/x")
	}
	got = formInitial(t, get(h, "/-/links/roadmap/edit?"+many.Encode()).Body.String())
	if len(got.Routes) != linkmodel.MaxRoutes || got.Appended != linkmodel.MaxRoutes-3 {
		t.Errorf("%d routes, %d appended; want %d and %d", len(got.Routes), got.Appended, linkmodel.MaxRoutes, linkmodel.MaxRoutes-3)
	}
}

// The edit form of a link that cannot be edited is not offered: a missing
// link is a 404, someone else's a 403 that names the owner, and a failure a
// logged 500. None of them repeats the address.
func TestEditFormRefusals(t *testing.T) {
	const secret = "s3cr3t"
	link := formLink()
	tests := []struct {
		name       string
		byName     func(string) (linkmodel.Link, bool, error)
		owner      string
		wantStatus int
		want       []string
		wantLog    bool
	}{
		{"missing", func(string) (linkmodel.Link, bool, error) { return linkmodel.Link{}, false, app.ErrNotFound },
			"", http.StatusNotFound, []string{"No such link"}, false},
		{"not the user's", func(string) (linkmodel.Link, bool, error) { return link, false, nil },
			hostileTexts[0], http.StatusForbidden,
			[]string{"You can&#39;t edit this link", "go/roadmap belongs to " + html.EscapeString(hostileTexts[0])}, false},
		{"store failure", func(string) (linkmodel.Link, bool, error) { return linkmodel.Link{}, false, errors.New("disk on fire") },
			"", http.StatusInternalServerError, []string{"Something went wrong"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var logs bytes.Buffer
			svc := &fakeService{owner: tt.owner, fakeQueries: fakeQueries{byNameFn: tt.byName}}
			h := newTestServer(svc, func(o *Options) { o.Logger = slog.New(slog.NewTextHandler(&logs, nil)) }).Handler()
			rec := get(h, "/-/links/"+secret+"/edit?pattern="+secret)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			body := rec.Body.String()
			assertObeysPolicy(t, body)
			for _, want := range tt.want {
				if !strings.Contains(body, want) {
					t.Errorf("page lacks %q", want)
				}
			}
			if strings.Contains(body, secret) || strings.Contains(body, "<script>alert") || strings.Contains(body, "data-initial") {
				t.Errorf("the refusal repeats the address, holds raw markup or still offers the form:\n%s", body)
			}
			if got := strings.Contains(logs.String(), "disk on fire"); got != tt.wantLog {
				t.Errorf("error logged = %v, want %v:\n%s", got, tt.wantLog, logs.String())
			}
		})
	}
}

// The form pages are pages: GET and HEAD only.
func TestFormPagesAreReadOnly(t *testing.T) {
	svc := &fakeService{fakeQueries: fakeQueries{byNameFn: func(string) (linkmodel.Link, bool, error) { return formLink(), true, nil }}}
	h := newTestServer(svc, nil).Handler()
	for _, target := range []string{"/-/new", "/-/links/roadmap/edit"} {
		rec := request{method: http.MethodPost, target: target, headers: map[string]string{"Content-Type": "application/json"}, body: "{}"}.send(h)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("POST %s = %d, want 405", target, rec.Code)
		}
	}
	if n := len(svc.createdDrafts()); n != 0 {
		t.Errorf("a page created %d links", n)
	}
}

// jsonEqual compares two values by their JSON, the form the page carries.
func jsonEqual(t *testing.T, got, want any) bool {
	t.Helper()
	a, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	return bytes.Equal(a, b)
}

// A page of another site can open the forms with routes filled in. Routes it
// asks to add to an existing link are left out: one more specific than the
// link's own would take part of its traffic to that site's host after a
// single click on Save. A prefilled create form is kept (DIR-7) with a
// warning that names where each destination leads. Fetch Metadata decides;
// "same-site" counts as another site, since every port of localhost is one.
func TestFormPrefillFromAnotherSite(t *testing.T) {
	link := formLink()
	svc := &fakeService{fakeQueries: fakeQueries{byNameFn: func(string) (linkmodel.Link, bool, error) { return link, true, nil }}}
	h := newTestServer(svc, nil).Handler()
	hijack := url.Values{
		"pattern":     {"roadmap/10/{day:int}", "roadmap/x"},
		"destination": {"https://evil.test/steal?d={day}", "not a url"},
	}.Encode()
	navigation := func(site string) map[string]string {
		if site == "" {
			return nil
		}
		return map[string]string{"Sec-Fetch-Site": site, "Sec-Fetch-Mode": "navigate", "Sec-Fetch-Dest": "document"}
	}

	for _, tt := range []struct {
		site    string
		foreign bool
	}{
		{"", false}, // no Fetch Metadata: a tool, an old browser
		{"none", false},
		{"same-origin", false},
		{"same-site", true},
		{"cross-site", true},
	} {
		t.Run("edit "+tt.site, func(t *testing.T) {
			rec := request{target: "/-/links/roadmap/edit?" + hijack, headers: navigation(tt.site)}.send(h)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d", rec.Code)
			}
			body := rec.Body.String()
			assertFormPageSafe(t, body)
			got := formInitial(t, body)
			wantAppended := 2
			if tt.foreign {
				wantAppended = 0
			}
			if got.Appended != wantAppended || len(got.Routes) != 3+wantAppended {
				t.Errorf("appended %d of %d routes; want %d", got.Appended, len(got.Routes), wantAppended)
			}
			if notice := strings.Contains(body, `id="ignored-prefill"`); notice != tt.foreign {
				t.Errorf("notice of the routes left out shown = %v, want %v", notice, tt.foreign)
			}
			if strings.Contains(body, "evil.test") {
				t.Error("the edit form repeats a destination from the address outside its draft")
			}
		})
		t.Run("new "+tt.site, func(t *testing.T) {
			rec := request{target: "/-/new?" + hijack, headers: navigation(tt.site)}.send(h)
			body := rec.Body.String()
			assertFormPageSafe(t, body)
			if got := formInitial(t, body); len(got.Routes) != 2 || got.Routes[0].Destination != "https://evil.test/steal?d={day}" {
				t.Errorf("the create form's routes = %+v, want both kept", got.Routes)
			}
			warned := strings.Contains(body, `id="foreign-prefill"`)
			if warned != tt.foreign {
				t.Fatalf("warning shown = %v, want %v", warned, tt.foreign)
			}
			if tt.foreign {
				for _, want := range []string{`Opens a page on <bdi class="url">evil.test</bdi>`, "A destination that can't be read yet"} {
					if !strings.Contains(body, want) {
						t.Errorf("the warning lacks %q", want)
					}
				}
			}
		})
	}

	// An empty create form opened from another site has nothing to warn of.
	rec := request{target: "/-/new", headers: navigation("cross-site")}.send(h)
	if strings.Contains(rec.Body.String(), `id="foreign-prefill"`) {
		t.Error("an empty form warns of a prefill")
	}

	// "Add this route" on a create form that another site filled in opens
	// the edit form from the Directory's own page, marked: the routes are
	// appended, under the same warning, for the same reason.
	for _, tt := range []struct {
		name, site, query string
		appended          int
		warned, ignored   bool
	}{
		{"handed over, marked", "same-origin", hijack + "&prefill=foreign", 2, true, false},
		{"handed over, unmarked", "same-origin", hijack, 2, false, false},
		{"marked, without Fetch Metadata", "", hijack + "&prefill=foreign", 2, true, false},
		{"marked, from another site", "cross-site", hijack + "&prefill=foreign", 0, false, true},
		{"marked, nothing to add", "same-origin", "prefill=foreign", 0, false, false},
		{"another value", "same-origin", hijack + "&prefill=yes", 2, false, false},
	} {
		t.Run("edit "+tt.name, func(t *testing.T) {
			rec := request{target: "/-/links/roadmap/edit?" + tt.query, headers: navigation(tt.site)}.send(h)
			body := rec.Body.String()
			assertFormPageSafe(t, body)
			if got := formInitial(t, body); got.Appended != tt.appended {
				t.Errorf("appended %d routes, want %d", got.Appended, tt.appended)
			}
			if warned := strings.Contains(body, `id="foreign-prefill"`); warned != tt.warned {
				t.Errorf("warning shown = %v, want %v", warned, tt.warned)
			}
			if ignored := strings.Contains(body, `id="ignored-prefill"`); ignored != tt.ignored {
				t.Errorf("notice of the routes left out shown = %v, want %v", ignored, tt.ignored)
			}
			if tt.warned {
				for _, want := range []string{
					"filled in the routes added below",
					`Opens a page on <bdi class="url">evil.test</bdi>`,
					"A destination that can't be read yet",
				} {
					if !strings.Contains(body, want) {
						t.Errorf("the warning lacks %q", want)
					}
				}
			}
		})
	}
}
