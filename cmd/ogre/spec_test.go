package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tfrydrychewicz/ogre/internal/app"
	"github.com/tfrydrychewicz/ogre/internal/httpd"
	"github.com/tfrydrychewicz/ogre/internal/store"
)

// The worked examples of PRD 5.7 are data, kept once, in the link model's
// testdata (see its README.txt). The link model replays them against its own
// functions and package app against the use cases; this file replays them
// through HTTP, against the real command on a real port with a real database
// file (plan L2, "done when every P0 fixture passes through the Go API and
// through HTTP").
//
// Every request row is asked twice: as a go link (GET /<path>, which must
// redirect or answer 404) and of the resolve endpoint (which must say why).
// Every save row goes through POST or PATCH /-/api/v1/links.

const specPath = "../../internal/linkmodel/testdata/spec.json"

// The fixture file. The shape is described in the README next to it.
type (
	specFile struct {
		Format   string        `json:"format"`
		Version  int           `json:"version"`
		Source   string        `json:"source"`
		Links    []specLink    `json:"links"`
		Requests []specRequest `json:"requests"`
		Saves    []specSave    `json:"saves"`
	}
	specLink struct {
		Name        string      `json:"name"`
		Description string      `json:"description"`
		Routes      []specRoute `json:"routes"`
	}
	specRoute struct {
		Pattern     string `json:"pattern"`
		Destination string `json:"destination"`
	}
	specRequest struct {
		ID        string      `json:"id"`
		Request   string      `json:"request"`
		Path      string      `json:"path"`
		Rule      string      `json:"rule"`
		Expect    specExpect  `json:"expect"`
		Pending   string      `json:"pending"`
		UntilThen *specExpect `json:"untilThen"`
	}
	specExpect struct {
		Kind          string            `json:"kind"`
		Link          string            `json:"link"`
		Route         string            `json:"route"`
		Values        map[string]string `json:"values"`
		Destination   string            `json:"destination"`
		Reasons       []specReason      `json:"reasons"`
		RequestedName string            `json:"requestedName"`
		Suggestions   []specSuggestion  `json:"suggestions"`
	}
	specReason struct {
		Route    string `json:"route"`
		Code     string `json:"code"`
		Variable string `json:"variable"`
		Value    string `json:"value"`
	}
	specSuggestion struct {
		Name        string `json:"name"`
		Path        string `json:"path"`
		Edits       int    `json:"edits"`
		Resolves    bool   `json:"resolves"`
		Description string `json:"description"`
		Destination string `json:"destination"`
	}
	specSave struct {
		ID      string `json:"id"`
		Attempt string `json:"attempt"`
		Outcome string `json:"outcome"`
		Needs   string `json:"needs"`
		Action  string `json:"action"`
		Link    string `json:"link"`
		Actor   string `json:"actor"`
		Draft   struct {
			Routes []specRoute `json:"routes"`
		} `json:"draft"`
		Expect specSaveExpect `json:"expect"`
	}
	specSaveExpect struct {
		Saved      bool          `json:"saved"`
		Name       string        `json:"name"`
		Error      string        `json:"error"`
		Problems   []specProblem `json:"problems"`
		Existing   string        `json:"existing"`
		NamesOwner bool          `json:"namesOwner"`
		LinksTo    string        `json:"linksTo"`
		Then       []struct {
			Path   string     `json:"path"`
			Expect specExpect `json:"expect"`
		} `json:"then"`
	}
	specProblem struct {
		Route    int    `json:"route"`
		Field    string `json:"field"`
		Code     string `json:"code"`
		Mentions string `json:"mentions"`
	}
)

// What the API answers, as far as this test reads it. Responses are decoded
// with unknown fields refused, so a field the plan does not list fails here.
type (
	apiLink struct {
		ID          int64  `json:"id"`
		Name        string `json:"name"`
		Description string `json:"description"`
		Owner       struct {
			ID          int64  `json:"id"`
			DisplayName string `json:"displayName"`
		} `json:"owner"`
		Version int        `json:"version"`
		CanEdit bool       `json:"canEdit"`
		Routes  []apiRoute `json:"routes"`
		Usage   struct {
			Total      int64   `json:"total"`
			Last30Days int64   `json:"last30Days"`
			LastUsedAt *string `json:"lastUsedAt"`
		} `json:"usage"`
		CreatedAt string `json:"createdAt"`
		UpdatedAt string `json:"updatedAt"`
	}
	apiRoute struct {
		ID          int64  `json:"id"`
		Pattern     string `json:"pattern"`
		Destination string `json:"destination"`
		Variables   []struct {
			Name        string `json:"name"`
			Type        string `json:"type"`
			Accepts     string `json:"accepts"`
			Description string `json:"description"`
			Example     string `json:"example"`
		} `json:"variables"`
	}
	apiLinkRef struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}
	apiProblem struct {
		Severity string `json:"severity"`
		Field    string `json:"field"`
		Code     string `json:"code"`
		Message  string `json:"message"`
		Start    int    `json:"start"`
		End      int    `json:"end"`
	}
	apiError struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
			Details struct {
				Problems []apiProblem `json:"problems"`
				Routes   []struct {
					Problems []apiProblem `json:"problems"`
				} `json:"routes"`
				Existing *apiLinkRef `json:"existing"`
				CanEdit  *bool       `json:"canEdit"`
				// Of a 403: the link that may not be changed, and its owner.
				Link  *apiLinkRef `json:"link"`
				Owner *struct {
					ID          int64  `json:"id"`
					DisplayName string `json:"displayName"`
				} `json:"owner"`
			} `json:"details"`
		} `json:"error"`
	}
	// apiDecision is the union of the resolve endpoint's answers; pointers
	// and nil slices tell "absent" from "empty".
	apiDecision struct {
		Kind string `json:"kind"`
		Link *struct {
			ID          int64  `json:"id"`
			Name        string `json:"name"`
			Description string `json:"description"`
		} `json:"link"`
		Route       *apiDecisionRoute `json:"route"`
		Values      map[string]string `json:"values"`
		Destination *string           `json:"destination"`
		Routes      []struct {
			ID      int64  `json:"id"`
			Pattern string `json:"pattern"`
			Reason  struct {
				Code     string `json:"code"`
				Variable string `json:"variable"`
				Value    string `json:"value"`
				Accepts  string `json:"accepts"`
				Message  string `json:"message"`
			} `json:"reason"`
		} `json:"routes"`
		RequestedName *string          `json:"requestedName"`
		Suggestions   []specSuggestion `json:"suggestions"`
	}
	apiDecisionRoute struct {
		ID      int64  `json:"id"`
		Pattern string `json:"pattern"`
	}
)

// strictJSON decodes raw into dst and fails the test on a field dst lacks.
func strictJSON(t *testing.T, what string, raw []byte, dst any) {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		t.Fatalf("decode %s: %v\n%s", what, err, raw)
	}
}

func loadSpec(t *testing.T) specFile {
	t.Helper()
	raw, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatalf("read the spec fixture: %v", err)
	}
	var spec specFile
	strictJSON(t, specPath, raw, &spec)
	if spec.Format != "ogre-spec" || spec.Version != 1 {
		t.Fatalf("%s is %q version %d, want ogre-spec version 1", specPath, spec.Format, spec.Version)
	}
	if len(spec.Links) == 0 || len(spec.Requests) == 0 || len(spec.Saves) == 0 {
		t.Fatalf("%s has %d links, %d requests, %d saves", specPath, len(spec.Links), len(spec.Requests), len(spec.Saves))
	}
	return spec
}

// call sends one request and returns the status, the headers and the body.
// A body is sent as JSON. Redirects are reported, not followed.
func call(t *testing.T, method, target string, body any) (int, http.Header, []byte) {
	t.Helper()
	var payload io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		payload = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, target, payload)
	if err != nil {
		t.Fatalf("%s %s: %v", method, target, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := noRedirects.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, target, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("%s %s: read the body: %v", method, target, err)
	}
	return resp.StatusCode, resp.Header, raw
}

type (
	draftBody struct {
		Description string       `json:"description"`
		Routes      []draftRoute `json:"routes"`
	}
	draftRoute struct {
		ID          int64  `json:"id,omitempty"`
		Pattern     string `json:"pattern"`
		Destination string `json:"destination"`
	}
	updateBody struct {
		draftBody
		Version int `json:"version"`
	}
)

func specDraft(description string, routes []specRoute) draftBody {
	d := draftBody{Description: description}
	for _, r := range routes {
		d.Routes = append(d.Routes, draftRoute{Pattern: r.Pattern, Destination: r.Destination})
	}
	return d
}

// createFixtureLinks saves the fixture's links through POST /links. Creating
// them is part of the test: what was sent is what must come back.
func createFixtureLinks(t *testing.T, base string, spec specFile) {
	t.Helper()
	for _, l := range spec.Links {
		status, _, raw := call(t, http.MethodPost, base+"/-/api/v1/links", specDraft(l.Description, l.Routes))
		if status != http.StatusCreated {
			t.Fatalf("create the fixture link %s: %d %s", l.Name, status, raw)
		}
		var saved apiLink
		strictJSON(t, "the created link", raw, &saved)
		if saved.Name != l.Name || saved.Description != l.Description || len(saved.Routes) != len(l.Routes) || saved.Version != 1 {
			t.Fatalf("the fixture link %s was saved as %s", l.Name, raw)
		}
		for i, r := range l.Routes {
			if saved.Routes[i].Pattern != r.Pattern || saved.Routes[i].Destination != r.Destination {
				t.Fatalf("%s route %d was saved as %+v, want %+v", l.Name, i, saved.Routes[i], r)
			}
		}
	}
}

// listLinks returns GET /links: the raw body, and the links by name.
func listLinks(t *testing.T, base string) (string, map[string]apiLink) {
	t.Helper()
	status, _, raw := call(t, http.MethodGet, base+"/-/api/v1/links", nil)
	if status != http.StatusOK {
		t.Fatalf("GET /links: %d %s", status, raw)
	}
	var out struct {
		Links []apiLink `json:"links"`
	}
	strictJSON(t, "the link list", raw, &out)
	byName := make(map[string]apiLink, len(out.Links))
	for _, l := range out.Links {
		byName[l.Name] = l
	}
	return string(raw), byName
}

// checkRequest holds the server to one "expect" object of the fixture, both
// ways: through the resolve endpoint and as a go link. links is the server's
// current link list, for the IDs an answer must carry.
func checkRequest(t *testing.T, base string, links map[string]apiLink, path string, want specExpect) {
	t.Helper()

	// The resolve endpoint. The fixture's path is the raw path after "go/";
	// as a query value it is encoded once more.
	status, header, raw := call(t, http.MethodGet, base+"/-/api/v1/resolve?path="+url.QueryEscape(path), nil)
	if status != http.StatusOK || !strings.HasPrefix(header.Get("Content-Type"), "application/json") {
		t.Fatalf("resolve %q: %d %s %s", path, status, header.Get("Content-Type"), raw)
	}
	var got apiDecision
	strictJSON(t, "the decision", raw, &got)
	if got.Kind != want.Kind {
		t.Fatalf("resolve %q: kind = %s, want %s (%s)", path, got.Kind, want.Kind, raw)
	}

	// The go link itself. The path goes out exactly as the fixture writes it,
	// escapes, trailing slash and query string included.
	pageStatus, pageHeader, page := call(t, http.MethodGet, base+"/"+path, nil)
	if got := pageHeader.Get("Cache-Control"); got != "no-store" && want.Kind != "home" {
		t.Errorf("GET /%s: Cache-Control = %q, want no-store", path, got)
	}
	expectPage := func(status int, contains ...string) {
		t.Helper()
		if pageStatus != status || pageHeader.Get("Location") != "" {
			t.Errorf("GET /%s = %d (Location %q), want %d", path, pageStatus, pageHeader.Get("Location"), status)
		}
		if ct := pageHeader.Get("Content-Type"); ct != "text/html; charset=utf-8" {
			t.Errorf("GET /%s: Content-Type = %q, want the miss page", path, ct)
		}
		// What the user reads: the page's text, as a browser shows it.
		text := visibleText(page)
		for _, want := range contains {
			if !strings.Contains(text, want) && !strings.Contains(string(page), want) {
				t.Errorf("GET /%s: the page lacks %q:\n%s", path, want, text)
			}
		}
	}

	switch want.Kind {
	case "home":
		if got.Link != nil || got.Route != nil || got.Routes != nil || got.Suggestions != nil || got.Destination != nil || got.RequestedName != nil {
			t.Errorf("resolve %q: a home decision carries more than its kind: %s", path, raw)
		}
		if pageStatus != http.StatusFound || pageHeader.Get("Location") != "/-/" {
			t.Errorf("GET /%s = %d to %q, want 302 to the Directory's own page /-/", path, pageStatus, pageHeader.Get("Location"))
		}

	case "hit":
		link, known := links[want.Link]
		if !known {
			t.Fatalf("the fixture expects link %q, which the server does not list", want.Link)
		}
		if got.Link == nil || got.Link.Name != want.Link || got.Link.ID != link.ID || got.Link.Description != link.Description {
			t.Errorf("resolve %q: link = %+v, want %s (id %d)", path, got.Link, want.Link, link.ID)
		}
		var wantRoute apiDecisionRoute
		for _, r := range link.Routes {
			if r.Pattern == want.Route {
				wantRoute = apiDecisionRoute{ID: r.ID, Pattern: r.Pattern}
			}
		}
		if got.Route == nil || *got.Route != wantRoute || wantRoute.ID == 0 {
			t.Errorf("resolve %q: route = %+v, want %+v", path, got.Route, wantRoute)
		}
		// "values" is always an object, {} for a route without variables.
		if got.Values == nil || len(got.Values) != len(want.Values) || (len(want.Values) > 0 && !reflect.DeepEqual(got.Values, want.Values)) {
			t.Errorf("resolve %q: values = %v, want %v", path, got.Values, want.Values)
		}
		if got.Destination == nil || *got.Destination != want.Destination {
			t.Errorf("resolve %q: destination = %v, want %q", path, got.Destination, want.Destination)
		}
		if got.Routes != nil || got.Suggestions != nil || got.RequestedName != nil {
			t.Errorf("resolve %q: a hit carries what belongs to a miss: %s", path, raw)
		}
		if pageStatus != http.StatusFound || pageHeader.Get("Location") != want.Destination {
			t.Errorf("GET /%s = %d to %q, want 302 to %q", path, pageStatus, pageHeader.Get("Location"), want.Destination)
		}

	case "miss_known":
		link, known := links[want.Link]
		if !known {
			t.Fatalf("the fixture expects link %q, which the server does not list", want.Link)
		}
		if got.Link == nil || got.Link.Name != want.Link || got.Link.ID != link.ID {
			t.Errorf("resolve %q: link = %+v, want %s (id %d)", path, got.Link, want.Link, link.ID)
		}
		if got.Route != nil || got.Destination != nil || got.Values != nil || got.Suggestions != nil || got.RequestedName != nil {
			t.Errorf("resolve %q: a known-name miss carries what belongs to another kind: %s", path, raw)
		}
		if len(got.Routes) != len(want.Reasons) || len(got.Routes) != len(link.Routes) {
			t.Fatalf("resolve %q: %d routes with reasons, want %d, one per route: %s", path, len(got.Routes), len(want.Reasons), raw)
		}
		pageLines := []string{"go/" + want.Link + " can't open this address"}
		for i, w := range want.Reasons {
			g := got.Routes[i]
			if g.Pattern != w.Route || g.ID != link.Routes[i].ID {
				t.Errorf("resolve %q: route %d = %d %s, want %d %s", path, i, g.ID, g.Pattern, link.Routes[i].ID, w.Route)
			}
			if g.Reason.Code != w.Code || g.Reason.Variable != w.Variable || g.Reason.Value != w.Value {
				t.Errorf("resolve %q: reason %d = {%s %q %q}, want {%s %q %q}", path, i,
					g.Reason.Code, g.Reason.Variable, g.Reason.Value, w.Code, w.Variable, w.Value)
			}
			// MISS-2: a sentence that names the variable, the value and what
			// the variable accepts.
			if g.Reason.Message == "" || !strings.Contains(g.Reason.Message, w.Variable) || !strings.Contains(g.Reason.Message, w.Value) {
				t.Errorf("resolve %q: reason %d message %q does not mention %q and %q", path, i, g.Reason.Message, w.Variable, w.Value)
			}
			if w.Variable != "" && (g.Reason.Accepts == "" || !strings.Contains(g.Reason.Message, g.Reason.Accepts)) {
				t.Errorf("resolve %q: reason %d does not say what %s accepts: %+v", path, i, w.Variable, g.Reason)
			}
			// The page shows each route with the very sentence the API gave.
			pageLines = append(pageLines, "go/"+w.Route, "Why it doesn't fit: "+g.Reason.Message)
		}
		expectPage(http.StatusNotFound, pageLines...)

	case "miss_unknown":
		if got.RequestedName == nil || *got.RequestedName != want.RequestedName {
			t.Errorf("resolve %q: requestedName = %v, want %q", path, got.RequestedName, want.RequestedName)
		}
		if got.Link != nil || got.Route != nil || got.Routes != nil || got.Destination != nil || got.Values != nil {
			t.Errorf("resolve %q: an unknown-name miss carries a link: %s", path, raw)
		}
		// "suggestions" is always an array; the fixture lists all of it.
		if got.Suggestions == nil || len(got.Suggestions) != len(want.Suggestions) ||
			(len(want.Suggestions) > 0 && !reflect.DeepEqual(got.Suggestions, want.Suggestions)) {
			t.Errorf("resolve %q: suggestions = %+v, want %+v (%s)", path, got.Suggestions, want.Suggestions, raw)
		}
		var pageLines []string
		if want.RequestedName != "" {
			pageLines = append(pageLines, "Create go/")
		}
		for _, s := range want.Suggestions {
			// The suggestion is the corrected go link; when it resolves,
			// the link goes to that address itself (MISS-1).
			pageLines = append(pageLines, "go/"+s.Path)
			if s.Resolves {
				pageLines = append(pageLines, `href="/`+s.Path+`"`)
			}
			// Following the suggestion leads where it says.
			if s.Resolves {
				followed, followedHeader, _ := call(t, http.MethodGet, base+"/"+s.Path, nil)
				if followed != http.StatusFound || followedHeader.Get("Location") != s.Destination {
					t.Errorf("the suggestion go/%s = %d to %q, want 302 to %q", s.Path, followed, followedHeader.Get("Location"), s.Destination)
				}
			}
		}
		expectPage(http.StatusNotFound, pageLines...)
		if want.RequestedName != "" && !strings.Contains(strings.ToLower(visibleText(page)), "no link go/"+want.RequestedName) {
			t.Errorf("GET /%s does not name the missing link go/%s:\n%s", path, want.RequestedName, visibleText(page))
		}
		if len(want.Suggestions) == 0 && strings.Contains(string(page), "Did you mean") {
			t.Errorf("GET /%s suggests something, want nothing:\n%s", path, page)
		}

	default:
		t.Fatalf("the fixture expects kind %q, which this test does not know", want.Kind)
	}
}

func TestSpecRequestsThroughHTTP(t *testing.T) {
	spec := loadSpec(t)
	srv := startServing(t, freeAddr(t), filepath.Join(t.TempDir(), "ogre.db"))
	createFixtureLinks(t, srv.url, spec)
	_, links := listLinks(t, srv.url)

	for _, req := range spec.Requests {
		t.Run(req.ID+" "+req.Request, func(t *testing.T) {
			if req.Pending != "" {
				if req.UntilThen == nil {
					t.Fatalf("row %s is pending %s but says nothing about until then", req.ID, req.Pending)
				}
				checkRequest(t, srv.url, links, req.Path, *req.UntilThen)
				t.Skipf("pending %s: the PRD outcome needs a later milestone; until then the row answers as checked", req.Pending)
			}
			checkRequest(t, srv.url, links, req.Path, req.Expect)
		})
	}

	if code := srv.stop(); code != 0 {
		t.Errorf("exit code = %d, want 0; output:\n%s", code, srv.stderr)
	}
	// NFR-17: the log names the decision and the link, and nothing a user
	// typed after the name: no value, no raw path, no query.
	log := srv.stderr.String()
	for _, want := range []string{"decision=hit link=reports", "decision=miss_known link=jira", "decision=miss_unknown link=reprots"} {
		if !strings.Contains(log, want) {
			t.Errorf("the log lacks %q:\n%s", want, log)
		}
	}
	for _, typed := range []string{"api-server", "OPS-1234", "ops-1234", "latest", "a%20b", "a b&c", "readme", "Reports", "On-Call"} {
		if strings.Contains(log, typed) {
			t.Errorf("the log holds %q, which a user typed:\n%s", typed, log)
		}
	}
}

func TestSpecSavesThroughHTTP(t *testing.T) {
	spec := loadSpec(t)

	for _, save := range spec.Saves {
		t.Run(save.ID+" "+save.Attempt, func(t *testing.T) {
			// Every row starts from the five fixture links, on a server and
			// a database of its own.
			db := filepath.Join(t.TempDir(), "ogre.db")
			srv := startServing(t, freeAddr(t), db)
			createFixtureLinks(t, srv.url, spec)
			base := srv.url

			switch save.Actor {
			case "owner":
			case "other":
				// Local mode has one user, who may edit everything, so the
				// command cannot refuse anyone. The row is replayed against
				// the same layers put together the way the command does it,
				// over the database the command just wrote, acting as a
				// second user.
				if code := srv.stop(); code != 0 {
					t.Fatalf("exit code = %d; output:\n%s", code, srv.stderr)
				}
				base = serveAsSecondUser(t, db)
			default:
				t.Fatalf("unknown actor %q", save.Actor)
			}

			before, links := listLinks(t, base)
			body := specDraft("", save.Draft.Routes)
			var (
				status int
				raw    []byte
				target apiLink // the link being edited, for an update
			)
			switch save.Action {
			case "create":
				status, _, raw = call(t, http.MethodPost, base+"/-/api/v1/links", body)
			case "update":
				var known bool
				if target, known = links[save.Link]; !known {
					t.Fatalf("the row edits %q, which is not a fixture link", save.Link)
				}
				body.Description = target.Description
				// As an editor would: a route that is still there keeps its
				// ID.
				for i, r := range body.Routes {
					for _, stored := range target.Routes {
						if stored.Pattern == r.Pattern {
							body.Routes[i].ID = stored.ID
						}
					}
				}
				status, _, raw = call(t, http.MethodPatch, base+"/-/api/v1/links/"+itoa(target.ID), updateBody{body, target.Version})
			default:
				t.Fatalf("unknown action %q", save.Action)
			}

			if save.Expect.Saved {
				wantStatus := http.StatusCreated
				if save.Action == "update" {
					wantStatus = http.StatusOK
				}
				if status != wantStatus {
					t.Fatalf("%s: %d %s, want %d", save.Attempt, status, raw, wantStatus)
				}
				var saved apiLink
				strictJSON(t, "the saved link", raw, &saved)
				if saved.Name != save.Expect.Name {
					t.Errorf("saved under the name %q, want %q", saved.Name, save.Expect.Name)
				}
				if save.Action == "update" {
					if saved.ID != target.ID || saved.Version != target.Version+1 {
						t.Errorf("updated link has ID %d version %d, want ID %d version %d", saved.ID, saved.Version, target.ID, target.Version+1)
					}
					for i, r := range body.Routes {
						if r.ID != 0 && saved.Routes[i].ID != r.ID {
							t.Errorf("route %d (%s) changed its ID from %d to %d", i, r.Pattern, r.ID, saved.Routes[i].ID)
						}
					}
				}
				_, links = listLinks(t, base)
				for _, then := range save.Expect.Then {
					checkRequest(t, base, links, then.Path, then.Expect)
				}
				return
			}

			var failure apiError
			strictJSON(t, "the error", raw, &failure)
			switch save.Expect.Error {
			case "validation":
				if status != http.StatusUnprocessableEntity || failure.Error.Code != "link.invalid" {
					t.Fatalf("%s: %d %s, want 422 link.invalid", save.Attempt, status, raw)
				}
				checkSpecProblems(t, failure, save.Draft.Routes, save.Expect.Problems)
			case "link.name_taken":
				if status != http.StatusConflict || failure.Error.Code != "link.name_taken" {
					t.Fatalf("%s: %d %s, want 409 link.name_taken", save.Attempt, status, raw)
				}
				// DIR-4: which link has the name, and that its owner may
				// edit that one instead.
				holder := links[save.Expect.Existing]
				existing, canEdit := failure.Error.Details.Existing, failure.Error.Details.CanEdit
				if existing != nil && existing.Name != save.Expect.Name {
					t.Errorf("the fixture names the draft %q, and the refusal is about %q", save.Expect.Name, existing.Name)
				}
				if existing == nil || existing.Name != save.Expect.Existing || existing.ID != holder.ID || holder.ID == 0 {
					t.Errorf("details.existing = %+v, want the link %q (id %d)", existing, save.Expect.Existing, holder.ID)
				}
				if canEdit == nil || !*canEdit {
					t.Errorf("details.canEdit = %v, want true for the owner of the existing link", canEdit)
				}
			case "link.forbidden":
				if status != http.StatusForbidden || failure.Error.Code != "link.forbidden" {
					t.Fatalf("%s: %d %s, want 403 link.forbidden", save.Attempt, status, raw)
				}
				// PRD 5.7, S5: "the message names the owner and links to
				// go/gh". Both are asked of the refusal itself: its message,
				// for a person, and its details, for a client that builds
				// the link.
				getStatus, _, getRaw := call(t, http.MethodGet, base+"/-/api/v1/links/"+itoa(target.ID), nil)
				var link apiLink
				if getStatus != http.StatusOK {
					t.Fatalf("GET the refused link: %d %s", getStatus, getRaw)
				}
				strictJSON(t, "the refused link", getRaw, &link)
				if link.Name != save.Expect.Name || link.CanEdit {
					t.Errorf("the refused link is %q with canEdit=%v, want %q and false", link.Name, link.CanEdit, save.Expect.Name)
				}
				owner := link.Owner.DisplayName
				if owner == "" || owner == secondUserName {
					t.Fatalf("owner = %+v, want the name of the link's owner", link.Owner)
				}
				message, details := failure.Error.Message, failure.Error.Details
				if linksTo := "go/" + save.Expect.LinksTo; !strings.Contains(message, linksTo) {
					t.Errorf("the message %q does not point at %s", message, linksTo)
				}
				if details.Link == nil || details.Link.Name != save.Expect.LinksTo || details.Link.ID != target.ID {
					t.Errorf("details.link = %+v, want the link %q (id %d)", details.Link, save.Expect.LinksTo, target.ID)
				}
				namesOwner := strings.Contains(message, owner) && details.Owner != nil && details.Owner.DisplayName == owner
				if namesOwner != save.Expect.NamesOwner {
					t.Errorf("the refusal names the owner %q: %v, want %v\nmessage: %s\ndetails.owner: %+v",
						owner, namesOwner, save.Expect.NamesOwner, message, details.Owner)
				}
				if strings.Contains(message, secondUserName) {
					t.Errorf("the message %q names the refused user as if they were the owner", message)
				}
			default:
				t.Fatalf("the fixture expects error %q, which this test does not know", save.Expect.Error)
			}

			// A refused save changes nothing.
			if after, _ := listLinks(t, base); after != before {
				t.Errorf("a refused save changed the links:\nbefore: %s\nafter:  %s", before, after)
			}
			checkRequest(t, base, links, "gh/api-server", specExpect{
				Kind: "hit", Link: "gh", Route: "gh/{repo}",
				Values: map[string]string{"repo": "api-server"}, Destination: "https://github.com/acme/api-server",
			})
		})
	}
}

// checkSpecProblems holds a 422 to the errors the fixture lists: all of them,
// in order. Warnings are not part of the fixture. Each problem must also
// point into the text it is about.
func checkSpecProblems(t *testing.T, failure apiError, routes []specRoute, want []specProblem) {
	t.Helper()
	type located struct {
		route int
		apiProblem
	}
	var got []located
	details := failure.Error.Details
	for _, p := range details.Problems {
		if p.Severity == "error" {
			got = append(got, located{-1, p})
		}
	}
	if len(details.Routes) != len(routes) {
		t.Fatalf("details.routes has %d entries, want one per route sent (%d)", len(details.Routes), len(routes))
	}
	for i, route := range details.Routes {
		for _, p := range route.Problems {
			if p.Severity == "error" {
				got = append(got, located{i, p})
			}
		}
	}
	if len(got) != len(want) {
		t.Fatalf("errors = %+v, want %+v", got, want)
	}
	for i, w := range want {
		g := got[i]
		if g.route != w.Route || g.Field != w.Field || g.Code != w.Code || !strings.Contains(g.Message, w.Mentions) {
			t.Errorf("error %d = route %d, %s, %s, %q; want route %d, %s, %s, mentioning %q",
				i, g.route, g.Field, g.Code, g.Message, w.Route, w.Field, w.Code, w.Mentions)
			continue
		}
		text := routes[w.Route].Pattern
		if w.Field == "destination" {
			text = routes[w.Route].Destination
		}
		// The fixture's texts are ASCII, so UTF-16 offsets are byte offsets.
		if g.Start < 0 || g.End < g.Start || g.End > len(text) {
			t.Errorf("error %d marks %d..%d, outside the %s %q", i, g.Start, g.End, w.Field, text)
		}
	}
}

const secondUserName = "Someone Else"

// serveAsSecondUser serves the database at db the way "ogre serve" does
// (store, use cases, HTTP layer, in that order), except that every request
// acts as a second user who is not an admin and owns nothing. It returns the
// server's base URL. The command must not be running on db.
//
// No Store method can add a user before sign-in exists, so the row is written
// through a connection of its own; importing package store has registered the
// driver.
func serveAsSecondUser(t *testing.T, db string) string {
	t.Helper()
	ctx := context.Background()

	conn, err := sql.Open("sqlite", "file:"+filepath.ToSlash(db)+"?_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)")
	if err != nil {
		t.Fatalf("open %s: %v", db, err)
	}
	res, err := conn.Exec(
		`INSERT INTO users (email, display_name, role, status, created_at) VALUES (?, ?, 'user', 'active', ?)`,
		"someone.else@example.com", secondUserName, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		t.Fatalf("add a second user: %v", err)
	}
	userID, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}

	st, err := store.Open(ctx, db)
	if err != nil {
		t.Fatalf("open the store: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	ts := httptest.NewUnstartedServer(nil)
	addr := ts.Listener.Addr().String()
	directory, err := app.New(ctx, st, app.Options{OwnHosts: httpd.OwnHosts(addr, nil)})
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}
	ts.Config.Handler = httpd.New(directory, httpd.Options{
		Addr:    addr,
		Version: version,
		Actor:   app.Actor{UserID: userID, DisplayName: secondUserName},
	}).Handler()
	ts.Start()
	t.Cleanup(ts.Close)
	return ts.URL
}

// A destination on the Directory itself is refused (PRD 5.4): the command
// tells the use cases which hosts it answers on, --allow-host names included.
// Another port of the same computer is another site and is fine.
func TestServeRefusesDestinationsOnItself(t *testing.T) {
	port, stderr := serveWith(t, "--allow-host", "go,go.example.com:80")
	base := "http://localhost:" + port
	waitUntilHealthy(t, base, stderr)

	otherPort := "1" // never the server's: a free port is far above 1
	tests := []struct {
		name        string
		destination string
		wantCode    string // "" means the link is saved
	}{
		{"its own address", "http://localhost:" + port + "/-/", "destination.directory"},
		{"its own address, another scheme and case", "https://LocalHost:" + port + "/x", "destination.directory"},
		{"the IPv4 loopback", "http://127.0.0.1:" + port + "/x", "destination.directory"},
		{"the IPv6 loopback", "http://[::1]:" + port + "/x", "destination.directory"},
		{"an allowed name on the listen port", "http://go:" + port + "/x", "destination.go_link"},
		{"an allowed name with its own port", "http://go.example.com/x", "destination.directory"},
		// Other spellings of the same address, which a browser rewrites
		// before it connects.
		{"the IPv4 loopback with a part left out", "http://127.1:" + port + "/x", "destination.directory"},
		{"the IPv4 loopback as one number", "http://2130706433:" + port + "/x", "destination.directory"},
		{"the IPv4 loopback in hexadecimal", "http://0x7f.0.0.1:" + port + "/x", "destination.directory"},
		{"the IPv4 loopback in octal", "http://017700000001:" + port + "/x", "destination.directory"},
		{"the port with a leading zero", "http://localhost:0" + port + "/x", "destination.directory"},
		{"the IPv6 loopback written out", "http://[0:0:0:0:0:0:0:1]:" + port + "/x", "destination.directory"},
		{"another port of this computer", "http://localhost:" + otherPort + "/x", ""},
		{"the allowed name on another port", "https://go.example.com/x", ""},
		{"another site", "https://example.com/x", ""},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, _, raw := call(t, http.MethodPost, base+"/-/api/v1/links", draftBody{
				Routes: []draftRoute{{Pattern: "self" + itoa(int64(i)), Destination: tt.destination}},
			})
			if tt.wantCode == "" {
				if status != http.StatusCreated {
					t.Errorf("%s: %d %s, want it saved", tt.destination, status, raw)
				}
				return
			}
			var failure apiError
			strictJSON(t, "the error", raw, &failure)
			if status != http.StatusUnprocessableEntity || len(failure.Error.Details.Routes) != 1 {
				t.Fatalf("%s: %d %s, want 422", tt.destination, status, raw)
			}
			var codes []string
			for _, p := range failure.Error.Details.Routes[0].Problems {
				codes = append(codes, p.Code)
			}
			if !reflect.DeepEqual(codes, []string{tt.wantCode}) {
				t.Errorf("%s: problems %v, want %s", tt.destination, codes, tt.wantCode)
			}
		})
	}

	// An edit is held to the same rule: a saved link cannot be turned onto
	// the Directory afterwards, and following it never comes back here.
	t.Run("an edit onto itself", func(t *testing.T) {
		status, _, raw := call(t, http.MethodPost, base+"/-/api/v1/links", draftBody{
			Routes: []draftRoute{{Pattern: "selfloop", Destination: "https://example.com/x"}},
		})
		var link apiLink
		if status != http.StatusCreated {
			t.Fatalf("create: %d %s", status, raw)
		}
		strictJSON(t, "the link", raw, &link)
		for _, destination := range []string{"http://localhost:" + port + "/selfloop", "http://127.1:" + port + "/selfloop"} {
			status, _, raw := call(t, http.MethodPatch, base+"/-/api/v1/links/"+itoa(link.ID), map[string]any{
				"description": "", "version": link.Version,
				"routes": []draftRoute{{ID: link.Routes[0].ID, Pattern: "selfloop", Destination: destination}},
			})
			var failure apiError
			strictJSON(t, "the error", raw, &failure)
			if status != http.StatusUnprocessableEntity || len(failure.Error.Details.Routes) != 1 ||
				len(failure.Error.Details.Routes[0].Problems) != 1 ||
				failure.Error.Details.Routes[0].Problems[0].Code != "destination.directory" {
				t.Errorf("PATCH onto %s: %d %s, want 422 destination.directory", destination, status, raw)
			}
		}
		client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		resp, err := client.Get(base + "/selfloop")
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if got := resp.Header.Get("Location"); resp.StatusCode != http.StatusFound || got != "https://example.com/x" {
			t.Errorf("GET /selfloop = %d to %q, want the destination it was saved with", resp.StatusCode, got)
		}
	})
}

// NFR-8 through the real server: the costliest link that can be saved, asked
// the costliest request, as a go link and through the resolve endpoint. Both
// answer at once (it was 5 s of CPU per request), a link beyond the budget is
// not saved, and a page of another site cannot ask at all.
func TestServeBoundsRegexWork(t *testing.T) {
	port, stderr := serveWith(t)
	base := "http://localhost:" + port
	waitUntilHealthy(t, base, stderr)

	routes := func(n int) []draftRoute {
		var out []draftRoute
		for i := range n {
			out = append(out, draftRoute{
				Pattern:     "slow/{v:regex((.*){1," + itoa(int64(196-i)) + "}b)}",
				Destination: "https://example.com/?v={v}",
			})
		}
		return out
	}
	status, _, raw := call(t, http.MethodPost, base+"/-/api/v1/links", draftBody{Routes: routes(5)})
	var failure apiError
	strictJSON(t, "the error", raw, &failure)
	if status != http.StatusUnprocessableEntity || len(failure.Error.Details.Problems) != 1 ||
		failure.Error.Details.Problems[0].Code != "pattern.regex_too_complex" || failure.Error.Details.Problems[0].Field != "link" {
		t.Fatalf("a link over the regex budget: %d %s, want 422 pattern.regex_too_complex on the link", status, raw)
	}
	if status, _, raw := call(t, http.MethodPost, base+"/-/api/v1/links", draftBody{Routes: routes(4)}); status != http.StatusCreated {
		t.Fatalf("the largest link within the budget: %d %s", status, raw)
	}

	value := strings.Repeat("a", 256) // the longest value a regular expression looks at
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for _, target := range []string{base + "/slow/" + value, base + "/-/api/v1/resolve?path=slow/" + value} {
		best := time.Hour
		for range 3 {
			start := time.Now()
			resp, err := client.Get(target)
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if took := time.Since(start); took < best {
				best = took
			}
			// The miss page is HTML, where the apostrophe is an entity.
			if !strings.Contains(visibleText(body), "doesn't accept") {
				t.Fatalf("GET %s = %d %s, want a miss that explains itself", target[len(base):len(base)+24], resp.StatusCode, body)
			}
		}
		// Milliseconds in practice; the bound allows for the race detector
		// and a busy machine, and is far from the seconds this used to take.
		if best > time.Second {
			t.Errorf("the costliest request took %v on %s...", best, target[len(base):len(base)+24])
		}
	}

	// A script on another site cannot set the work in motion.
	req, _ := http.NewRequest(http.MethodGet, base+"/-/api/v1/resolve?path=slow/"+value, nil)
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	req.Header.Set("Sec-Fetch-Mode", "no-cors")
	req.Header.Set("Sec-Fetch-Dest", "empty")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden || !strings.Contains(string(body), "request.cross_site") {
		t.Errorf("a cross-site no-cors fetch of the resolve endpoint = %d %s, want 403 request.cross_site", resp.StatusCode, body)
	}
}

// waitUntilHealthy waits for a server started by the test to answer.
func waitUntilHealthy(t *testing.T, base string, stderr *lockedBuffer) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		resp, err := http.Get(base + "/-/healthz")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("ogre did not answer %s/-/healthz within 10s; its output:\n%s", base, stderr)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// The validate example of plan section 3.5, word for word, against the
// running server: an unfinished pattern already yields its name, its tokens,
// the problem with its position, and what the finished variable accepts.
// Beyond the plan's example the answer has name.canEdit and the link-level
// problems, both always present.
func TestValidateAnswersThePlanExample(t *testing.T) {
	srv := startServing(t, freeAddr(t), filepath.Join(t.TempDir(), "ogre.db"))

	const request = `{ "linkId": null,
  "routes": [ { "pattern": "reports/{month:int}/{day",
                "destination": "https://reports.example.com/daily?month={month}&day={day}",
                "samples": { "month": ["10", "x"] } } ] }`
	const want = `{ "valid": false,
  "name": { "value": "reports", "available": true, "existing": null, "canEdit": false },
  "routes": [ {
    "tokens": [ { "kind": "literal", "start": 0, "end": 7 },
                { "kind": "variable", "start": 8, "end": 19, "name": "month", "type": "int" } ],
    "problems": [ { "severity": "error", "field": "pattern", "code": "pattern.unclosed_variable",
                    "start": 20, "end": 24, "message": "This variable is missing its closing }." } ],
    "samples": { "month": { "10": true, "x": false } } } ],
  "problems": [] }`

	resp, err := http.Post(srv.url+"/-/api/v1/links/validate", "application/json", strings.NewReader(request))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("validate: %d %s", resp.StatusCode, raw)
	}
	var got, expected any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	if err := json.Unmarshal([]byte(want), &expected); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, expected) {
		t.Errorf("validate answered\n%s\nwant\n%s", raw, want)
	}

	// Nothing was saved by asking.
	if _, links := listLinks(t, srv.url); len(links) != 0 {
		t.Errorf("validating created %d links", len(links))
	}
}

var (
	inlineTag = regexp.MustCompile(`(?i)</?(?:a|span|bdi|strong|code|kbd)\b[^>]*>`)
	anyTag    = regexp.MustCompile(`(?s)<[^>]*>`)
	spaces    = regexp.MustCompile(`\s+`)
)

// visibleText is the text of a page as a browser shows it, near enough for
// these checks: inline tags dropped, other tags as spaces, entities decoded,
// white space collapsed.
func visibleText(page []byte) string {
	text := inlineTag.ReplaceAllString(string(page), "")
	text = html.UnescapeString(anyTag.ReplaceAllString(text, " "))
	return strings.TrimSpace(spaces.ReplaceAllString(text, " "))
}
