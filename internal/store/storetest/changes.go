package storetest

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/tfrydrychewicz/ogre/internal/linkmodel"
	"github.com/tfrydrychewicz/ogre/internal/store"
)

// This file holds the cases about reading one link and about changing and
// deleting links: what an update keeps, what it replaces, and that a refused
// change leaves everything as it was.

func testGetLink(t *testing.T, st store.Store) {
	ctx := context.Background()
	first := mustCreate(t, st, linkmodel.Link{Name: "wiki", Description: "The wiki", Routes: []linkmodel.Route{
		route("wiki/z", "https://wiki.example.com/z"),
		route("wiki", "https://wiki.example.com/"),
	}})
	second := mustCreate(t, st, linkmodel.Link{Name: "gh", Routes: []linkmodel.Route{
		route("gh", "https://github.com/acme"),
	}})

	// Each link comes back whole and with its own routes only.
	assertSameLink(t, mustGet(t, st, first.ID), first)
	assertSameLink(t, mustGet(t, st, second.ID), second)

	got := mustGet(t, st, second.ID)
	if got.Routes == nil || got.Routes[0].Variables == nil {
		t.Errorf("GetLink returned nil Routes or Variables: %+v", got)
	}
	if got.CreatedAt.Location() != time.UTC || got.UpdatedAt.Location() != time.UTC {
		t.Errorf("GetLink returned times in %v / %v, want UTC", got.CreatedAt.Location(), got.UpdatedAt.Location())
	}

	for _, id := range []int64{0, -1, first.ID + second.ID + 1000} {
		l, err := st.GetLink(ctx, id)
		if !errors.Is(err, store.ErrNotFound) {
			t.Errorf("GetLink(%d) = %+v, %v; want ErrNotFound", id, l, err)
		}
	}
}

// testUpdateLink is the happy path with everything an edit can do at once:
// a new description, a changed destination, a route removed, one added, and
// the rest put in another order.
func testUpdateLink(t *testing.T, st store.Store) {
	ctx := context.Background()
	editor := localUserID(t, st)
	other := mustCreate(t, st, linkmodel.Link{Name: "other", Routes: []linkmodel.Route{
		route("other", "https://example.com/other"),
	}})
	created := mustCreate(t, st, linkmodel.Link{Name: "docs", Description: "before", Routes: []linkmodel.Route{
		route("docs", "https://example.com/docs"),
		route("docs/api", "https://example.com/api"),
		route("docs/old", "https://example.com/old"),
	}})
	home, api, old := created.Routes[0], created.Routes[1], created.Routes[2]

	before := time.Now().Add(-2 * time.Second)
	updated, err := st.UpdateLink(ctx, linkmodel.Link{
		ID:          created.ID,
		Name:        "docs",
		Description: "after",
		// Values an update must ignore: it changes neither the owner nor
		// the creation time, and the version is the one it computes.
		OwnerID:   editor + 1000,
		Version:   77,
		CreatedAt: time.Date(1999, 1, 1, 0, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(1999, 1, 1, 0, 0, 0, 0, time.UTC),
		Routes: []linkmodel.Route{
			// api moves to the front with a new destination; a position the
			// store must overwrite.
			{ID: api.ID, Position: 9, Pattern: "docs/api", Destination: "https://api.example.com/v2"},
			// A new route between two kept ones.
			{Pattern: "docs/new", Destination: "https://example.com/new"},
			// home moves to the end, unchanged otherwise. "old" is not
			// named, so it goes.
			{ID: home.ID, Pattern: "docs", Destination: "https://example.com/docs"},
		},
	}, created.Version, editor)
	after := time.Now().Add(2 * time.Second)
	if err != nil {
		t.Fatalf("UpdateLink: %v", err)
	}

	if updated.ID != created.ID || updated.Name != "docs" || updated.Description != "after" {
		t.Errorf("id, name, description = %d, %q, %q; want %d, docs, after", updated.ID, updated.Name, updated.Description, created.ID)
	}
	if updated.OwnerID != created.OwnerID {
		t.Errorf("OwnerID = %d, want it unchanged at %d", updated.OwnerID, created.OwnerID)
	}
	if updated.Version != created.Version+1 {
		t.Errorf("Version = %d, want %d (bumped once)", updated.Version, created.Version+1)
	}
	if !updated.CreatedAt.Equal(created.CreatedAt) {
		t.Errorf("CreatedAt = %v, want it unchanged at %v", updated.CreatedAt, created.CreatedAt)
	}
	if updated.UpdatedAt.Before(before) || updated.UpdatedAt.After(after) || updated.UpdatedAt.Before(created.UpdatedAt) {
		t.Errorf("UpdatedAt = %v, want the time of the update (between %v and %v, not before %v)",
			updated.UpdatedAt, before, after, created.UpdatedAt)
	}
	if updated.UpdatedAt.Location() != time.UTC {
		t.Errorf("UpdatedAt is in %v, want UTC", updated.UpdatedAt.Location())
	}

	if len(updated.Routes) != 3 {
		t.Fatalf("got %d routes, want 3: %+v", len(updated.Routes), updated.Routes)
	}
	added := updated.Routes[1]
	want := []linkmodel.Route{
		{ID: api.ID, Position: 0, Pattern: "docs/api", Destination: "https://api.example.com/v2"},
		{ID: added.ID, Position: 1, Pattern: "docs/new", Destination: "https://example.com/new"},
		{ID: home.ID, Position: 2, Pattern: "docs", Destination: "https://example.com/docs"},
	}
	for i, w := range want {
		g := updated.Routes[i]
		if g.ID != w.ID || g.Position != w.Position || g.Pattern != w.Pattern || g.Destination != w.Destination {
			t.Errorf("route %d = %+v, want %+v", i, g, w)
		}
		if g.Variables == nil {
			t.Errorf("route %d has nil Variables, want an empty list", i)
		}
	}
	if added.ID <= 0 || added.ID == home.ID || added.ID == api.ID || added.ID == old.ID || added.ID == other.Routes[0].ID {
		t.Errorf("the new route got id %d, want one of its own (existing: %d, %d, %d, %d)",
			added.ID, home.ID, api.ID, old.ID, other.Routes[0].ID)
	}

	// What UpdateLink returned is what is stored, and nothing else moved.
	assertStored(t, st, updated)
	assertStored(t, st, other)
	if n := len(mustList(t, st)); n != 2 {
		t.Errorf("the store holds %d links after the update, want 2", n)
	}

	// The new version is the one to edit next; the old one is stale.
	again := updated
	again.Description = "third"
	if _, err := st.UpdateLink(ctx, again, created.Version, editor); !errors.Is(err, store.ErrVersionConflict) {
		t.Errorf("a second update at the old version: error = %v, want ErrVersionConflict", err)
	}
	third := mustUpdate(t, st, again, updated.Version)
	if third.Version != created.Version+2 || third.Description != "third" {
		t.Errorf("after a second update: version %d, description %q; want %d, third", third.Version, third.Description, created.Version+2)
	}
	// Sending back exactly what was read keeps every route's identity.
	for i, r := range third.Routes {
		if r.ID != updated.Routes[i].ID {
			t.Errorf("route %d changed id from %d to %d in an update that kept it", i, updated.Routes[i].ID, r.ID)
		}
	}
	assertStored(t, st, third)
}

func testUpdateLeavesInputAlone(t *testing.T, st store.Store) {
	created := mustCreate(t, st, linkmodel.Link{Name: "docs", Routes: []linkmodel.Route{
		route("docs", "https://example.com/docs"),
	}})
	in := linkmodel.Link{ID: created.ID, Name: "docs", Version: 55, Routes: []linkmodel.Route{
		{ID: created.Routes[0].ID, Position: 7, Pattern: "docs", Destination: "https://example.com/1"},
		{Position: 7, Pattern: "docs/new", Destination: "https://example.com/2"},
	}}
	mustUpdate(t, st, in, created.Version)
	if in.Version != 55 || in.Routes[0].Position != 7 || in.Routes[1].ID != 0 || in.Routes[1].Position != 7 ||
		in.Routes[0].Variables != nil || !in.UpdatedAt.IsZero() {
		t.Errorf("UpdateLink modified its argument: %+v", in)
	}
}

// testUpdateSwapsPatterns exchanges the patterns of two routes that are both
// kept. The end state is valid (each shape still appears once), so the update
// must go through, however the implementation enforces unique shapes while it
// rewrites the routes one by one.
func testUpdateSwapsPatterns(t *testing.T, st store.Store) {
	created := mustCreate(t, st, linkmodel.Link{Name: "sw", Routes: []linkmodel.Route{
		route("sw/a", "https://example.com/a"),
		route("sw/b", "https://example.com/b"),
		route("sw/c", "https://example.com/c"),
	}})
	a, b, c := created.Routes[0], created.Routes[1], created.Routes[2]

	swapped := created
	swapped.Routes = []linkmodel.Route{
		{ID: a.ID, Pattern: "sw/b", Destination: "https://example.com/a"},
		{ID: b.ID, Pattern: "sw/c", Destination: "https://example.com/b"},
		{ID: c.ID, Pattern: "sw/a", Destination: "https://example.com/c"},
	}
	updated := mustUpdate(t, st, swapped, created.Version)
	for i, wantPattern := range []string{"sw/b", "sw/c", "sw/a"} {
		if updated.Routes[i].ID != created.Routes[i].ID || updated.Routes[i].Pattern != wantPattern {
			t.Errorf("route %d = %+v, want id %d with pattern %q", i, updated.Routes[i], created.Routes[i].ID, wantPattern)
		}
	}
	assertStored(t, st, updated)

	// A removed route's pattern is free for a new route in the same update.
	replaced := updated
	replaced.Routes = []linkmodel.Route{
		{ID: a.ID, Pattern: "sw/b", Destination: "https://example.com/a"},
		{Pattern: "sw/c", Destination: "https://example.com/new"},
	}
	updated = mustUpdate(t, st, replaced, updated.Version)
	if len(updated.Routes) != 2 || updated.Routes[1].ID == b.ID || updated.Routes[1].ID == c.ID {
		t.Errorf("routes after replacing one = %+v, want the kept route and a new one", updated.Routes)
	}
	assertStored(t, st, updated)
}

func testUpdateVersionConflict(t *testing.T, st store.Store) {
	ctx := context.Background()
	editor := localUserID(t, st)
	created := mustCreate(t, st, linkmodel.Link{Name: "docs", Description: "v1", Routes: []linkmodel.Route{
		route("docs", "https://example.com/docs"),
		route("docs/api", "https://example.com/api"),
	}})
	change := linkmodel.Link{ID: created.ID, Name: "renamed", Description: "changed", Routes: []linkmodel.Route{
		{ID: created.Routes[1].ID, Pattern: "renamed", Destination: "https://example.com/changed"},
		{Pattern: "renamed/new", Destination: "https://example.com/new"},
	}}

	// At version 1: every other version is refused, older or newer.
	for _, version := range []int{0, -1, created.Version + 1, created.Version + 100} {
		l, err := st.UpdateLink(ctx, change, version, editor)
		if !errors.Is(err, store.ErrVersionConflict) {
			t.Errorf("UpdateLink at version %d = %+v, %v; want ErrVersionConflict", version, l, err)
		}
		if errors.Is(err, store.ErrNotFound) {
			t.Errorf("UpdateLink at version %d: error = %v; a stale version is not a missing link", version, err)
		}
		assertStored(t, st, created)
	}
	if n := len(mustList(t, st)); n != 1 {
		t.Errorf("the store holds %d links after refused updates, want 1", n)
	}

	// After a real update the version that was current is stale.
	v2 := mustUpdate(t, st, linkmodel.Link{ID: created.ID, Name: "docs", Description: "v2", Routes: created.Routes}, created.Version)
	if _, err := st.UpdateLink(ctx, change, created.Version, editor); !errors.Is(err, store.ErrVersionConflict) {
		t.Errorf("UpdateLink at the previous version: error = %v, want ErrVersionConflict", err)
	}
	assertStored(t, st, v2)
}

func testUpdateMissingLink(t *testing.T, st store.Store) {
	ctx := context.Background()
	editor := localUserID(t, st)
	kept := mustCreate(t, st, linkmodel.Link{Name: "kept", Routes: []linkmodel.Route{route("kept", "https://example.com/")}})

	for _, id := range []int64{0, kept.ID + 1000} {
		l, err := st.UpdateLink(ctx, linkmodel.Link{
			ID: id, Name: "ghost", Routes: []linkmodel.Route{route("ghost", "https://example.com/ghost")},
		}, 1, editor)
		if !errors.Is(err, store.ErrNotFound) {
			t.Errorf("UpdateLink(id %d) = %+v, %v; want ErrNotFound", id, l, err)
		}
		if errors.Is(err, store.ErrVersionConflict) {
			t.Errorf("UpdateLink(id %d): error = %v; a missing link is not a version conflict", id, err)
		}
	}
	// An update never creates.
	if got := mustList(t, st); len(got) != 1 {
		t.Fatalf("the store holds %d links, want only the one created", len(got))
	}
	assertStored(t, st, kept)
}

func testRenameToFreeName(t *testing.T, st store.Store) {
	ctx := context.Background()
	created := mustCreate(t, st, linkmodel.Link{Name: "wiki", Description: "The wiki", Routes: []linkmodel.Route{
		route("wiki", "https://wiki.example.com/"),
		route("wiki/a", "https://wiki.example.com/a"),
	}})
	mustCreate(t, st, linkmodel.Link{Name: "zebra", Routes: []linkmodel.Route{route("zebra", "https://example.com/z")}})

	// A rename is an update whose routes start with another first segment.
	renamed := mustUpdate(t, st, linkmodel.Link{ID: created.ID, Name: "handbook", Description: "The wiki", Routes: []linkmodel.Route{
		{ID: created.Routes[0].ID, Pattern: "handbook", Destination: "https://wiki.example.com/"},
		{ID: created.Routes[1].ID, Pattern: "handbook/a", Destination: "https://wiki.example.com/a"},
	}}, created.Version)

	if renamed.ID != created.ID || renamed.Name != "handbook" || renamed.Version != created.Version+1 {
		t.Errorf("renamed link = %+v, want the same id under the name handbook at the next version", renamed)
	}
	for i, r := range renamed.Routes {
		if r.ID != created.Routes[i].ID {
			t.Errorf("route %d changed id from %d to %d in a rename", i, created.Routes[i].ID, r.ID)
		}
	}
	assertStored(t, st, renamed)

	var names []string
	for _, l := range mustList(t, st) {
		names = append(names, l.Name)
	}
	if fmt.Sprint(names) != "[handbook zebra]" {
		t.Errorf("stored names = %v, want [handbook zebra] (the old name gone, ordered by name)", names)
	}

	// The old name is free again.
	reused, err := createLink(ctx, st, linkmodel.Link{
		Name: "wiki", OwnerID: created.OwnerID, Routes: []linkmodel.Route{route("wiki", "https://example.com/new-wiki")},
	})
	if err != nil {
		t.Fatalf("CreateLink under the name a rename gave up: %v", err)
	}
	if reused.ID == created.ID {
		t.Errorf("the new link got id %d, which belongs to the renamed link", reused.ID)
	}
}

func testRenameToTakenName(t *testing.T, st store.Store) {
	ctx := context.Background()
	editor := localUserID(t, st)
	holder := mustCreate(t, st, linkmodel.Link{Name: "taken", Description: "the holder", Routes: []linkmodel.Route{
		route("taken", "https://example.com/holder"),
	}})
	created := mustCreate(t, st, linkmodel.Link{Name: "mine", Description: "before", Routes: []linkmodel.Route{
		route("mine", "https://example.com/mine"),
		route("mine/a", "https://example.com/mine/a"),
	}})

	l, err := st.UpdateLink(ctx, linkmodel.Link{ID: created.ID, Name: "taken", Description: "after", Routes: []linkmodel.Route{
		{ID: created.Routes[0].ID, Pattern: "taken", Destination: "https://example.com/stolen"},
		{Pattern: "taken/b", Destination: "https://example.com/stolen/b"},
	}}, created.Version, editor)
	if !errors.Is(err, store.ErrNameTaken) {
		t.Fatalf("rename to a taken name = %+v, %v; want ErrNameTaken", l, err)
	}
	if errors.Is(err, store.ErrVersionConflict) || errors.Is(err, store.ErrNotFound) {
		t.Errorf("error = %v; a taken name is neither a version conflict nor a missing link", err)
	}
	assertStored(t, st, created)
	assertStored(t, st, holder)
	if n := len(mustList(t, st)); n != 2 {
		t.Errorf("the store holds %d links after the refused rename, want 2", n)
	}

	// Keeping its own name is of course not "taken".
	same := mustUpdate(t, st, linkmodel.Link{ID: created.ID, Name: "mine", Description: "after", Routes: created.Routes}, created.Version)
	if same.Name != "mine" || same.Description != "after" {
		t.Errorf("an update that keeps the name returned %+v", same)
	}
}

func testUpdateNameMustBeNormalized(t *testing.T, st store.Store) {
	ctx := context.Background()
	editor := localUserID(t, st)
	created := mustCreate(t, st, linkmodel.Link{Name: "docs", Routes: []linkmodel.Route{route("docs", "https://example.com/")}})

	for _, name := range []string{"Docs", "DOCS", "Handbook"} {
		_, err := st.UpdateLink(ctx, linkmodel.Link{ID: created.ID, Name: name, Routes: created.Routes}, created.Version, editor)
		if !errors.Is(err, store.ErrNameNotNormalized) {
			t.Errorf("UpdateLink with the name %q: error = %v, want ErrNameNotNormalized", name, err)
		}
	}
	assertStored(t, st, created)
}

// testUpdateRouteOfAnotherLink pins down what a route ID means in an update:
// it names a route of the link being updated, and nothing else. An ID from
// another link, an ID no route has, or the same ID twice is refused whole.
func testUpdateRouteOfAnotherLink(t *testing.T, st store.Store) {
	ctx := context.Background()
	editor := localUserID(t, st)
	victim := mustCreate(t, st, linkmodel.Link{Name: "victim", Description: "untouched", Routes: []linkmodel.Route{
		route("victim", "https://example.com/victim"),
		route("victim/a", "https://example.com/victim/a"),
	}})
	created := mustCreate(t, st, linkmodel.Link{Name: "mine", Description: "before", Routes: []linkmodel.Route{
		route("mine", "https://example.com/mine"),
		route("mine/a", "https://example.com/mine/a"),
	}})
	own := created.Routes[0].ID

	tests := []struct {
		name   string
		routes []linkmodel.Route
	}{
		{"route of another link", []linkmodel.Route{
			{ID: own, Pattern: "mine", Destination: "https://example.com/changed"},
			{ID: victim.Routes[1].ID, Pattern: "mine/stolen", Destination: "https://example.com/stolen"},
		}},
		{"only a route of another link", []linkmodel.Route{
			{ID: victim.Routes[0].ID, Pattern: "mine", Destination: "https://example.com/stolen"},
		}},
		{"id no route has", []linkmodel.Route{
			{ID: own, Pattern: "mine", Destination: "https://example.com/changed"},
			{ID: own + 100000, Pattern: "mine/ghost", Destination: "https://example.com/ghost"},
		}},
		{"negative id", []linkmodel.Route{
			{ID: -1, Pattern: "mine", Destination: "https://example.com/changed"},
		}},
		{"same id twice", []linkmodel.Route{
			{ID: own, Pattern: "mine", Destination: "https://example.com/changed"},
			{ID: own, Pattern: "mine/again", Destination: "https://example.com/again"},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l, err := st.UpdateLink(ctx, linkmodel.Link{ID: created.ID, Name: "mine", Description: "after", Routes: tt.routes},
				created.Version, editor)
			if !errors.Is(err, store.ErrRouteNotInLink) {
				t.Errorf("UpdateLink = %+v, %v; want ErrRouteNotInLink", l, err)
			}
			assertStored(t, st, created)
			assertStored(t, st, victim)
		})
	}

	// The version is checked first: a caller holding a stale copy is told
	// to read again, not that its route IDs are wrong.
	_, err := st.UpdateLink(ctx, linkmodel.Link{ID: created.ID, Name: "mine", Routes: tests[0].routes}, created.Version+1, editor)
	if !errors.Is(err, store.ErrVersionConflict) {
		t.Errorf("stale version and a foreign route: error = %v, want ErrVersionConflict", err)
	}
}

// testFailingUpdateChangesNothing makes an update fail after it has already
// rewritten the link row and some routes: the last route repeats a pattern.
// That is the case that needs the transaction.
func testFailingUpdateChangesNothing(t *testing.T, st store.Store) {
	ctx := context.Background()
	editor := localUserID(t, st)
	created := mustCreate(t, st, linkmodel.Link{Name: "docs", Description: "before", Routes: []linkmodel.Route{
		route("docs", "https://example.com/docs"),
		route("docs/api", "https://example.com/api"),
		route("docs/old", "https://example.com/old"),
	}})

	tests := []struct {
		name   string
		routes []linkmodel.Route
	}{
		{"new route repeats a new route", []linkmodel.Route{
			{ID: created.Routes[0].ID, Pattern: "docs", Destination: "https://example.com/changed"},
			{Pattern: "docs/twice", Destination: "https://example.com/1"},
			{Pattern: "docs/twice", Destination: "https://example.com/2"},
		}},
		{"new route repeats a kept route", []linkmodel.Route{
			{ID: created.Routes[1].ID, Pattern: "docs/api", Destination: "https://example.com/changed"},
			{Pattern: "docs/new", Destination: "https://example.com/new"},
			{Pattern: "docs/api", Destination: "https://example.com/again"},
		}},
		{"kept route takes the pattern of a kept route", []linkmodel.Route{
			{ID: created.Routes[0].ID, Pattern: "docs/renamed", Destination: "https://example.com/changed"},
			{ID: created.Routes[1].ID, Pattern: "docs/renamed", Destination: "https://example.com/api"},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l, err := st.UpdateLink(ctx, linkmodel.Link{ID: created.ID, Name: "docs", Description: "after", Routes: tt.routes},
				created.Version, editor)
			if err == nil {
				t.Fatalf("UpdateLink with a pattern used twice succeeded: %+v", l)
			}
			for _, wrong := range []error{store.ErrNameTaken, store.ErrVersionConflict, store.ErrNotFound} {
				if errors.Is(err, wrong) {
					t.Errorf("error = %v; a repeated pattern must not be reported as %v", err, wrong)
				}
			}
			assertStored(t, st, created)
		})
	}

	// The refused attempts used up nothing: the link is still at its first
	// version and the corrected edit goes through.
	fixed := mustUpdate(t, st, linkmodel.Link{ID: created.ID, Name: "docs", Description: "after", Routes: []linkmodel.Route{
		{ID: created.Routes[0].ID, Pattern: "docs", Destination: "https://example.com/changed"},
		{Pattern: "docs/twice", Destination: "https://example.com/1"},
	}}, created.Version)
	if fixed.Version != created.Version+1 {
		t.Errorf("version after the corrected update = %d, want %d", fixed.Version, created.Version+1)
	}
	assertStored(t, st, fixed)
}

func testDeleteLink(t *testing.T, st store.Store) {
	ctx := context.Background()
	editor := localUserID(t, st)
	kept := mustCreate(t, st, linkmodel.Link{Name: "kept", Description: "stays", Routes: []linkmodel.Route{
		route("kept", "https://example.com/kept"),
		route("kept/a", "https://example.com/kept/a"),
	}})
	doomed := mustCreate(t, st, linkmodel.Link{Name: "doomed", Routes: []linkmodel.Route{
		route("doomed", "https://example.com/doomed"),
		route("doomed/a", "https://example.com/doomed/a"),
	}})

	if err := st.DeleteLink(ctx, doomed.ID, doomed.Version, editor); err != nil {
		t.Fatalf("DeleteLink: %v", err)
	}

	if l, err := st.GetLink(ctx, doomed.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("GetLink after the delete = %+v, %v; want ErrNotFound", l, err)
	}
	got := mustList(t, st)
	if len(got) != 1 {
		t.Fatalf("ListLinks after the delete returned %d links, want 1", len(got))
	}
	assertStored(t, st, kept)

	// Gone means gone for every later call.
	if err := st.DeleteLink(ctx, doomed.ID, 0, editor); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("a second DeleteLink: error = %v, want ErrNotFound", err)
	}
	_, err := st.UpdateLink(ctx, linkmodel.Link{ID: doomed.ID, Name: "doomed", Routes: doomed.Routes}, doomed.Version, editor)
	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("UpdateLink of a deleted link: error = %v, want ErrNotFound", err)
	}

	// Its name is free, and the link that takes it is a new link.
	reborn, err := createLink(ctx, st, linkmodel.Link{
		Name: "doomed", OwnerID: editor, Routes: []linkmodel.Route{route("doomed", "https://example.com/again")},
	})
	if err != nil {
		t.Fatalf("CreateLink under a deleted link's name: %v", err)
	}
	if reborn.Version != 1 || len(reborn.Routes) != 1 {
		t.Errorf("the new link = %+v, want version 1 with its one route", reborn)
	}
	// The deleted link was the newest one, which is when a store that
	// numbers rows "highest plus one" would hand its ID out again. It must
	// not: the history kept under that ID belongs to the deleted link, and
	// an edit still addressed to it (same ID, version 1) must not land on
	// this one.
	if reborn.ID == doomed.ID || reborn.ID == kept.ID {
		t.Errorf("the new link got id %d; ids in use or used before: %d, %d", reborn.ID, kept.ID, doomed.ID)
	}
	assertStored(t, st, reborn)

	// And again after deleting every link: an empty store still remembers.
	for _, l := range mustList(t, st) {
		if err := st.DeleteLink(ctx, l.ID, 0, editor); err != nil {
			t.Fatalf("DeleteLink(%q): %v", l.Name, err)
		}
	}
	last, err := createLink(ctx, st, linkmodel.Link{
		Name: "last", OwnerID: editor, Routes: []linkmodel.Route{route("last", "https://example.com/last")},
	})
	if err != nil {
		t.Fatalf("CreateLink in the emptied store: %v", err)
	}
	if last.ID == doomed.ID || last.ID == kept.ID || last.ID == reborn.ID {
		t.Errorf("the link created in the emptied store got id %d, used before by another link (%d, %d, %d)",
			last.ID, kept.ID, doomed.ID, reborn.ID)
	}
}

func testDeleteVersion(t *testing.T, st store.Store) {
	ctx := context.Background()
	editor := localUserID(t, st)
	created := mustCreate(t, st, linkmodel.Link{Name: "docs", Description: "v1", Routes: []linkmodel.Route{
		route("docs", "https://example.com/docs"),
	}})
	current := mustUpdate(t, st, linkmodel.Link{ID: created.ID, Name: "docs", Description: "v2", Routes: created.Routes}, created.Version)

	for _, version := range []int{created.Version, current.Version + 1, -1} {
		err := st.DeleteLink(ctx, created.ID, version, editor)
		if !errors.Is(err, store.ErrVersionConflict) {
			t.Errorf("DeleteLink at version %d: error = %v, want ErrVersionConflict", version, err)
		}
		if errors.Is(err, store.ErrNotFound) {
			t.Errorf("DeleteLink at version %d: error = %v; a stale version is not a missing link", version, err)
		}
		assertStored(t, st, current)
	}

	if err := st.DeleteLink(ctx, created.ID, current.Version, editor); err != nil {
		t.Fatalf("DeleteLink at the current version: %v", err)
	}
	if got := mustList(t, st); len(got) != 0 {
		t.Errorf("the store still holds %+v", got)
	}

	// Version 0 means "whatever is there": no check.
	unchecked := mustCreate(t, st, linkmodel.Link{Name: "any", Routes: []linkmodel.Route{route("any", "https://example.com/")}})
	unchecked = mustUpdate(t, st, linkmodel.Link{ID: unchecked.ID, Name: "any", Description: "v2", Routes: unchecked.Routes}, unchecked.Version)
	if err := st.DeleteLink(ctx, unchecked.ID, 0, editor); err != nil {
		t.Errorf("DeleteLink with version 0 at stored version %d: %v", unchecked.Version, err)
	}
	if got := mustList(t, st); len(got) != 0 {
		t.Errorf("the store still holds %+v", got)
	}
}

func testDeleteMissingLink(t *testing.T, st store.Store) {
	ctx := context.Background()
	editor := localUserID(t, st)
	kept := mustCreate(t, st, linkmodel.Link{Name: "kept", Routes: []linkmodel.Route{route("kept", "https://example.com/")}})

	for _, version := range []int{0, 1} {
		err := st.DeleteLink(ctx, kept.ID+1000, version, editor)
		if !errors.Is(err, store.ErrNotFound) {
			t.Errorf("DeleteLink of a missing link (version %d): error = %v, want ErrNotFound", version, err)
		}
		if errors.Is(err, store.ErrVersionConflict) {
			t.Errorf("DeleteLink of a missing link (version %d): error = %v; that is not a version conflict", version, err)
		}
	}
	assertStored(t, st, kept)
}

// testConcurrentUpdates is optimistic concurrency under contention: twenty
// editors all read version 1 and all save. Exactly one may win; the others
// must be told, and none of their changes may be mixed into the result.
func testConcurrentUpdates(t *testing.T, st store.Store) {
	ctx := context.Background()
	editor := localUserID(t, st)
	created := mustCreate(t, st, linkmodel.Link{Name: "busy", Description: "v1", Routes: []linkmodel.Route{
		route("busy", "https://example.com/busy"),
		route("busy/a", "https://example.com/busy/a"),
	}})

	const n = 20
	errs := make([]error, n)
	results := make([]linkmodel.Link, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i], errs[i] = st.UpdateLink(ctx, linkmodel.Link{
				ID: created.ID, Name: "busy", Description: fmt.Sprintf("writer %d", i),
				Routes: []linkmodel.Route{
					{ID: created.Routes[0].ID, Pattern: "busy", Destination: fmt.Sprintf("https://example.com/%d", i)},
					{Pattern: fmt.Sprintf("busy/w%d", i), Destination: fmt.Sprintf("https://example.com/%d/new", i)},
				},
			}, created.Version, editor)
		}()
	}
	wg.Wait()

	winner := -1
	for i, err := range errs {
		switch {
		case err == nil && winner >= 0:
			t.Errorf("updates %d and %d both succeeded at version %d", winner, i, created.Version)
		case err == nil:
			winner = i
		case !errors.Is(err, store.ErrVersionConflict):
			t.Errorf("update %d: error = %v, want nil or ErrVersionConflict", i, err)
		}
	}
	if winner < 0 {
		t.Fatal("no update succeeded, want exactly one")
	}

	won := results[winner]
	if won.Version != created.Version+1 || won.Description != fmt.Sprintf("writer %d", winner) || len(won.Routes) != 2 {
		t.Errorf("the winning update returned %+v", won)
	}
	// The stored link is the winner's, whole: no route of a loser slipped in.
	assertStored(t, st, won)
	if revs := revisionsOf(t, st, created.ID); len(revs) != 2 {
		t.Errorf("the link has %d revisions after one create and one successful update, want 2", len(revs))
	}
}

// testConcurrentDeletes: of several deletes of one link, one removes it and
// the rest find it gone.
func testConcurrentDeletes(t *testing.T, st store.Store) {
	ctx := context.Background()
	editor := localUserID(t, st)
	created := mustCreate(t, st, linkmodel.Link{Name: "busy", Routes: []linkmodel.Route{route("busy", "https://example.com/busy")}})

	const n = 10
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = st.DeleteLink(ctx, created.ID, created.Version, editor)
		}()
	}
	wg.Wait()

	deleted := 0
	for i, err := range errs {
		switch {
		case err == nil:
			deleted++
		case !errors.Is(err, store.ErrNotFound):
			t.Errorf("delete %d: error = %v, want nil or ErrNotFound", i, err)
		}
	}
	if deleted != 1 {
		t.Errorf("%d of %d concurrent deletes succeeded, want exactly 1", deleted, n)
	}
	if got := mustList(t, st); len(got) != 0 {
		t.Errorf("the store still holds %+v", got)
	}
	revs := revisionsOf(t, st, created.ID)
	if len(revs) != 2 || revs[1].Action != store.RevisionDelete {
		t.Errorf("revisions after the race = %+v, want one create and one delete", revs)
	}
}
