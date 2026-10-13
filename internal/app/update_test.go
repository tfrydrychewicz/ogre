package app

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tfrydrychewicz/ogre/internal/linkmodel"
	"github.com/tfrydrychewicz/ogre/internal/store"
)

// ghDraft is the link most of these tests edit.
func ghDraft() Draft {
	return Draft{Description: "GitHub shortcuts", Routes: []DraftRoute{
		{Pattern: "gh", Destination: "https://github.com/acme"},
		{Pattern: "gh/new", Destination: "https://github.com/organizations/acme/repositories/new"},
		{Pattern: "gh/{repo}", Destination: "https://github.com/acme/{repo}",
			Variables: []linkmodel.VariableMeta{{Name: "repo", Description: "Repository", Example: "ogre"}}},
	}}
}

// editOf returns the draft an editor would send back for l unchanged: every
// route with its ID.
func editOf(l linkmodel.Link) Draft {
	d := Draft{Description: l.Description}
	for _, r := range l.Routes {
		d.Routes = append(d.Routes, DraftRoute{ID: r.ID, Pattern: r.Pattern, Destination: r.Destination, Variables: r.Variables})
	}
	return d
}

func hits(t *testing.T, a *App, path, destination string) {
	t.Helper()
	if d := a.Resolve(path); d.Kind != DecisionHit || d.Destination != destination {
		t.Errorf("Resolve(%s) = %v %q, want a hit on %q", path, d.Kind, d.Destination, destination)
	}
}

func misses(t *testing.T, a *App, path string, kind DecisionKind) {
	t.Helper()
	if d := a.Resolve(path); d.Kind != kind || d.Destination != "" {
		t.Errorf("Resolve(%s) = %v %q, want %v", path, d.Kind, d.Destination, kind)
	}
}

func TestUpdateLink(t *testing.T) {
	ctx := context.Background()
	a, st := newApp(t)
	created := mustCreate(t, a, ghDraft())

	// Change the description and one destination, drop gh/new, add a route.
	edit := editOf(created)
	edit.Description = "  GitHub, edited  "
	edit.Routes[0].Destination = "https://github.com/acme-corp"
	edit.Routes = append(edit.Routes[:1], edit.Routes[2:]...)
	edit.Routes = append(edit.Routes, DraftRoute{Pattern: "GH/{repo}/pr/{num:INT}", Destination: "https://github.com/acme/{repo}/pull/{num}"})

	got, err := a.UpdateLink(ctx, localActor, created.ID, edit, created.Version)
	if err != nil {
		t.Fatalf("UpdateLink: %v", err)
	}
	if got.ID != created.ID || got.Name != "gh" || got.Version != 2 || got.OwnerID != created.OwnerID {
		t.Errorf("updated link = %+v, want the same link at version 2", got)
	}
	if got.Description != "GitHub, edited" {
		t.Errorf("Description = %q, want it trimmed", got.Description)
	}
	if !got.CreatedAt.Equal(created.CreatedAt) || got.UpdatedAt.Before(created.UpdatedAt) {
		t.Errorf("CreatedAt %v -> %v, UpdatedAt %v -> %v", created.CreatedAt, got.CreatedAt, created.UpdatedAt, got.UpdatedAt)
	}
	// Routes keep their identity through the edit; the new one gets its own.
	if len(got.Routes) != 3 {
		t.Fatalf("Routes = %+v, want 3", got.Routes)
	}
	if got.Routes[0].ID != created.Routes[0].ID || got.Routes[1].ID != created.Routes[2].ID {
		t.Errorf("kept routes have IDs %d and %d, want %d and %d",
			got.Routes[0].ID, got.Routes[1].ID, created.Routes[0].ID, created.Routes[2].ID)
	}
	newID := got.Routes[2].ID
	for _, r := range created.Routes {
		if newID == 0 || newID == r.ID {
			t.Errorf("the new route has ID %d, which is no ID of its own", newID)
		}
	}
	if got.Routes[2].Pattern != "gh/{repo}/pr/{num:int}" || got.Routes[2].Position != 2 {
		t.Errorf("new route = %+v, want the canonical pattern at position 2", got.Routes[2])
	}
	// Variable descriptions survive an edit that sends them back.
	if want := []linkmodel.VariableMeta{{Name: "repo", Description: "Repository", Example: "ogre"}}; !reflect.DeepEqual(got.Routes[1].Variables, want) {
		t.Errorf("variables of gh/{repo} = %+v, want %+v", got.Routes[1].Variables, want)
	}

	// What was returned is what is stored, and what resolves, at once.
	if stored, err := a.GetLink(ctx, localActor, created.ID); err != nil || !reflect.DeepEqual(stored, got) {
		t.Errorf("GetLink = %+v, %v; want the returned link %+v", stored, err, got)
	}
	hits(t, a, "/gh", "https://github.com/acme-corp")
	hits(t, a, "/gh/ogre/pr/7", "https://github.com/acme/ogre/pull/7")
	// gh/new is gone as a route of its own: "new" is now a repository.
	hits(t, a, "/gh/new", "https://github.com/acme/new")
	if d := a.Resolve("/gh"); d.Link.Version != 2 || d.Route.ID != created.Routes[0].ID {
		t.Errorf("Resolve(/gh) answers from link version %d, route %d", d.Link.Version, d.Route.ID)
	}

	// One revision per change, by the editor (plan L2.10).
	revs := st.revisions(t, created.ID)
	if len(revs) != 2 || revs[0].Action != store.RevisionCreate || revs[1].Action != store.RevisionUpdate || revs[1].EditorID != localActor.UserID {
		t.Errorf("revisions = %+v, want a create and an update by the editor", revs)
	}

	// The caller's draft is left as it was.
	if edit.Description != "  GitHub, edited  " || edit.Routes[2].Pattern != "GH/{repo}/pr/{num:INT}" {
		t.Errorf("UpdateLink changed the draft: %+v", edit)
	}
}

// A rename is a draft whose patterns begin with another name. The link keeps
// its identity, the new name resolves at once and the old one stops.
func TestRenameLink(t *testing.T) {
	ctx := context.Background()
	a, st := newApp(t)
	created := mustCreate(t, a, ghDraft())
	mustCreate(t, a, draft("wiki", "https://wiki.example.com"))

	edit := editOf(created)
	edit.Routes[0].Pattern = "GitHub"
	edit.Routes[1].Pattern = "github/new"
	edit.Routes[2].Pattern = "github/{repo}"
	got, err := a.UpdateLink(ctx, localActor, created.ID, edit, 1)
	if err != nil {
		t.Fatalf("rename: %v", err)
	}
	if got.ID != created.ID || got.Name != "github" || got.Version != 2 {
		t.Errorf("renamed link = %+v, want the same link named github at version 2", got)
	}
	for i, r := range got.Routes {
		if r.ID != created.Routes[i].ID {
			t.Errorf("route %d changed its ID from %d to %d in the rename", i, created.Routes[i].ID, r.ID)
		}
	}
	hits(t, a, "/github", "https://github.com/acme")
	hits(t, a, "/GitHub/ogre", "https://github.com/acme/ogre")
	misses(t, a, "/gh", DecisionMissUnknown)
	misses(t, a, "/gh/ogre", DecisionMissUnknown)
	if n := a.index.Load().Len(); n != 2 {
		t.Errorf("index holds %d links after a rename, want 2", n)
	}
	if links, _ := a.ListLinks(ctx, localActor); len(links) != 2 || links[0].Name != "github" {
		t.Errorf("ListLinks after the rename = %+v", links)
	}
	if revs := st.revisions(t, created.ID); len(revs) != 2 {
		t.Errorf("%d revisions after a rename, want 2", len(revs))
	}

	// The old name is free again.
	again := mustCreate(t, a, draft("gh", "https://example.com/new-gh"))
	if again.ID == created.ID {
		t.Error("a new link got the ID of the renamed one")
	}
	hits(t, a, "/gh", "https://example.com/new-gh")
	hits(t, a, "/github", "https://github.com/acme")

	// Renaming onto a name that is taken is refused like creating it, and
	// says which link holds the name (DIR-4).
	edit = editOf(got)
	for i := range edit.Routes {
		edit.Routes[i].Pattern = "Wiki" + edit.Routes[i].Pattern[len("github"):]
	}
	before := dump(t, st)
	_, err = a.UpdateLink(ctx, localActor, got.ID, edit, got.Version)
	var taken *NameTakenError
	if !errors.As(err, &taken) || !errors.Is(err, ErrNameTaken) {
		t.Fatalf("rename onto wiki = %v, want a *NameTakenError", err)
	}
	if taken.Existing.Name != "wiki" || taken.Existing.ID == 0 || taken.Existing.ID == got.ID || len(taken.Existing.Routes) != 1 || !taken.Editable {
		t.Errorf("NameTakenError = %+v, want the wiki link, editable by an admin", taken)
	}
	if dump(t, st) != before {
		t.Error("a refused rename changed the store")
	}
	hits(t, a, "/github", "https://github.com/acme")
	hits(t, a, "/wiki", "https://wiki.example.com")
}

func TestDeleteLink(t *testing.T) {
	ctx := context.Background()
	a, st := newApp(t)
	created := mustCreate(t, a, ghDraft())
	mustCreate(t, a, draft("wiki", "https://wiki.example.com"))

	if err := a.DeleteLink(ctx, localActor, created.ID, created.Version); err != nil {
		t.Fatalf("DeleteLink: %v", err)
	}
	misses(t, a, "/gh", DecisionMissUnknown)
	misses(t, a, "/gh/ogre", DecisionMissUnknown)
	hits(t, a, "/wiki", "https://wiki.example.com")
	if _, err := a.GetLink(ctx, localActor, created.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetLink after the delete = %v, want ErrNotFound", err)
	}
	if links, _ := a.ListLinks(ctx, localActor); len(links) != 1 || links[0].Name != "wiki" {
		t.Errorf("ListLinks after the delete = %+v", links)
	}
	// History outlives the link.
	revs := st.revisions(t, created.ID)
	if len(revs) != 2 || revs[1].Action != store.RevisionDelete || revs[1].EditorID != localActor.UserID {
		t.Errorf("revisions = %+v, want a create and a delete", revs)
	}
	// Deleting again, or editing what is gone, is "not found".
	if err := a.DeleteLink(ctx, localActor, created.ID, 0); !errors.Is(err, ErrNotFound) {
		t.Errorf("second DeleteLink = %v, want ErrNotFound", err)
	}
	if _, err := a.UpdateLink(ctx, localActor, created.ID, ghDraft(), 1); !errors.Is(err, ErrNotFound) {
		t.Errorf("UpdateLink of a deleted link = %v, want ErrNotFound", err)
	}
	// The name is free for a new link, which is a different link.
	again := mustCreate(t, a, draft("gh", "https://example.com/new-gh"))
	if again.ID == created.ID || again.Version != 1 {
		t.Errorf("re-created link = %+v, want a new ID at version 1", again)
	}
	hits(t, a, "/gh", "https://example.com/new-gh")
}

func TestVersionConflict(t *testing.T) {
	ctx := context.Background()
	a, st := newApp(t)
	created := mustCreate(t, a, ghDraft())

	// Two editors read version 1. The first saves.
	first := editOf(created)
	first.Description = "first"
	saved, err := a.UpdateLink(ctx, localActor, created.ID, first, 1)
	if err != nil || saved.Version != 2 {
		t.Fatalf("first update = %+v, %v", saved, err)
	}
	before := dump(t, st)

	conflict := func(t *testing.T, err error, current int) {
		t.Helper()
		var c *VersionConflictError
		if !errors.As(err, &c) || !errors.Is(err, ErrVersionConflict) {
			t.Fatalf("error = %v, want a *VersionConflictError", err)
		}
		if c.CurrentVersion != current {
			t.Errorf("CurrentVersion = %d, want %d", c.CurrentVersion, current)
		}
		if errors.Is(err, ErrNotFound) || errors.Is(err, ErrForbidden) || errors.Is(err, ErrNameTaken) {
			t.Errorf("a version conflict also reads as another error: %v", err)
		}
	}

	second := editOf(created)
	second.Description = "second"
	for _, version := range []int{1, 0, 3, -1} {
		_, err = a.UpdateLink(ctx, localActor, created.ID, second, version)
		conflict(t, err, 2)
	}
	// Stale route IDs come with a stale version: the version is what is
	// reported.
	stale := editOf(created)
	stale.Routes[0].ID = 987654
	_, err = a.UpdateLink(ctx, localActor, created.ID, stale, 1)
	conflict(t, err, 2)

	conflict(t, a.DeleteLink(ctx, localActor, created.ID, 1), 2)
	conflict(t, a.DeleteLink(ctx, localActor, created.ID, 7), 2)

	if dump(t, st) != before {
		t.Error("a refused change wrote to the store")
	}
	if d := a.Resolve("/gh"); d.Link == nil || d.Link.Description != "first" || d.Link.Version != 2 {
		t.Errorf("after the conflicts Resolve answers from %+v", d.Link)
	}

	// With the current version both go through; 0 deletes whatever is there.
	if _, err := a.UpdateLink(ctx, localActor, created.ID, second, 2); err != nil {
		t.Errorf("update at the current version: %v", err)
	}
	if err := a.DeleteLink(ctx, localActor, created.ID, 0); err != nil {
		t.Errorf("DeleteLink with version 0: %v", err)
	}
}

// A change made behind the App's back (another process on the same database)
// is caught by the store's own version check, and reported the same way.
func TestVersionConflictDecidedByTheStore(t *testing.T) {
	ctx := context.Background()
	a, st := newApp(t)
	created := mustCreate(t, a, ghDraft())

	st.beforeWrite = func() {
		st.beforeWrite = nil
		l := created
		l.Description = "changed elsewhere"
		if _, err := st.SQLite.UpdateLink(ctx, l, 1, 1); err != nil {
			t.Errorf("the other writer: %v", err)
		}
	}
	_, err := a.UpdateLink(ctx, localActor, created.ID, editOf(created), 1)
	var c *VersionConflictError
	if !errors.As(err, &c) || c.CurrentVersion != 2 {
		t.Errorf("UpdateLink = %v, want a version conflict naming version 2", err)
	}
}

func TestEditForbidden(t *testing.T) {
	ctx := context.Background()
	a, st := newApp(t)
	grace := st.addUser(t, "Grace")
	hopper := st.addUser(t, "Hopper")
	admins := mustCreate(t, a, ghDraft()) // owned by the local admin
	graces, err := a.CreateLink(ctx, grace, draft("wiki", "https://wiki.example.com"))
	if err != nil {
		t.Fatalf("CreateLink as Grace: %v", err)
	}
	before := dump(t, st)

	invalid := Draft{Routes: []DraftRoute{{Pattern: "{x}", Destination: "nope"}}}
	refused := []struct {
		name  string
		actor Actor
		link  linkmodel.Link
	}{
		{"another user's link", grace, admins},
		{"a user who owns nothing", hopper, graces},
		{"nobody", Actor{}, admins},
		{"nobody, claiming to be an admin", Actor{Admin: true}, admins},
	}
	for _, tt := range refused {
		t.Run(tt.name, func(t *testing.T) {
			// Forbidden is decided before the draft is looked at: an invalid
			// draft, a stale version and a taken name all answer the same.
			for _, d := range []Draft{editOf(tt.link), invalid, draft("wiki", "https://x.example"), draft("gh", "https://x.example")} {
				for _, version := range []int{tt.link.Version, 99} {
					if _, err := a.UpdateLink(ctx, tt.actor, tt.link.ID, d, version); !errors.Is(err, ErrForbidden) {
						t.Errorf("UpdateLink = %v, want ErrForbidden", err)
					}
				}
			}
			for _, version := range []int{0, tt.link.Version, 99} {
				if err := a.DeleteLink(ctx, tt.actor, tt.link.ID, version); !errors.Is(err, ErrForbidden) {
					t.Errorf("DeleteLink = %v, want ErrForbidden", err)
				}
			}
			if a.CanEdit(tt.actor, tt.link) {
				t.Error("CanEdit disagrees with the use cases")
			}
		})
	}
	if dump(t, st) != before {
		t.Error("a forbidden change wrote to the store")
	}
	hits(t, a, "/gh", "https://github.com/acme")
	hits(t, a, "/wiki", "https://wiki.example.com")

	// Nobody acting never reaches the store.
	st.calls.Store(0)
	a.UpdateLink(ctx, Actor{}, admins.ID, editOf(admins), 1)
	a.DeleteLink(ctx, Actor{}, admins.ID, 1)
	if got := st.calls.Load(); got != 0 {
		t.Errorf("changes by nobody made %d store calls, want 0", got)
	}

	// The owner may edit their own link without being an admin, and an
	// admin may edit anyone's. The editor is recorded, the owner stays.
	edit := editOf(graces)
	edit.Description = "Grace's wiki"
	if got, err := a.UpdateLink(ctx, grace, graces.ID, edit, 1); err != nil || got.OwnerID != grace.UserID {
		t.Errorf("the owner's update = %+v, %v", got, err)
	}
	edit.Description = "Fixed by an admin"
	got, err := a.UpdateLink(ctx, localActor, graces.ID, edit, 2)
	if err != nil || got.OwnerID != grace.UserID || got.Version != 3 {
		t.Errorf("the admin's update = %+v, %v; want it saved and still owned by Grace", got, err)
	}
	if revs := st.revisions(t, graces.ID); len(revs) != 3 || revs[1].EditorID != grace.UserID || revs[2].EditorID != localActor.UserID {
		t.Errorf("revisions = %+v, want the editors recorded", revs)
	}
	if err := a.DeleteLink(ctx, grace, graces.ID, 3); err != nil {
		t.Errorf("the owner's delete: %v", err)
	}
}

// DIR-4: a taken name says which link has it and whether the person asking
// could edit that link instead.
func TestNameTakenSaysWhoHasIt(t *testing.T) {
	ctx := context.Background()
	a, st := newApp(t)
	grace := st.addUser(t, "Grace")
	existing := mustCreate(t, a, ghDraft())
	graces, _ := a.CreateLink(ctx, grace, draft("wiki", "https://wiki.example.com"))

	tests := []struct {
		name     string
		try      func() error
		existing linkmodel.Link
		editable bool
	}{
		{"create, by the owner of the existing link",
			func() error { _, err := a.CreateLink(ctx, localActor, draft("GH/x", "https://x.example")); return err }, existing, true},
		{"create, by someone else",
			func() error { _, err := a.CreateLink(ctx, grace, draft("gh", "https://x.example")); return err }, existing, false},
		{"create, by an admin who does not own it",
			func() error { _, err := a.CreateLink(ctx, localActor, draft("wiki", "https://x.example")); return err }, graces, true},
		{"rename, by someone who owns only their own link",
			func() error {
				_, err := a.UpdateLink(ctx, grace, graces.ID, draft("gh", "https://wiki.example.com"), graces.Version)
				return err
			}, existing, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.try()
			var taken *NameTakenError
			if !errors.As(err, &taken) || !errors.Is(err, ErrNameTaken) {
				t.Fatalf("error = %v, want a *NameTakenError", err)
			}
			if !reflect.DeepEqual(taken.Existing, tt.existing) || taken.Editable != tt.editable {
				t.Errorf("NameTakenError = %+v editable %v\n            want %+v editable %v", taken.Existing, taken.Editable, tt.existing, tt.editable)
			}
			var invalid *ValidationError
			if errors.As(err, &invalid) {
				t.Error("a taken name was reported as a ValidationError")
			}
			// The caller got a copy: changing it does not reach the index.
			taken.Existing.Routes[0].Destination = "https://evil.example"
			taken.Existing.Routes[0].Pattern = "evil"
			hits(t, a, "/gh", "https://github.com/acme")
			hits(t, a, "/wiki", "https://wiki.example.com")
		})
	}
}

func TestUpdateLinkValidation(t *testing.T) {
	ctx := context.Background()
	a, st := newApp(t)
	created := mustCreate(t, a, ghDraft())
	other := mustCreate(t, a, draft("wiki", "https://wiki.example.com"))
	before := dump(t, st)

	foreign := editOf(created)
	foreign.Routes[1].ID = other.Routes[0].ID // a route of another link
	unknown := editOf(created)
	unknown.Routes[2].ID = 424242
	twice := editOf(created)
	twice.Routes[1].ID = twice.Routes[0].ID
	sameShape := editOf(created)
	sameShape.Routes = append(sameShape.Routes, DraftRoute{Pattern: "gh/{name}", Destination: "https://github.com/{name}"})
	badMeta := editOf(created)
	badMeta.Routes[2].Variables = []linkmodel.VariableMeta{{Name: "nope", Description: "x"}}

	tests := []struct {
		name   string
		draft  Draft
		link   []string
		routes [][]string
	}{
		{"no routes", Draft{}, []string{linkmodel.CodeNoRoutes}, nil},
		{"same shape as a kept route", sameShape, nil, [][]string{nil, nil, nil, {linkmodel.CodeSameShape}}},
		{"a route of another link", foreign, nil, [][]string{nil, {CodeRouteNotInLink}, nil}},
		{"a route that does not exist", unknown, nil, [][]string{nil, nil, {CodeRouteNotInLink}}},
		{"one saved route kept twice", twice, []string{linkmodel.CodeDuplicateRouteID}, [][]string{nil, nil, nil}},
		{"a description for a variable the pattern lacks", badMeta, nil, [][]string{nil, nil, {linkmodel.CodeUnknownVariableMeta}}},
		{"two names", draft("gh", "https://a.example", "gl", "https://b.example"), nil, [][]string{nil, {linkmodel.CodeNameMismatch}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := a.UpdateLink(ctx, localActor, created.ID, tt.draft, created.Version)
			var invalid *ValidationError
			if !errors.As(err, &invalid) {
				t.Fatalf("UpdateLink = %+v, %v; want a *ValidationError", got, err)
			}
			if !reflect.DeepEqual(codesOf(invalid.Problems), tt.link) {
				t.Errorf("link-level problems = %v, want %v", codesOf(invalid.Problems), tt.link)
			}
			if len(invalid.Routes) != len(tt.routes) {
				t.Fatalf("Routes has %d entries, want %d: %+v", len(invalid.Routes), len(tt.routes), invalid.Routes)
			}
			for i := range tt.routes {
				if !reflect.DeepEqual(codesOf(invalid.Routes[i]), tt.routes[i]) {
					t.Errorf("route %d problems = %v, want %v", i, codesOf(invalid.Routes[i]), tt.routes[i])
				}
			}
			// Validate, asked about the same edit, says the same (NFR-22).
			res := a.Validate(ctx, localActor, ValidateRequest{LinkID: created.ID, Draft: tt.draft})
			if res.Valid || !reflect.DeepEqual(codesOf(res.Problems), tt.link) {
				t.Errorf("Validate: valid %v, link-level %v; want invalid with %v", res.Valid, codesOf(res.Problems), tt.link)
			}
			for i := range tt.routes {
				if !reflect.DeepEqual(codesOf(res.Routes[i].Problems), tt.routes[i]) {
					t.Errorf("Validate: route %d problems = %v, want %v", i, codesOf(res.Routes[i].Problems), tt.routes[i])
				}
			}
		})
	}
	if dump(t, st) != before {
		t.Error("an invalid update wrote to the store")
	}
	hits(t, a, "/gh/new", "https://github.com/organizations/acme/repositories/new")
}

// codesOf lists the codes of the errors among problems; warnings are left
// out. It returns nil for none.
func codesOf(problems []linkmodel.Problem) []string {
	var codes []string
	for _, p := range problems {
		if p.Severity == linkmodel.SeverityError {
			codes = append(codes, p.Code)
		}
	}
	return codes
}

// A warning is advice: it does not keep a link from being saved.
func TestWarningsDoNotBlockSaving(t *testing.T) {
	ctx := context.Background()
	a, _ := newApp(t)
	// {repo} is declared and never used: a warning.
	d := draft("gh/{repo}", "https://github.com/acme")
	if res := a.Validate(ctx, localActor, ValidateRequest{Draft: d}); !res.Valid || len(res.Routes[0].Problems) != 1 ||
		res.Routes[0].Problems[0].Code != linkmodel.CodeDestinationUnusedVariable || res.Routes[0].Problems[0].Severity != linkmodel.SeverityWarning {
		t.Errorf("Validate = %+v, want valid with one warning", res)
	}
	created, err := a.CreateLink(ctx, localActor, d)
	if err != nil {
		t.Fatalf("CreateLink with a warning: %v", err)
	}
	hits(t, a, "/gh/anything", "https://github.com/acme")
	edit := editOf(created)
	edit.Description = "still warned about"
	if _, err := a.UpdateLink(ctx, localActor, created.ID, edit, 1); err != nil {
		t.Errorf("UpdateLink with a warning: %v", err)
	}
}

// The Directory's own hosts are configuration, handed to the destination
// rule: a go link must not lead back into the Directory (PRD 5.4).
func TestOwnHostsAreRefusedAsDestinations(t *testing.T) {
	ctx := context.Background()
	a, _ := newAppWith(t, Options{OwnHosts: []string{"localhost:4655", "https://go.example.com"}})
	plain, _ := newApp(t)

	tests := []struct {
		destination string
		refused     bool
	}{
		{"http://localhost:4655/-/links", true},
		{"http://LOCALHOST:4655/reports/{x}", true},
		{"https://go.example.com/reports", true},
		{"http://localhost:3000/dashboard", false}, // another port is another site
		{"https://example.com/go.example.com", false},
	}
	for i, tt := range tests {
		d := draft("own/{x}", tt.destination)
		d.Routes[0].Pattern = "own" + string(rune('a'+i)) + "/{x}"
		res := a.Validate(ctx, localActor, ValidateRequest{Draft: d})
		_, err := a.CreateLink(ctx, localActor, d)
		var invalid *ValidationError
		if tt.refused {
			if !errors.As(err, &invalid) || !reflect.DeepEqual(codesOf(invalid.Routes[0]), []string{linkmodel.CodeDestinationDirectory}) {
				t.Errorf("CreateLink(%s) = %v, want %s", tt.destination, err, linkmodel.CodeDestinationDirectory)
			}
			if res.Valid || !reflect.DeepEqual(codesOf(res.Routes[0].Problems), []string{linkmodel.CodeDestinationDirectory}) {
				t.Errorf("Validate(%s) = %+v, want the same refusal", tt.destination, res.Routes[0].Problems)
			}
		} else if err != nil || !res.Valid {
			t.Errorf("CreateLink(%s) = %v, Validate valid %v; want it saved", tt.destination, err, res.Valid)
		}
		// Without the option the rule cannot apply.
		if _, err := plain.CreateLink(ctx, localActor, d); err != nil {
			t.Errorf("without OwnHosts, CreateLink(%s) = %v", tt.destination, err)
		}
	}

	// An edit is held to the same rule as a new link: a link that points
	// elsewhere cannot be turned onto the Directory afterwards.
	l := mustCreate(t, a, draft("own", "https://example.com/"))
	for _, tt := range tests {
		d := draft("own", strings.ReplaceAll(tt.destination, "{x}", "x"))
		d.Routes[0].ID = l.Routes[0].ID
		saved, err := a.UpdateLink(ctx, localActor, l.ID, d, l.Version)
		var invalid *ValidationError
		switch {
		case tt.refused:
			if !errors.As(err, &invalid) || len(invalid.Routes) != 1 ||
				!reflect.DeepEqual(codesOf(invalid.Routes[0]), []string{linkmodel.CodeDestinationDirectory}) {
				t.Errorf("UpdateLink onto %s = %v, want %s", tt.destination, err, linkmodel.CodeDestinationDirectory)
			}
		case err != nil:
			t.Errorf("UpdateLink onto %s = %v, want it saved", tt.destination, err)
		default:
			l = saved
		}
	}
	if d := a.Resolve("/own"); d.Kind != DecisionHit || strings.Contains(d.Destination, "4655") || strings.Contains(d.Destination, "go.example.com/reports") {
		t.Errorf("after the refused edits, go/own leads to %q", d.Destination)
	}
}

// What UpdateLink reports when several things are wrong at once. The version
// comes before the draft's own problems: someone whose copy is stale has to
// load the link again whatever they fix, and should hear that first, not
// after correcting every field.
func TestUpdateLinkReportsTheVersionBeforeTheDraft(t *testing.T) {
	ctx := context.Background()
	a, st := newApp(t)
	created := mustCreate(t, a, ghDraft())
	mustCreate(t, a, draft("wiki", "https://wiki.example.com"))
	grace := st.addUser(t, "Grace")
	current, stale := created.Version, created.Version+5

	invalid := Draft{Routes: []DraftRoute{{Pattern: "{x}", Destination: "nope"}}}
	foreign := editOf(created)
	foreign.Routes[0].ID = 987654
	taken := draft("wiki", "https://x.example")

	var (
		conflict   *VersionConflictError
		validation *ValidationError
		nameTaken  *NameTakenError
		forbidden  *ForbiddenError
	)
	tests := []struct {
		name    string
		actor   Actor
		id      int64
		draft   Draft
		version int
		want    any // a pointer to the error type, or the sentinel
	}{
		{"stale version and an invalid draft", localActor, created.ID, invalid, stale, &conflict},
		{"stale version and no routes", localActor, created.ID, Draft{}, stale, &conflict},
		{"stale version and a foreign route", localActor, created.ID, foreign, stale, &conflict},
		{"stale version and a taken name", localActor, created.ID, taken, stale, &conflict},
		{"current version and an invalid draft", localActor, created.ID, invalid, current, &validation},
		{"current version and a foreign route", localActor, created.ID, foreign, current, &validation},
		{"current version and a taken name", localActor, created.ID, taken, current, &nameTaken},
		{"not the owner, with everything else wrong too", grace, created.ID, invalid, stale, &forbidden},
		{"no such link, with everything else wrong too", localActor, 4242, invalid, stale, ErrNotFound},
	}
	before := dump(t, st)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := a.UpdateLink(ctx, tt.actor, tt.id, tt.draft, tt.version)
			ok := false
			switch want := tt.want.(type) {
			case **VersionConflictError:
				ok = errors.As(err, want) && (*want).CurrentVersion == current
			case **ValidationError:
				ok = errors.As(err, want)
			case **NameTakenError:
				ok = errors.As(err, want)
			case **ForbiddenError:
				ok = errors.As(err, want)
			case error:
				ok = errors.Is(err, want)
			}
			if !ok {
				t.Errorf("UpdateLink = %v (%T), want %T", err, err, tt.want)
			}
		})
	}
	if dump(t, st) != before {
		t.Error("a refused update wrote to the store")
	}
}

// A refusal to change someone else's link says which link it is and who owns
// it (PRD 5.7, save example 5), for updates and deletes alike. A refusal that
// is not about a link the actor can see says nothing more.
func TestForbiddenNamesTheLinkAndItsOwner(t *testing.T) {
	ctx := context.Background()
	a, st := newApp(t)
	grace := st.addUser(t, "Grace")
	gh := mustCreate(t, a, ghDraft()) // owned by the built-in user
	owner, err := a.LocalActor(ctx)
	if err != nil || owner.DisplayName == "" {
		t.Fatalf("LocalActor = %+v, %v", owner, err)
	}

	_, updateErr := a.UpdateLink(ctx, grace, gh.ID, editOf(gh), gh.Version)
	deleteErr := a.DeleteLink(ctx, grace, gh.ID, gh.Version)
	for action, err := range map[string]error{"UpdateLink": updateErr, "DeleteLink": deleteErr} {
		var forbidden *ForbiddenError
		if !errors.As(err, &forbidden) || !errors.Is(err, ErrForbidden) {
			t.Fatalf("%s = %v, want a *ForbiddenError that is ErrForbidden", action, err)
		}
		if forbidden.Link.ID != gh.ID || forbidden.Link.Name != "gh" || forbidden.Owner != owner.DisplayName {
			t.Errorf("%s refusal = link %d %q, owner %q; want gh and %q", action, forbidden.Link.ID, forbidden.Link.Name, forbidden.Owner, owner.DisplayName)
		}
		if errors.Is(err, ErrNotFound) || errors.Is(err, ErrVersionConflict) || errors.Is(err, ErrNameTaken) {
			t.Errorf("%s refusal also reads as another error: %v", action, err)
		}
		if !strings.Contains(err.Error(), "gh") || strings.Contains(err.Error(), owner.DisplayName) {
			t.Errorf("%s error text = %q, want it to name the link only", action, err)
		}
	}

	// Nobody acting is refused before any link is read.
	var forbidden *ForbiddenError
	if _, err := a.UpdateLink(ctx, Actor{}, gh.ID, editOf(gh), gh.Version); err != ErrForbidden || errors.As(err, &forbidden) {
		t.Errorf("UpdateLink as nobody = %v, want the bare ErrForbidden", err)
	}
	if err := a.DeleteLink(ctx, Actor{}, gh.ID, 0); err != ErrForbidden {
		t.Errorf("DeleteLink as nobody = %v, want the bare ErrForbidden", err)
	}
	// A link whose owner cannot be named is still named itself.
	graces, err := a.CreateLink(ctx, grace, draft("wiki", "https://wiki.example.com"))
	if err != nil {
		t.Fatal(err)
	}
	hopper := st.addUser(t, "Hopper")
	if err := a.DeleteLink(ctx, hopper, graces.ID, 0); !errors.As(err, &forbidden) || forbidden.Link.Name != "wiki" || forbidden.Owner != "" {
		t.Errorf("DeleteLink of Grace's link by Hopper = %v (%+v), want a refusal naming wiki and no owner", err, forbidden)
	}
}

// Writes go one at a time. A write derives the next snapshot of the index
// from the current one, so two at once would each publish a snapshot without
// the other's change; the single database connection hides that from any
// test that only looks at the outcome. So this one looks at the lock itself:
// whenever a write is inside the store, the write lock is held, and it stays
// held until the use case has published its snapshot and returned.
func TestWritesAreSerialized(t *testing.T) {
	ctx := context.Background()
	a, st := newApp(t)
	const writers = 4
	var existing []linkmodel.Link
	for i := range 2 * writers {
		name := fmt.Sprintf("old-%d", i)
		existing = append(existing, mustCreate(t, a, draft(name, "https://example.com/"+name)))
	}

	var inStore, unlocked atomic.Int64
	st.beforeWrite = func() {
		inStore.Add(1)
		// The lock can be taken only if the writer that is here does not
		// hold it.
		if a.writeMu.TryLock() {
			a.writeMu.Unlock()
			unlocked.Add(1)
		}
		// Long enough for another writer to arrive if nothing holds it back.
		time.Sleep(time.Millisecond)
	}

	// One kind of write at a time: were they mixed, a create that took no
	// lock would go unnoticed whenever an update happened to hold it.
	concurrently := func(do func(i int) error) {
		var wg sync.WaitGroup
		for i := range writers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if err := do(i); err != nil {
					t.Errorf("write %d: %v", i, err)
				}
			}()
		}
		wg.Wait()
	}
	concurrently(func(i int) error {
		name := fmt.Sprintf("new-%d", i)
		_, err := a.CreateLink(ctx, localActor, draft(name, "https://example.com/"+name))
		return err
	})
	if n := unlocked.Swap(0); n != 0 {
		t.Errorf("%d of %d creates were in the store without holding the write lock", n, writers)
	}
	concurrently(func(i int) error {
		d := editOf(existing[i])
		d.Description = "edited"
		_, err := a.UpdateLink(ctx, localActor, existing[i].ID, d, existing[i].Version)
		return err
	})
	if n := unlocked.Swap(0); n != 0 {
		t.Errorf("%d of %d updates were in the store without holding the write lock", n, writers)
	}
	concurrently(func(i int) error {
		gone := existing[writers+i]
		return a.DeleteLink(ctx, localActor, gone.ID, gone.Version)
	})
	if n := unlocked.Swap(0); n != 0 {
		t.Errorf("%d of %d deletes were in the store without holding the write lock", n, writers)
	}
	st.beforeWrite = nil

	if n := inStore.Load(); n != 3*writers {
		t.Fatalf("%d writes reached the store, want %d", n, 3*writers)
	}
	if !a.writeMu.TryLock() {
		t.Fatal("the write lock is still held after every write returned")
	}
	a.writeMu.Unlock()
	// And the outcome every writer is owed: nobody's change is missing.
	for i := range writers {
		hits(t, a, fmt.Sprintf("/new-%d", i), fmt.Sprintf("https://example.com/new-%d", i))
		if d := a.Resolve(fmt.Sprintf("/old-%d", i)); d.Link == nil || d.Link.Description != "edited" {
			t.Errorf("old-%d: the edit is missing from the index: %+v", i, d.Link)
		}
		misses(t, a, fmt.Sprintf("/old-%d", writers+i), DecisionMissUnknown)
	}
}
