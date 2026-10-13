package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/tfrydrychewicz/ogre/internal/linkmodel"
)

// The worked examples of PRD 5.7 are data, kept once, in the link model's
// testdata (see its README.txt). This file replays them through the use
// cases over the real SQLite store: the fixture links are created with
// CreateLink, every request goes through Resolve, and every save attempt
// through CreateLink or UpdateLink, including the rows that only this level
// can decide (a taken name, a user who may not edit).

const specPath = "../linkmodel/testdata/spec.json"

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

// loadSpec reads the fixture. A field this file does not know is an error,
// so a new kind of row cannot be skipped by accident.
func loadSpec(t testing.TB) specFile {
	t.Helper()
	raw, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatalf("read the spec fixture: %v", err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var spec specFile
	if err := dec.Decode(&spec); err != nil {
		t.Fatalf("decode %s: %v", specPath, err)
	}
	if spec.Format != "ogre-spec" || spec.Version != 1 {
		t.Fatalf("%s is %q version %d, want ogre-spec version 1", specPath, spec.Format, spec.Version)
	}
	if len(spec.Links) == 0 || len(spec.Requests) == 0 || len(spec.Saves) == 0 {
		t.Fatalf("%s has %d links, %d requests, %d saves", specPath, len(spec.Links), len(spec.Requests), len(spec.Saves))
	}
	return spec
}

// newSpecApp returns an App over a new SQLite store holding the fixture
// links, each saved through CreateLink by the owner.
func newSpecApp(t testing.TB, spec specFile) (a *App, st *spyStore, owner Actor) {
	t.Helper()
	a, st = newApp(t)
	owner, err := a.LocalActor(context.Background())
	if err != nil {
		t.Fatalf("LocalActor: %v", err)
	}
	for _, l := range spec.Links {
		saved, err := a.CreateLink(context.Background(), owner, specDraft(l.Description, l.Routes))
		if err != nil {
			t.Fatalf("create the fixture link %s: %v", l.Name, err)
		}
		if saved.Name != l.Name || len(saved.Routes) != len(l.Routes) {
			t.Fatalf("the fixture link %s was saved as %+v", l.Name, saved)
		}
		// The fixture writes patterns in their canonical form: what was
		// sent is what is stored, and what a hit will name.
		for i, r := range l.Routes {
			if saved.Routes[i].Pattern != r.Pattern || saved.Routes[i].Destination != r.Destination {
				t.Fatalf("%s route %d was saved as %+v, want %+v", l.Name, i, saved.Routes[i], r)
			}
		}
	}
	return a, st, owner
}

func specDraft(description string, routes []specRoute) Draft {
	d := Draft{Description: description}
	for _, r := range routes {
		d.Routes = append(d.Routes, DraftRoute{Pattern: r.Pattern, Destination: r.Destination})
	}
	return d
}

// checkDecision holds a decision to one "expect" object of the fixture.
func checkDecision(t *testing.T, d Decision, want specExpect) {
	t.Helper()
	if got := d.Kind.String(); got != want.Kind {
		t.Fatalf("kind = %s, want %s (decision %+v)", got, want.Kind, d)
	}
	switch want.Kind {
	case "home":
		if !reflect.DeepEqual(d, Decision{Kind: DecisionHome}) {
			t.Errorf("home decision carries more than its kind: %+v", d)
		}
	case "hit":
		if d.Link == nil || d.Link.Name != want.Link || d.LinkName != want.Link {
			t.Errorf("link = %+v (LinkName %q), want %q", d.Link, d.LinkName, want.Link)
		}
		if d.Route == nil || d.Route.Pattern != want.Route {
			t.Errorf("route = %+v, want the one with pattern %q", d.Route, want.Route)
		}
		if len(d.Values) != len(want.Values) || (len(want.Values) > 0 && !reflect.DeepEqual(d.Values, want.Values)) {
			t.Errorf("values = %v, want %v", d.Values, want.Values)
		}
		if d.Destination != want.Destination {
			t.Errorf("destination = %q, want %q", d.Destination, want.Destination)
		}
		if d.Reasons != nil || d.Suggestions != nil || d.RequestedName != "" || d.Err != nil {
			t.Errorf("a hit carries what belongs to a miss: %+v", d)
		}
	case "miss_known":
		if d.Link == nil || d.Link.Name != want.Link || d.LinkName != want.Link {
			t.Errorf("link = %+v (LinkName %q), want %q", d.Link, d.LinkName, want.Link)
		}
		if d.Destination != "" || d.Route != nil || d.Values != nil || d.Suggestions != nil {
			t.Errorf("a miss carries what belongs to a hit: %+v", d)
		}
		if len(d.Reasons) != len(want.Reasons) || len(d.Reasons) != len(d.Link.Routes) {
			t.Fatalf("%d reasons, want %d: one per route (%+v)", len(d.Reasons), len(want.Reasons), d.Reasons)
		}
		for i, w := range want.Reasons {
			r := d.Reasons[i]
			if r.Pattern != w.Route || r.Code != w.Code || r.Variable != w.Variable || r.Value != w.Value {
				t.Errorf("reason %d = {%s %s %q %q}, want {%s %s %q %q}", i,
					r.Pattern, r.Code, r.Variable, r.Value, w.Route, w.Code, w.Variable, w.Value)
			}
			if r.RouteID != d.Link.Routes[i].ID {
				t.Errorf("reason %d is about route %d, want %d", i, r.RouteID, d.Link.Routes[i].ID)
			}
			// MISS-2: the sentence names the variable, the value and what
			// the variable accepts.
			if r.Message == "" || !strings.Contains(r.Message, w.Variable) || !strings.Contains(r.Message, w.Value) {
				t.Errorf("reason %d message %q does not mention %q and %q", i, r.Message, w.Variable, w.Value)
			}
			if w.Variable != "" && (r.Accepts == "" || !strings.Contains(r.Message, r.Accepts)) {
				t.Errorf("reason %d does not say what %s accepts: %+v", i, w.Variable, r)
			}
		}
	case "miss_unknown":
		if d.RequestedName != want.RequestedName || d.LinkName != want.RequestedName {
			t.Errorf("requested name = %q (LinkName %q), want %q", d.RequestedName, d.LinkName, want.RequestedName)
		}
		if d.Link != nil || d.Route != nil || d.Destination != "" || d.Values != nil || d.Reasons != nil {
			t.Errorf("an unknown-name miss carries a link: %+v", d)
		}
		var wantSuggestions []Suggestion
		for _, s := range want.Suggestions {
			wantSuggestions = append(wantSuggestions, Suggestion{
				Name: s.Name, Path: s.Path, Edits: s.Edits, Resolves: s.Resolves,
				Destination: s.Destination, Description: s.Description,
			})
		}
		if !reflect.DeepEqual(bare(t, d.Suggestions), wantSuggestions) {
			t.Errorf("suggestions = %+v, want %+v", d.Suggestions, wantSuggestions)
		}
	default:
		t.Fatalf("the fixture expects kind %q, which this test does not know", want.Kind)
	}
}

func TestSpecRequests(t *testing.T) {
	spec := loadSpec(t)
	a, st, _ := newSpecApp(t, spec)
	st.calls.Store(0)

	for _, req := range spec.Requests {
		t.Run(req.ID+" "+req.Request, func(t *testing.T) {
			// The resolver receives the path with its leading slash.
			d := a.Resolve("/" + req.Path)
			if req.Pending != "" {
				if req.UntilThen == nil {
					t.Fatalf("row %s is pending %s but says nothing about until then", req.ID, req.Pending)
				}
				checkDecision(t, d, *req.UntilThen)
				t.Skipf("the PRD outcome needs %s, which is a later milestone; until then the row answers as checked", req.Pending)
			}
			checkDecision(t, d, req.Expect)
			// Without the slash it is the same request.
			checkDecision(t, a.Resolve(req.Path), req.Expect)
		})
	}
	if got := st.calls.Load(); got != 0 {
		t.Errorf("resolving the fixture requests made %d store calls, want 0", got)
	}
}

func TestSpecSaves(t *testing.T) {
	spec := loadSpec(t)
	ctx := context.Background()

	for _, save := range spec.Saves {
		t.Run(save.ID+" "+save.Attempt, func(t *testing.T) {
			// Every row starts from the five fixture links.
			a, st, owner := newSpecApp(t, spec)
			before := dump(t, st)

			actor := owner
			switch save.Actor {
			case "owner":
			case "other":
				actor = st.addUser(t, "Someone Else")
			default:
				t.Fatalf("unknown actor %q", save.Actor)
			}

			draft := specDraft("", save.Draft.Routes)
			var (
				saved  linkmodel.Link
				target linkmodel.Link // the link being edited, for an update
				err    error
			)
			switch save.Action {
			case "create":
				saved, err = a.CreateLink(ctx, actor, draft)
			case "update":
				var ok bool
				if target, ok = a.index.Load().Lookup(save.Link); !ok {
					t.Fatalf("the row edits %q, which is not a fixture link", save.Link)
				}
				draft.Description = target.Description
				// As an editor would: a route that is still there keeps
				// its ID.
				for i, r := range draft.Routes {
					for _, stored := range target.Routes {
						if stored.Pattern == r.Pattern {
							draft.Routes[i].ID = stored.ID
						}
					}
				}
				saved, err = a.UpdateLink(ctx, actor, target.ID, draft, target.Version)
			default:
				t.Fatalf("unknown action %q", save.Action)
			}

			if save.Expect.Saved {
				if err != nil {
					t.Fatalf("%s: %v, want it saved", save.Attempt, err)
				}
				if saved.Name != save.Expect.Name {
					t.Errorf("saved under the name %q, want %q", saved.Name, save.Expect.Name)
				}
				if save.Action == "update" {
					if saved.ID != target.ID || saved.Version != target.Version+1 {
						t.Errorf("updated link has ID %d version %d, want ID %d version %d",
							saved.ID, saved.Version, target.ID, target.Version+1)
					}
					for i, r := range draft.Routes {
						if r.ID != 0 && saved.Routes[i].ID != r.ID {
							t.Errorf("route %d (%s) changed its ID from %d to %d", i, r.Pattern, r.ID, saved.Routes[i].ID)
						}
					}
				}
				for _, then := range save.Expect.Then {
					checkDecision(t, a.Resolve("/"+then.Path), then.Expect)
				}
				return
			}

			if err == nil {
				t.Fatalf("%s was saved as %+v, want %s", save.Attempt, saved, save.Expect.Error)
			}
			switch save.Expect.Error {
			case "validation":
				var invalid *ValidationError
				if !errors.As(err, &invalid) {
					t.Fatalf("error = %v, want a *ValidationError", err)
				}
				checkSpecProblems(t, invalid, save.Expect.Problems)
			case "link.name_taken":
				var taken *NameTakenError
				if !errors.As(err, &taken) || !errors.Is(err, ErrNameTaken) {
					t.Fatalf("error = %v, want a *NameTakenError", err)
				}
				holder, _ := a.index.Load().Lookup(save.Expect.Existing)
				if save.Expect.Name != taken.Existing.Name {
					t.Errorf("the draft's name is %q in the fixture, and the refusal is about %q", save.Expect.Name, taken.Existing.Name)
				}
				if taken.Existing.Name != save.Expect.Existing || taken.Existing.ID != holder.ID || holder.ID == 0 {
					t.Errorf("the name is said to be taken by %+v, want the link %q", taken.Existing, save.Expect.Existing)
				}
				// DIR-4: the owner is told they can edit that link instead.
				if !taken.Editable {
					t.Error("Editable = false for the owner of the existing link")
				}
			case "link.forbidden":
				// PRD 5.7, S5: "the message names the owner and links to
				// go/gh". The refusal itself carries both, so whoever words
				// the message needs nothing else.
				var forbidden *ForbiddenError
				if !errors.As(err, &forbidden) || !errors.Is(err, ErrForbidden) {
					t.Fatalf("error = %v, want a *ForbiddenError", err)
				}
				if forbidden.Link.Name != save.Expect.LinksTo || forbidden.Link.Name != save.Expect.Name || forbidden.Link.ID != target.ID {
					t.Errorf("the refusal is about %+v, want the link %q", forbidden.Link, save.Expect.LinksTo)
				}
				owner, _ := a.LocalActor(ctx) // the fixture's links belong to the built-in user
				if named := forbidden.Owner != ""; named != save.Expect.NamesOwner {
					t.Errorf("the refusal names the owner: %v (%q), want %v", named, forbidden.Owner, save.Expect.NamesOwner)
				}
				if save.Expect.NamesOwner && (forbidden.Owner != owner.DisplayName || forbidden.Owner == actor.DisplayName) {
					t.Errorf("Owner = %q, want the link's owner %q", forbidden.Owner, owner.DisplayName)
				}
				if a.CanEdit(actor, forbidden.Link) {
					t.Error("CanEdit says the refused user may edit the link")
				}
			default:
				t.Fatalf("the fixture expects error %q, which this test does not know", save.Expect.Error)
			}

			// A refused save changes nothing, in the store or in what
			// resolves.
			if after := dump(t, st); after != before {
				t.Errorf("a refused save changed the store:\nbefore: %s\nafter:  %s", before, after)
			}
			for _, l := range spec.Links {
				if indexed, ok := a.index.Load().Lookup(l.Name); !ok || len(indexed.Routes) != len(l.Routes) || indexed.Version != 1 {
					t.Errorf("after a refused save the index has %+v for %s", indexed, l.Name)
				}
			}
		})
	}
}

// checkSpecProblems holds a ValidationError to the errors the fixture lists:
// all of them, in order. Warnings are not part of the fixture.
func checkSpecProblems(t *testing.T, invalid *ValidationError, want []specProblem) {
	t.Helper()
	type located struct {
		route int
		linkmodel.Problem
	}
	var got []located
	for _, p := range invalid.Problems {
		if p.Severity == linkmodel.SeverityError {
			got = append(got, located{-1, p})
		}
	}
	for i, route := range invalid.Routes {
		for _, p := range route {
			if p.Severity == linkmodel.SeverityError {
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
		}
	}
}

// dump is everything the store holds about links, as text: every link with
// its routes and variables, and every revision of every link ID that was
// ever handed out. Two equal dumps mean nothing was written in between.
func dump(t testing.TB, st *spyStore) string {
	t.Helper()
	links, err := st.SQLite.ListLinks(context.Background())
	if err != nil {
		t.Fatalf("ListLinks: %v", err)
	}
	var maxID int64
	for _, l := range links {
		maxID = max(maxID, l.ID)
	}
	// IDs are never reused, so a link created and deleted in between would
	// have taken an ID above every one seen here; look a little further.
	var b strings.Builder
	for id := int64(1); id <= maxID+3; id++ {
		for _, r := range st.revisions(t, id) {
			b.WriteString(r.Action + " " + r.At.String() + " " + r.Snapshot + "\n")
		}
	}
	raw, err := json.Marshal(links)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw) + "\n" + b.String()
}
