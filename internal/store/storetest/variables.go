package storetest

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/tfrydrychewicz/ogre/internal/linkmodel"
	"github.com/tfrydrychewicz/ogre/internal/store"
)

// This file holds the cases about a route's variables: the description and
// example of each are stored, and which variables a route has always follows
// from its pattern.

// meta is shorthand for one variable's metadata.
func meta(name, description, example string) linkmodel.VariableMeta {
	return linkmodel.VariableMeta{Name: name, Description: description, Example: example}
}

func testVariablesRoundTrip(t *testing.T, st store.Store) {
	// Text that would expose quoting, trimming or re-encoding on the way.
	const (
		unicodeText = "Dépôt — リポジトリ 🚀 עברית"
		awkward     = "it's \"quoted\"; DROP TABLE variables;--\n  second line\t"
	)
	created := mustCreate(t, st, linkmodel.Link{Name: "gh", Description: "GitHub shortcuts", Routes: []linkmodel.Route{
		{
			Pattern: "gh/{repo}/pr/{num:int}", Destination: "https://github.com/acme/{repo}/pull/{num}",
			Variables: []linkmodel.VariableMeta{meta("repo", unicodeText, "ogre"), meta("num", awkward, "42")},
		},
		{
			// Only a description, only an example, and neither.
			Pattern: "gh/{org}/{repo}/{branch}", Destination: "https://github.com/{org}/{repo}/tree/{branch}",
			Variables: []linkmodel.VariableMeta{meta("org", "Organisation", ""), meta("repo", "", "ogre"), meta("branch", "", "")},
		},
		// No variables at all.
		route("gh", "https://github.com/acme"),
	}})

	want := [][]linkmodel.VariableMeta{
		{meta("repo", unicodeText, "ogre"), meta("num", awkward, "42")},
		{meta("org", "Organisation", ""), meta("repo", "", "ogre"), meta("branch", "", "")},
		{},
	}
	for i, w := range want {
		got := created.Routes[i].Variables
		if got == nil {
			t.Errorf("route %d has nil Variables, want a list", i)
		}
		if !slices.Equal(got, w) {
			t.Errorf("route %d variables = %q, want %q", i, got, w)
		}
	}
	// Read back by both paths, byte for byte.
	assertStored(t, st, created)

	// The same name in two routes is two variables: each keeps its own text.
	if a, b := created.Routes[0].Variables[0], created.Routes[1].Variables[1]; a.Name != "repo" || b.Name != "repo" || a == b {
		t.Errorf("the two {repo} variables came back as %q and %q, want each with its own metadata", a, b)
	}
}

// testVariablesFollowThePattern: whatever the caller hands over, a route's
// Variables come back with one entry per variable of the pattern, in pattern
// order. Entries may be missing or out of order; the pattern decides.
func testVariablesFollowThePattern(t *testing.T, st store.Store) {
	tests := []struct {
		name  string
		given []linkmodel.VariableMeta
		want  []linkmodel.VariableMeta
	}{
		{"none given", nil,
			[]linkmodel.VariableMeta{meta("year", "", ""), meta("month", "", ""), meta("day", "", "")}},
		{"empty list given", []linkmodel.VariableMeta{},
			[]linkmodel.VariableMeta{meta("year", "", ""), meta("month", "", ""), meta("day", "", "")}},
		{"one of three", []linkmodel.VariableMeta{meta("month", "Month", "10")},
			[]linkmodel.VariableMeta{meta("year", "", ""), meta("month", "Month", "10"), meta("day", "", "")}},
		{"out of order", []linkmodel.VariableMeta{meta("day", "Day", "13"), meta("year", "Year", "2026"), meta("month", "Month", "10")},
			[]linkmodel.VariableMeta{meta("year", "Year", "2026"), meta("month", "Month", "10"), meta("day", "Day", "13")}},
		{"all empty", []linkmodel.VariableMeta{meta("year", "", ""), meta("month", "", ""), meta("day", "", "")},
			[]linkmodel.VariableMeta{meta("year", "", ""), meta("month", "", ""), meta("day", "", "")}},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name := "reports" + string(rune('a'+i))
			given := slices.Clone(tt.given)
			created := mustCreate(t, st, linkmodel.Link{Name: name, Routes: []linkmodel.Route{{
				Pattern:     name + "/{year:int}/{month:int}/{day:int}",
				Destination: "https://reports.example.com/{year}/{month}/{day}",
				Variables:   tt.given,
			}}})
			if got := created.Routes[0].Variables; !slices.Equal(got, tt.want) {
				t.Errorf("CreateLink returned variables %q, want %q", got, tt.want)
			}
			assertStored(t, st, created)
			if !slices.Equal(tt.given, given) {
				t.Errorf("CreateLink reordered or changed the caller's Variables: %q, was %q", tt.given, given)
			}
		})
	}
}

// testVariablesAreReplacedByAnUpdate: an update replaces a route's metadata
// as it replaces the route's other fields; nothing of the old set survives
// unless it is sent again.
func testVariablesAreReplacedByAnUpdate(t *testing.T, st store.Store) {
	created := mustCreate(t, st, linkmodel.Link{Name: "gh", Routes: []linkmodel.Route{
		{
			Pattern: "gh/{repo}/pr/{num:int}", Destination: "https://github.com/acme/{repo}/pull/{num}",
			Variables: []linkmodel.VariableMeta{meta("repo", "Repository", "ogre"), meta("num", "Pull request", "42")},
		},
		{
			Pattern: "gh/{repo}", Destination: "https://github.com/acme/{repo}",
			Variables: []linkmodel.VariableMeta{meta("repo", "Repository", "ogre")},
		},
		{
			Pattern: "gh/{repo}/issues/{num:int}", Destination: "https://github.com/acme/{repo}/issues/{num}",
			Variables: []linkmodel.VariableMeta{meta("repo", "Dropped with its route", "x")},
		},
	}})
	pr, repo := created.Routes[0], created.Routes[1]

	updated := mustUpdate(t, st, linkmodel.Link{ID: created.ID, Name: "gh", Routes: []linkmodel.Route{
		// Kept route, variable renamed in the pattern and one description
		// changed. "repo" no longer exists here, and "num" is sent without
		// its example, which therefore goes.
		{
			ID: pr.ID, Pattern: "gh/{project}/pr/{num:int}", Destination: "https://github.com/acme/{project}/pull/{num}",
			Variables: []linkmodel.VariableMeta{meta("num", "PR number", ""), meta("project", "Project", "ogre")},
		},
		// A new route with metadata of its own.
		{
			Pattern: "gh/{repo}/wiki/{page}", Destination: "https://github.com/acme/{repo}/wiki/{page}",
			Variables: []linkmodel.VariableMeta{meta("page", "Wiki page", "Home")},
		},
		// Kept route sent without metadata: its description and example go.
		{ID: repo.ID, Pattern: "gh/{repo}", Destination: "https://github.com/acme/{repo}"},
	}}, created.Version)

	want := [][]linkmodel.VariableMeta{
		{meta("project", "Project", "ogre"), meta("num", "PR number", "")},
		{meta("repo", "", ""), meta("page", "Wiki page", "Home")},
		{meta("repo", "", "")},
	}
	if len(updated.Routes) != len(want) {
		t.Fatalf("got %d routes, want %d", len(updated.Routes), len(want))
	}
	for i, w := range want {
		if got := updated.Routes[i].Variables; !slices.Equal(got, w) {
			t.Errorf("route %d variables = %q, want %q", i, got, w)
		}
	}
	if updated.Routes[0].ID != pr.ID || updated.Routes[2].ID != repo.ID {
		t.Errorf("kept routes changed ids: %+v", updated.Routes)
	}
	assertStored(t, st, updated)

	// The history shows the metadata of each version.
	checkRevisions(t, st, created.ID, created.OwnerID, []wantRevision{
		{store.RevisionCreate, created}, {store.RevisionUpdate, updated},
	})

	// Putting the old pattern back does not bring the old text back.
	restored := mustUpdate(t, st, linkmodel.Link{ID: created.ID, Name: "gh", Routes: []linkmodel.Route{
		{ID: pr.ID, Pattern: "gh/{repo}/pr/{num:int}", Destination: "https://github.com/acme/{repo}/pull/{num}"},
	}}, updated.Version)
	if got, w := restored.Routes[0].Variables, []linkmodel.VariableMeta{meta("repo", "", ""), meta("num", "", "")}; !slices.Equal(got, w) {
		t.Errorf("variables after restoring the pattern = %q, want %q", got, w)
	}
	assertStored(t, st, restored)
}

// testVariablesMustBelongToThePattern: metadata for a name the pattern does
// not declare could be stored but never read back. The store refuses it, so
// nothing a caller hands over is dropped without a word.
func testVariablesMustBelongToThePattern(t *testing.T, st store.Store) {
	ctx := context.Background()
	editor := localUserID(t, st)
	created := mustCreate(t, st, linkmodel.Link{Name: "gh", Routes: []linkmodel.Route{{
		Pattern: "gh/{repo}", Destination: "https://github.com/acme/{repo}",
		Variables: []linkmodel.VariableMeta{meta("repo", "Repository", "ogre")},
	}}})

	tests := []struct {
		name      string
		pattern   string
		variables []linkmodel.VariableMeta
	}{
		{"unknown name", "{repo}", []linkmodel.VariableMeta{meta("repo", "Repository", ""), meta("branch", "Branch", "")}},
		{"unknown name with empty text", "{repo}", []linkmodel.VariableMeta{meta("branch", "", "")}},
		{"name in another case", "{repo}", []linkmodel.VariableMeta{meta("Repo", "Repository", "")}},
		{"same name twice", "{repo}", []linkmodel.VariableMeta{meta("repo", "one", ""), meta("repo", "two", "")}},
		{"pattern without variables", "all", []linkmodel.VariableMeta{meta("repo", "Repository", "")}},
		{"empty name", "{repo}", []linkmodel.VariableMeta{meta("", "Nameless", "")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l, err := st.CreateLink(ctx, linkmodel.Link{Name: "fresh", OwnerID: editor, Routes: []linkmodel.Route{
				route("fresh", "https://example.com/"),
				{Pattern: "fresh/" + tt.pattern, Destination: "https://example.com/x", Variables: tt.variables},
			}}, editor)
			if !errors.Is(err, store.ErrVariableMismatch) {
				t.Errorf("CreateLink = %+v, %v; want ErrVariableMismatch", l, err)
			}

			l, err = st.UpdateLink(ctx, linkmodel.Link{ID: created.ID, Name: "gh", Description: "changed", Routes: []linkmodel.Route{
				{ID: created.Routes[0].ID, Pattern: "gh/" + tt.pattern, Destination: "https://example.com/x", Variables: tt.variables},
			}}, created.Version, editor)
			if !errors.Is(err, store.ErrVariableMismatch) {
				t.Errorf("UpdateLink = %+v, %v; want ErrVariableMismatch", l, err)
			}

			if got := mustList(t, st); len(got) != 1 {
				t.Errorf("the store holds %d links, want only the one created", len(got))
			}
			assertStored(t, st, created)
		})
	}
	checkRevisions(t, st, created.ID, editor, []wantRevision{{store.RevisionCreate, created}})
}

// testSameShapeIsRefused: two routes of one link whose patterns differ only
// in the names of their variables would match exactly the same requests (PRD
// 5.5). The link model reports that to the user before saving; the store is
// the last line and must refuse it too, whole.
func testSameShapeIsRefused(t *testing.T, st store.Store) {
	ctx := context.Background()
	editor := localUserID(t, st)

	l, err := st.CreateLink(ctx, linkmodel.Link{Name: "gh", OwnerID: editor, Routes: []linkmodel.Route{
		route("gh/{repo}", "https://github.com/acme/{repo}"),
		route("gh/{project}", "https://github.com/acme/{project}"),
	}}, editor)
	if err == nil {
		t.Fatalf("CreateLink with gh/{repo} and gh/{project} succeeded: %+v", l)
	}
	if got := mustList(t, st); len(got) != 0 {
		t.Fatalf("a refused link was stored: %+v", got)
	}

	// A different type is a different shape: these two can live together.
	created := mustCreate(t, st, linkmodel.Link{Name: "gh", Description: "before", Routes: []linkmodel.Route{
		{Pattern: "gh/{repo}", Destination: "https://github.com/acme/{repo}",
			Variables: []linkmodel.VariableMeta{meta("repo", "Repository", "ogre")}},
		route("gh/{num:int}", "https://github.com/acme/ogre/pull/{num}"),
	}})

	l, err = st.UpdateLink(ctx, linkmodel.Link{ID: created.ID, Name: "gh", Description: "after", Routes: []linkmodel.Route{
		{ID: created.Routes[0].ID, Pattern: "gh/{repo}", Destination: "https://example.com/changed/{repo}"},
		{ID: created.Routes[1].ID, Pattern: "gh/{num:int}", Destination: "https://github.com/acme/ogre/pull/{num}"},
		route("gh/{id:int}", "https://github.com/acme/ogre/issues/{id}"),
	}}, created.Version, editor)
	if err == nil {
		t.Fatalf("UpdateLink adding gh/{id:int} beside gh/{num:int} succeeded: %+v", l)
	}
	for _, wrong := range []error{store.ErrNameTaken, store.ErrVersionConflict, store.ErrNotFound, store.ErrRouteNotInLink} {
		if errors.Is(err, wrong) {
			t.Errorf("error = %v; a repeated shape must not be reported as %v", err, wrong)
		}
	}
	// Nothing of the refused update stuck: not the description, not the
	// first route's new destination, not the loss of its metadata.
	assertStored(t, st, created)
	checkRevisions(t, st, created.ID, editor, []wantRevision{{store.RevisionCreate, created}})

	// Renaming a variable of a kept route keeps its shape, and is fine.
	renamed := mustUpdate(t, st, linkmodel.Link{ID: created.ID, Name: "gh", Routes: []linkmodel.Route{
		{ID: created.Routes[0].ID, Pattern: "gh/{project}", Destination: "https://github.com/acme/{project}"},
		{ID: created.Routes[1].ID, Pattern: "gh/{number:int}", Destination: "https://github.com/acme/ogre/pull/{number}"},
	}}, created.Version)
	assertStored(t, st, renamed)
}
