package storetest

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/tfrydrychewicz/ogre/internal/linkmodel"
	"github.com/tfrydrychewicz/ogre/internal/store"
)

// This file holds the cases about history: every change writes exactly one
// revision, in the same transaction as the change, with a snapshot in the
// one documented format.

// revisionsOf returns a link's revisions. A Store must also be a
// store.RevisionReader: there is no other way to see, through the interface,
// that history is being written.
func revisionsOf(t *testing.T, st store.Store, linkID int64) []store.Revision {
	t.Helper()
	reader, ok := st.(store.RevisionReader)
	if !ok {
		t.Fatalf("%T does not implement store.RevisionReader; the conformance suite needs it to check that revisions are written", st)
	}
	revs, err := reader.Revisions(context.Background(), linkID)
	if err != nil {
		t.Fatalf("Revisions(%d): %v", linkID, err)
	}
	if revs == nil {
		t.Errorf("Revisions(%d) returned nil, want an empty slice", linkID)
	}
	return revs
}

func testRevisions(t *testing.T, st store.Store) {
	ctx := context.Background()
	editor := localUserID(t, st)

	// Another link, changed in between, whose history must stay its own.
	other1 := mustCreate(t, st, linkmodel.Link{Name: "other", Routes: []linkmodel.Route{route("other", "https://example.com/other")}})

	before := time.Now().Add(-2 * time.Second)
	v1 := mustCreate(t, st, linkmodel.Link{Name: "docs", Description: "first <b> & \"second\"", Routes: []linkmodel.Route{
		route("docs", "https://example.com/docs?a=1&b=<2>"),
		route("docs/api", "https://example.com/api"),
	}})
	first := checkRevisions(t, st, v1.ID, editor, []wantRevision{{store.RevisionCreate, v1}})
	// Text is kept as written. JSON allows "&" and "<" to be written as
	// \u escapes, and Go's encoder does so unless told not to; a snapshot is
	// meant to be readable where it is stored.
	if literal := `https://example.com/docs?a=1&b=<2>`; !strings.Contains(first[0].Snapshot, literal) {
		t.Errorf("snapshot does not contain the destination as written (%s):\n%s", literal, first[0].Snapshot)
	}

	v2 := mustUpdate(t, st, linkmodel.Link{ID: v1.ID, Name: "docs", Description: "second", Routes: []linkmodel.Route{
		{ID: v1.Routes[1].ID, Pattern: "docs/api", Destination: "https://api.example.com/"},
		{Pattern: "docs/new", Destination: "https://example.com/new"},
	}}, v1.Version)
	other2 := mustUpdate(t, st, linkmodel.Link{ID: other1.ID, Name: "other", Description: "changed", Routes: other1.Routes}, other1.Version)

	// A rename: the snapshot carries the new name, the history stays with
	// the link.
	v3 := mustUpdate(t, st, linkmodel.Link{ID: v1.ID, Name: "handbook", Description: "second", Routes: []linkmodel.Route{
		{ID: v2.Routes[0].ID, Pattern: "handbook/api", Destination: "https://api.example.com/"},
	}}, v2.Version)
	checkRevisions(t, st, v1.ID, editor, []wantRevision{
		{store.RevisionCreate, v1}, {store.RevisionUpdate, v2}, {store.RevisionUpdate, v3},
	})

	if err := st.DeleteLink(ctx, v1.ID, v3.Version, editor); err != nil {
		t.Fatalf("DeleteLink: %v", err)
	}
	after := time.Now().Add(2 * time.Second)

	// History outlives the link, and the last entry shows the link as it
	// was before it went: same version, same routes.
	revs := checkRevisions(t, st, v1.ID, editor, []wantRevision{
		{store.RevisionCreate, v1}, {store.RevisionUpdate, v2}, {store.RevisionUpdate, v3}, {store.RevisionDelete, v3},
	})
	for i, r := range revs {
		if r.At.Before(before) || r.At.After(after) {
			t.Errorf("revision %d is dated %v, want a time between %v and %v", i, r.At, before, after)
		}
		if r.At.Location() != time.UTC {
			t.Errorf("revision %d is dated in %v, want UTC", i, r.At.Location())
		}
	}
	// For a create and an update the revision is dated as the link is.
	for i, l := range []linkmodel.Link{v1, v2, v3} {
		if len(revs) > i && !revs[i].At.Equal(l.UpdatedAt) {
			t.Errorf("revision %d is dated %v, want the link's UpdatedAt %v", i, revs[i].At, l.UpdatedAt)
		}
	}

	// The other link has exactly its own two.
	checkRevisions(t, st, other1.ID, editor, []wantRevision{
		{store.RevisionCreate, other1}, {store.RevisionUpdate, other2},
	})

	// A link created under the freed name is a new link with a new history.
	reborn := mustCreate(t, st, linkmodel.Link{Name: "handbook", Routes: []linkmodel.Route{route("handbook", "https://example.com/")}})
	checkRevisions(t, st, reborn.ID, editor, []wantRevision{{store.RevisionCreate, reborn}})
	if n := len(revisionsOf(t, st, v1.ID)); n != 4 {
		t.Errorf("the deleted link has %d revisions after its name was reused, want 4", n)
	}
}

type wantRevision struct {
	action string
	link   linkmodel.Link
}

// checkRevisions asserts that a link has exactly the wanted revisions, oldest
// first, each by the given editor and with a snapshot of the wanted link.
func checkRevisions(t *testing.T, st store.Store, linkID, editor int64, want []wantRevision) []store.Revision {
	t.Helper()
	revs := revisionsOf(t, st, linkID)
	if len(revs) != len(want) {
		t.Fatalf("link %d has %d revisions, want %d: %+v", linkID, len(revs), len(want), revs)
	}
	var lastID int64
	for i, r := range revs {
		if r.Action != want[i].action {
			t.Errorf("revision %d has action %q, want %q", i, r.Action, want[i].action)
		}
		if r.LinkID != linkID {
			t.Errorf("revision %d belongs to link %d, want %d", i, r.LinkID, linkID)
		}
		if r.EditorID != editor {
			t.Errorf("revision %d has editor %d, want %d", i, r.EditorID, editor)
		}
		if r.ID <= lastID {
			t.Errorf("revision %d has id %d after id %d, want ids rising with time", i, r.ID, lastID)
		}
		lastID = r.ID
		assertSnapshot(t, r.Snapshot, want[i].link)
	}
	return revs
}

// assertSnapshot checks a stored snapshot twice. Read as plain JSON, with no
// help from package store, it must be exactly the documented document: that
// is what pins the format for a second implementation and for readers
// outside this code. Read with store.DecodeSnapshot it must give back the
// link.
func assertSnapshot(t *testing.T, snapshot string, want linkmodel.Link) {
	t.Helper()
	var got any
	if err := json.Unmarshal([]byte(snapshot), &got); err != nil {
		t.Errorf("snapshot is not JSON: %v\n%s", err, snapshot)
		return
	}
	if wantDoc := snapshotDocument(want); !reflect.DeepEqual(got, wantDoc) {
		wantJSON, _ := json.Marshal(wantDoc)
		t.Errorf("snapshot of %q is\n%s\nwant a document equal to\n%s", want.Name, snapshot, wantJSON)
	}

	decoded, err := store.DecodeSnapshot(snapshot)
	if err != nil {
		t.Errorf("DecodeSnapshot: %v\n%s", err, snapshot)
		return
	}
	assertSameLink(t, decoded, want)
	if decoded.Routes == nil {
		t.Errorf("DecodeSnapshot returned nil Routes")
	}
	for i, r := range decoded.Routes {
		if r.Variables == nil {
			t.Errorf("DecodeSnapshot returned nil Variables for route %d", i)
		}
	}
}

// snapshotDocument spells out the snapshot format of a link as generic JSON
// values, independently of the types package store encodes with.
func snapshotDocument(l linkmodel.Link) map[string]any {
	routes := []any{}
	for _, r := range l.Routes {
		variables := []any{}
		for _, v := range r.Variables {
			variables = append(variables, map[string]any{
				"name": v.Name, "description": v.Description, "example": v.Example,
			})
		}
		routes = append(routes, map[string]any{
			"id":          float64(r.ID),
			"position":    float64(r.Position),
			"pattern":     r.Pattern,
			"destination": r.Destination,
			"variables":   variables,
		})
	}
	return map[string]any{
		"id":          float64(l.ID),
		"name":        l.Name,
		"description": l.Description,
		"ownerId":     float64(l.OwnerID),
		"version":     float64(l.Version),
		"createdAt":   l.CreatedAt.UTC().Format(time.RFC3339),
		"updatedAt":   l.UpdatedAt.UTC().Format(time.RFC3339),
		"routes":      routes,
	}
}

func testRevisionsOfUnknownLink(t *testing.T, st store.Store) {
	created := mustCreate(t, st, linkmodel.Link{Name: "docs", Routes: []linkmodel.Route{route("docs", "https://example.com/")}})
	if revs := revisionsOf(t, st, created.ID+1000); len(revs) != 0 {
		t.Errorf("a link that never existed has revisions: %+v", revs)
	}
}

// testRefusedChangesWriteNoRevision runs one of every kind of refused change
// and then counts: the history must show only what really happened.
func testRefusedChangesWriteNoRevision(t *testing.T, st store.Store) {
	ctx := context.Background()
	editor := localUserID(t, st)
	holder := mustCreate(t, st, linkmodel.Link{Name: "taken", Routes: []linkmodel.Route{route("taken", "https://example.com/")}})
	created := mustCreate(t, st, linkmodel.Link{Name: "docs", Routes: []linkmodel.Route{
		route("docs", "https://example.com/docs"),
		route("docs/api", "https://example.com/api"),
	}})
	valid := linkmodel.Link{ID: created.ID, Name: "docs", Description: "changed", Routes: created.Routes}

	refused := map[string]func() error{
		"create: name taken": func() error {
			_, err := st.CreateLink(ctx, linkmodel.Link{Name: "docs", OwnerID: editor, Routes: []linkmodel.Route{route("docs", "https://example.com/")}}, editor)
			return err
		},
		"create: name not normalized": func() error {
			_, err := st.CreateLink(ctx, linkmodel.Link{Name: "Fresh", OwnerID: editor, Routes: []linkmodel.Route{route("fresh", "https://example.com/")}}, editor)
			return err
		},
		"create: pattern twice": func() error {
			_, err := st.CreateLink(ctx, linkmodel.Link{Name: "twice", OwnerID: editor, Routes: []linkmodel.Route{
				route("twice", "https://example.com/1"), route("twice", "https://example.com/2"),
			}}, editor)
			return err
		},
		"update: stale version": func() error {
			_, err := st.UpdateLink(ctx, valid, created.Version+1, editor)
			return err
		},
		"update: missing link": func() error {
			l := valid
			l.ID = created.ID + 1000
			_, err := st.UpdateLink(ctx, l, 1, editor)
			return err
		},
		"update: name taken": func() error {
			l := valid
			l.Name = "taken"
			_, err := st.UpdateLink(ctx, l, created.Version, editor)
			return err
		},
		"update: pattern twice": func() error {
			l := valid
			l.Routes = []linkmodel.Route{created.Routes[0], route("docs/x", "https://example.com/1"), route("docs/x", "https://example.com/2")}
			_, err := st.UpdateLink(ctx, l, created.Version, editor)
			return err
		},
		"update: route of another link": func() error {
			l := valid
			l.Routes = []linkmodel.Route{{ID: holder.Routes[0].ID, Pattern: "docs", Destination: "https://example.com/"}}
			_, err := st.UpdateLink(ctx, l, created.Version, editor)
			return err
		},
		"delete: stale version": func() error { return st.DeleteLink(ctx, created.ID, created.Version+1, editor) },
		"delete: missing link":  func() error { return st.DeleteLink(ctx, created.ID+1000, 0, editor) },
	}
	names := make([]string, 0, len(refused))
	for name := range refused {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if err := refused[name](); err == nil {
			t.Errorf("%s: succeeded, want it refused", name)
		}
	}

	checkRevisions(t, st, created.ID, editor, []wantRevision{{store.RevisionCreate, created}})
	checkRevisions(t, st, holder.ID, editor, []wantRevision{{store.RevisionCreate, holder}})
	if revs := revisionsOf(t, st, created.ID+1000); len(revs) != 0 {
		t.Errorf("a refused change to a missing link left revisions: %+v", revs)
	}
	// No refused create left a link, and so no revision of one, behind.
	if got := mustList(t, st); len(got) != 2 {
		t.Errorf("the store holds %d links, want the 2 created", len(got))
	}
	assertStored(t, st, created)
	assertStored(t, st, holder)
}

// testUnknownEditorChangesNothing fails each change at its very last step:
// everything about the link is valid, and only the revision cannot be
// written, because its editor does not exist. The change must be undone with
// it. A link whose history is missing an entry is worse than a refused save.
func testUnknownEditorChangesNothing(t *testing.T, st store.Store) {
	ctx := context.Background()
	owner := localUserID(t, st)
	nobody := owner + 1000
	created := mustCreate(t, st, linkmodel.Link{Name: "docs", Description: "before", Routes: []linkmodel.Route{
		route("docs", "https://example.com/docs"),
		route("docs/api", "https://example.com/api"),
	}})

	l, err := st.CreateLink(ctx, linkmodel.Link{
		Name: "fresh", OwnerID: owner, Routes: []linkmodel.Route{route("fresh", "https://example.com/fresh")},
	}, nobody)
	if err == nil {
		t.Errorf("CreateLink by an editor who does not exist succeeded: %+v", l)
	}
	if errors.Is(err, store.ErrNameTaken) {
		t.Errorf("CreateLink: error = %v; an unknown editor is not a taken name", err)
	}

	l, err = st.UpdateLink(ctx, linkmodel.Link{ID: created.ID, Name: "renamed", Description: "after", Routes: []linkmodel.Route{
		{ID: created.Routes[1].ID, Pattern: "renamed", Destination: "https://example.com/changed"},
		{Pattern: "renamed/new", Destination: "https://example.com/new"},
	}}, created.Version, nobody)
	if err == nil {
		t.Errorf("UpdateLink by an editor who does not exist succeeded: %+v", l)
	}
	for _, wrong := range []error{store.ErrNameTaken, store.ErrVersionConflict, store.ErrNotFound} {
		if errors.Is(err, wrong) {
			t.Errorf("UpdateLink: error = %v; an unknown editor must not be reported as %v", err, wrong)
		}
	}

	if err := st.DeleteLink(ctx, created.ID, created.Version, nobody); err == nil {
		t.Error("DeleteLink by an editor who does not exist succeeded")
	}

	if got := mustList(t, st); len(got) != 1 {
		t.Fatalf("the store holds %d links, want only the one created", len(got))
	}
	assertStored(t, st, created)
	checkRevisions(t, st, created.ID, owner, []wantRevision{{store.RevisionCreate, created}})

	// Nothing is left half-done: the same changes by a real editor succeed.
	if _, err := st.CreateLink(ctx, linkmodel.Link{
		Name: "fresh", OwnerID: owner, Routes: []linkmodel.Route{route("fresh", "https://example.com/fresh")},
	}, owner); err != nil {
		t.Errorf("CreateLink by the local user after the refused one: %v", err)
	}
	if err := st.DeleteLink(ctx, created.ID, created.Version, owner); err != nil {
		t.Errorf("DeleteLink by the local user after the refused one: %v", err)
	}
}
