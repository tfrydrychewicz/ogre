package perf

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/tfrydrychewicz/ogre/internal/app"
	"github.com/tfrydrychewicz/ogre/internal/linkmodel"
	"github.com/tfrydrychewicz/ogre/internal/store"
)

// seeded generates n links into a new database under t.TempDir and returns
// them with the database's path.
func seeded(t *testing.T, n int) (*Generated, string) {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "perf.db")
	owner, err := LocalOwner(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	gen, err := Generate(n, 1, owner)
	if err != nil {
		t.Fatal(err)
	}
	if err := Seed(ctx, path, gen.Links); err != nil {
		t.Fatal(err)
	}
	return gen, path
}

// TestSeedMatchesStore is what makes the bulk path of Seed trustworthy: the
// same links saved one by one through the store, as the product saves them,
// must leave the same links and the same history behind. When a migration
// adds a column or a table that CreateLink fills, this fails until Seed fills
// it too.
func TestSeedMatchesStore(t *testing.T) {
	ctx := context.Background()
	const n = 60 // enough for every kind of link; each store save syncs to disk

	gen, bulkPath := seeded(t, n)

	viaStore := filepath.Join(t.TempDir(), "store.db")
	st, err := store.Open(ctx, viaStore)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	u, err := st.LocalUser(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range gen.Links {
		if _, err := st.CreateLink(ctx, l, u.ID); err != nil {
			t.Fatalf("store.CreateLink(%q): %v", l.Name, err)
		}
	}

	bulk, err := store.Open(ctx, bulkPath)
	if err != nil {
		t.Fatal(err)
	}
	defer bulk.Close()

	want, err := st.ListLinks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got, err := bulk.ListLinks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != n || len(want) != n {
		t.Fatalf("seeded %d links, the store saved %d; want %d each", len(got), len(want), n)
	}
	// The store stamps links with the time of the save; Seed spreads them
	// over two years. Everything else must be equal, IDs included.
	for i := range want {
		w, g := want[i], got[i]
		w.CreatedAt, w.UpdatedAt = g.CreatedAt, g.UpdatedAt
		if !reflect.DeepEqual(g, w) {
			t.Fatalf("link %d differs:\nseeded: %+v\nstore:  %+v", i, g, w)
		}
	}

	for _, l := range want {
		wantRevs, err := st.Revisions(ctx, l.ID)
		if err != nil {
			t.Fatal(err)
		}
		gotRevs, err := bulk.Revisions(ctx, l.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(gotRevs) != 1 || len(wantRevs) != 1 {
			t.Fatalf("link %q: %d seeded revisions, %d stored; want 1 each", l.Name, len(gotRevs), len(wantRevs))
		}
		g, w := gotRevs[0], wantRevs[0]
		if g.ID != w.ID || g.LinkID != w.LinkID || g.EditorID != w.EditorID || g.Action != w.Action {
			t.Fatalf("link %q: seeded revision %+v, stored %+v", l.Name, g, w)
		}
		gl, err := store.DecodeSnapshot(g.Snapshot)
		if err != nil {
			t.Fatal(err)
		}
		wl, err := store.DecodeSnapshot(w.Snapshot)
		if err != nil {
			t.Fatal(err)
		}
		if !g.At.Equal(gl.UpdatedAt) {
			t.Fatalf("link %q: the seeded revision is dated %v, its snapshot %v", l.Name, g.At, gl.UpdatedAt)
		}
		wl.CreatedAt, wl.UpdatedAt = gl.CreatedAt, gl.UpdatedAt
		if !reflect.DeepEqual(gl, wl) {
			t.Fatalf("link %q: snapshots differ:\nseeded: %+v\nstore:  %+v", l.Name, gl, wl)
		}
	}
}

// TestSeedRefusesNonEmpty: a second seed would collide on IDs halfway
// through; it is refused before anything is written.
func TestSeedRefusesNonEmpty(t *testing.T) {
	gen, path := seeded(t, 5)
	if err := Seed(context.Background(), path, gen.Links); err == nil {
		t.Fatal("seeding a database that holds links succeeded")
	}
}

// TestWorkloadDecisions checks every generated request against the resolver
// itself, loaded from a seeded database the way the server loads it: a hit
// must be a hit, and each kind of miss that kind. A run that measured
// something other than what its table says would be worse than no run.
func TestWorkloadDecisions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	gen, path := seeded(t, 3000)
	st, err := store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a, err := app.New(ctx, st)
	if err != nil {
		t.Fatal(err)
	}

	suggested := 0
	for _, tc := range []struct {
		paths []string
		want  app.DecisionKind
	}{
		{gen.Hits, app.DecisionHit},
		{gen.KnownMisses, app.DecisionMissKnown},
		{gen.UnknownMisses, app.DecisionMissUnknown},
	} {
		if len(tc.paths) < 100 {
			t.Errorf("only %d %v requests were generated", len(tc.paths), tc.want)
		}
		for _, p := range tc.paths {
			d := a.Resolve(p)
			if d.Kind != tc.want {
				t.Errorf("Resolve(%q) = %v, want %v", p, d.Kind, tc.want)
			}
			if d.Kind == app.DecisionMissUnknown && len(d.Suggestions) > 0 {
				suggested++
			}
		}
	}
	// Half the unknown names are typos of real ones; most of those must
	// produce suggestions, or the miss page measured is the cheap kind only.
	if suggested < len(gen.UnknownMisses)/4 {
		t.Errorf("only %d of %d unknown-name misses have suggestions", suggested, len(gen.UnknownMisses))
	}
}

// TestGenerate checks what the run's table rests on: the same seed gives the
// same Directory, names are unique, and the kinds of link come in their
// shares.
func TestGenerate(t *testing.T) {
	t.Parallel()
	const n = 10000
	a, err := Generate(n, 7, 1)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Generate(n, 7, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Fatal("the same seed gave two different Directories")
	}
	if len(a.Links) != n {
		t.Fatalf("got %d links, want %d", len(a.Links), n)
	}

	names := map[string]bool{}
	single, withVars, many := 0, 0, 0
	totalLen := 0
	for i, l := range a.Links {
		if names[l.Name] {
			t.Fatalf("name %q appears twice", l.Name)
		}
		names[l.Name] = true
		totalLen += len(l.Name)
		if l.ID != int64(i+1) {
			t.Fatalf("link %d has ID %d", i, l.ID)
		}
		hasVars := false
		for _, r := range l.Routes {
			if len(r.Variables) > 0 {
				hasVars = true
			}
		}
		switch {
		case len(l.Routes) >= 6:
			many++
		case hasVars:
			withVars++
		case len(l.Routes) == 1:
			single++
		}
		if _, problems := linkmodel.ValidateDraft(draftOf(l)); problems.HasErrors() {
			t.Fatalf("link %q does not pass the save rules: %+v", l.Name, problems)
		}
	}
	share := func(k int) float64 { return float64(k) / n }
	if s := share(single); s < 0.65 || s > 0.75 {
		t.Errorf("%.1f%% of links are a single address, want about 70%%", 100*s)
	}
	if s := share(withVars); s < 0.20 || s > 0.32 {
		t.Errorf("%.1f%% of links have variables and few routes, want about 27%%", 100*s)
	}
	if s := share(many); s < 0.01 || s > 0.04 {
		t.Errorf("%.1f%% of links have six routes or more, want about 2%%", 100*s)
	}
	if avg := float64(totalLen) / n; avg < 6 || avg > 20 {
		t.Errorf("names average %.1f characters; real go links are short words", avg)
	}
	if len(a.SearchOne) == 0 || len(a.SearchTwo) == 0 {
		t.Fatal("no search queries were generated")
	}
}

func draftOf(l linkmodel.Link) linkmodel.Draft {
	d := linkmodel.Draft{Description: l.Description}
	for _, r := range l.Routes {
		d.Routes = append(d.Routes, linkmodel.DraftRoute{Pattern: r.Pattern, Destination: r.Destination, Variables: r.Variables})
	}
	return d
}
