package httpd

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/tfrydrychewicz/ogre/internal/app"
	"github.com/tfrydrychewicz/ogre/internal/linkmodel"
)

// An empty Directory says how to start: a new link, and the browser setup.
func TestHomeFirstRun(t *testing.T) {
	rec := get(newTestServer(&fakeService{}, nil).Handler(), "/-/")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	assertObeysPolicy(t, body)
	for _, want := range []string{
		"<title>Ogre</title>",
		"Your Directory is empty",
		`<a class="button button-primary" href="/-/new">New link</a>`,
		`<a class="button" href="/-/setup">Set up your browser</a>`,
		// SRCH-3: the search box has focus on load.
		`id="q" type="search" name="q" value=""`, " autofocus ",
		// The box has a label of its own, not only its placeholder.
		`<label class="visually-hidden" for="q">Search go links</label>`,
		`src="` + assetURL(t, "js/home.js") + `"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("home lacks %q", want)
		}
	}
	// Home has its own search box; the header's is left out.
	if strings.Contains(body, `id="site-search-q"`) {
		t.Error("home shows the header's search box as well as its own")
	}
	if !regexp.MustCompile(`id="results-section"[^>]*hidden`).MatchString(body) {
		t.Error("the results section is shown without a search")
	}
}

// The lists of the home page are the server's (SRCH-3), so they work without
// JavaScript. A link without variables opens directly, through the
// resolver; one with variables opens its page at the launcher (SRCH-4).
func TestHomeLists(t *testing.T) {
	roadmap, reports := sampleLink(), pageReportsLink()
	svc := &fakeService{fakeQueries: fakeQueries{
		yourLinks:   []linkmodel.Link{reports},
		recentLinks: []linkmodel.Link{roadmap, reports},
	}}
	rec := get(newTestServer(svc, nil).Handler(), "/-/")
	body := rec.Body.String()
	assertObeysPolicy(t, body)
	for _, want := range []string{
		`<h2 class="section-title" id="yours-title">Your links</h2>`,
		`<h2 class="section-title" id="recent-title">Recently added</h2>`,
		`<a class="result-link golink" href="/roadmap" data-direct="true">go/roadmap</a>`,
		`<a class="result-link golink" href="/-/links/reports#launch">go/reports</a>`,
		`<a class="result-details" href="/-/links/roadmap">Details`,
		`go/reports/<span class="pattern-var">{month:int}</span>/<span class="pattern-var">{day:int}</span>`,
		"<bdi>reports.example.com</bdi>", "<bdi>docs.example.com</bdi>",
		"<bdi>Where we are going</bdi>", "Owner: <bdi>Local user</bdi>",
		"Takes values",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("home lacks %q", want)
		}
	}
	if strings.Contains(body, "Your Directory is empty") {
		t.Error("a Directory with links is shown as empty")
	}
	if got := strings.Count(body, `<li class="result">`); got != 3 {
		t.Errorf("%d list entries, want 3", got)
	}
}

// A link with many routes lists the first three and counts the rest.
func TestHomeListsManyRoutes(t *testing.T) {
	l := sampleLink()
	for i := range 4 {
		l.Routes = append(l.Routes, linkmodel.Route{ID: int64(40 + i), Pattern: "roadmap/q" + string(rune('1'+i)), Destination: "https://docs.example.com/"})
	}
	svc := &fakeService{fakeQueries: fakeQueries{recentLinks: []linkmodel.Link{l}}}
	body := get(newTestServer(svc, nil).Handler(), "/-/").Body.String()
	if !strings.Contains(body, "and 3 more routes") {
		t.Error("the routes beyond three are not counted")
	}
	if strings.Contains(body, "roadmap/q2") {
		t.Error("more than three routes are listed")
	}
}

// Without JavaScript, the search form is a GET of the home page, and the
// server renders the ranked results in the Service's order.
func TestHomeSearchWithoutJavaScript(t *testing.T) {
	reports, roadmap := pageReportsLink(), sampleLink()
	svc := &fakeService{fakeQueries: fakeQueries{
		searchFn: func(q string, limit int) ([]app.SearchResult, error) {
			return []app.SearchResult{
				{Link: reports, Rank: app.RankMisspelled, Edits: 1},
				{Link: roadmap, Rank: app.RankDescription},
			}, nil
		},
		recentLinks: []linkmodel.Link{roadmap, reports},
	}}
	h := newTestServer(svc, nil).Handler()
	body := get(h, "/-/?q=+reprots+").Body.String()
	assertObeysPolicy(t, body)

	// One more than the page shows, to tell whether more matched.
	if got := svc.searches(); len(got) != 1 || got[0].q != "reprots" || got[0].limit != app.DefaultListLimit+1 {
		t.Errorf("searches = %+v, want one for \"reprots\" with the default limit and one more", got)
	}
	for _, want := range []string{
		`<form class="home-search" id="home-search" role="search" action="/-/" method="get" data-limit="20" data-routes-shown="3">`,
		`name="q" value="reprots"`,
		`role="status">2 links match.</p>`,
		`<span class="badge">Similar name</span>`,
		// Without JavaScript nothing appears as you type, and the page
		// does not say it would.
		`<span class="js-only" hidden>Results appear as you type.`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("results page lacks %q", want)
		}
	}
	results := body[strings.Index(body, `id="results"`):]
	if i, j := strings.Index(results, "go/reports"), strings.Index(results, "go/roadmap"); i < 0 || j < 0 || i > j {
		t.Error("results are not in the Service's order")
	}
	if regexp.MustCompile(`id="results-section"[^>]*hidden`).MatchString(body) {
		t.Error("the results are hidden")
	}
	if !regexp.MustCompile(`id="home-lists"[^>]*hidden`).MatchString(body) {
		t.Error("the lists are shown beside the results")
	}

	// Nothing found: said so, with a way to make the link.
	svc.searchFn = nil
	body = get(h, "/-/?q=nothing").Body.String()
	if !strings.Contains(body, `role="status">No link matches your search.</p>`) {
		t.Error("an empty search does not say so")
	}
	if regexp.MustCompile(`id="search-create"[^>]*hidden`).MatchString(body) {
		t.Error("an empty search offers no new link")
	}
	if !strings.Contains(body, `id="search-create-link" href="/-/new?pattern=nothing"`) {
		t.Error("the new link offered for an empty search is not prefilled with the query")
	}

	// A blank query is no search.
	get(h, "/-/?q=+++")
	if got := svc.searches(); len(got) != 2 {
		t.Errorf("a blank query was searched: %+v", got)
	}
}

// The results stop at the limit, and the page says that more matched only
// when more did: exactly as many as the limit is the whole list.
func TestHomeSearchCapped(t *testing.T) {
	for _, tt := range []struct {
		matches    int
		wantShown  int
		wantCapped bool
	}{
		{app.DefaultListLimit - 1, app.DefaultListLimit - 1, false},
		{app.DefaultListLimit, app.DefaultListLimit, false},
		{app.DefaultListLimit + 1, app.DefaultListLimit, true},
	} {
		svc := &fakeService{fakeQueries: fakeQueries{
			searchFn: func(q string, limit int) ([]app.SearchResult, error) {
				var out []app.SearchResult
				for i := 0; i < min(tt.matches, limit); i++ {
					l := sampleLink()
					l.Name = fmt.Sprintf("r%02d", i)
					out = append(out, app.SearchResult{Link: l, Rank: app.RankPrefix})
				}
				return out, nil
			},
			recentLinks: []linkmodel.Link{sampleLink()},
		}}
		body := get(newTestServer(svc, nil).Handler(), "/-/?q=r").Body.String()
		results := body[strings.Index(body, `id="results"`):strings.Index(body, `id="search-create"`)]
		if got := strings.Count(results, `<li class="result">`); got != tt.wantShown {
			t.Errorf("%d matches: %d results shown, want %d", tt.matches, got, tt.wantShown)
		}
		capped := strings.Contains(body, fmt.Sprintf("; showing the best %d.", app.DefaultListLimit))
		if capped != tt.wantCapped {
			t.Errorf("%d matches: \"showing the best\" said = %v, want %v", tt.matches, capped, tt.wantCapped)
		}
	}
}

// A failing search or list is a logged 500 page that repeats nothing.
func TestHomeServiceError(t *testing.T) {
	svc := &fakeService{fakeQueries: fakeQueries{searchFn: func(string, int) ([]app.SearchResult, error) {
		return nil, errors.New("index on fire")
	}}}
	rec := get(newTestServer(svc, nil).Handler(), "/-/?q=secret-query")
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
	if body := rec.Body.String(); strings.Contains(body, "index on fire") || strings.Contains(body, "secret-query") {
		t.Error("the error page repeats the error or the query")
	}
}

// NFR-11: hostile links, owners and queries are shown as text, in results
// and lists alike.
func TestHomeEscapesHostileData(t *testing.T) {
	l := hostileLink()
	svc := &fakeService{owner: strings.Join(hostileText, " "), fakeQueries: fakeQueries{
		searchFn: func(string, int) ([]app.SearchResult, error) {
			return []app.SearchResult{{Link: l, Rank: app.RankName}}, nil
		},
		yourLinks:   []linkmodel.Link{l},
		recentLinks: []linkmodel.Link{l},
	}}
	h := newTestServer(svc, nil).Handler()
	for _, target := range []string{
		"/-/",
		"/-/?q=" + strings.NewReplacer(" ", "+", `"`, "%22", "<", "%3C", ">", "%3E", "'", "%27").Replace(strings.Join(hostileText, " ")),
	} {
		rec := get(h, target)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status = %d", target, rec.Code)
		}
		assertSafePage(t, rec.Body.String())
	}
}
