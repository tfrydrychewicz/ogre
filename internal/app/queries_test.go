package app

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/tfrydrychewicz/ogre/internal/index"
	"github.com/tfrydrychewicz/ogre/internal/linkmodel"
)

func TestLinkByName(t *testing.T) {
	a, st := newApp(t)
	ctx := context.Background()
	other := st.addUser(t, "Ben")
	reports := mustCreate(t, a, Draft{Description: "Daily reports", Routes: []DraftRoute{
		{Pattern: "reports/{month:int}/{day:int}", Destination: "https://reports.example.com/{month}/{day}"}}})
	// An edit raises the version; the link page and the edit form must see
	// the stored one.
	updated, err := a.UpdateLink(ctx, localActor, reports.ID, Draft{Description: "Daily reports, moved", Routes: []DraftRoute{
		{ID: reports.Routes[0].ID, Pattern: "reports/{month:int}/{day:int}", Destination: "https://new.example.com/{month}/{day}"}}}, 1)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name     string
		actor    Actor
		query    string
		editable bool
		err      error
	}{
		{"owner", localActor, "reports", true, nil},
		{"any case", localActor, "REPORTS", true, nil},
		{"someone else", other, "reports", false, nil},
		{"nobody acting", Actor{}, "reports", false, nil},
		{"unknown", localActor, "reprots", false, ErrNotFound},
		{"empty", localActor, "", false, ErrNotFound},
		{"a path is not a name", localActor, "reports/10/13", false, ErrNotFound},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			l, editable, err := a.LinkByName(ctx, tc.actor, tc.query)
			if !errors.Is(err, tc.err) {
				t.Fatalf("err = %v, want %v", err, tc.err)
			}
			if tc.err != nil {
				return
			}
			if editable != tc.editable {
				t.Errorf("editable = %v, want %v", editable, tc.editable)
			}
			if l.ID != updated.ID || l.Version != 2 || l.Description != "Daily reports, moved" ||
				len(l.Routes) != 1 || l.Routes[0].Destination != "https://new.example.com/{month}/{day}" {
				t.Errorf("link = %+v, want the stored version 2", l)
			}
		})
	}
}

// LinkByName reads the link from the store, so what it finds is what an edit
// is checked against even if another process changed the database.
func TestLinkByNameReadsTheStore(t *testing.T) {
	a, st := newApp(t)
	ctx := context.Background()
	l := mustCreate(t, a, draft("roadmap", "https://example.com/roadmap"))

	st.calls.Store(0)
	if _, _, err := a.LinkByName(ctx, localActor, "roadmap"); err != nil || st.calls.Load() != 1 {
		t.Errorf("err = %v after %d store calls, want nil after 1", err, st.calls.Load())
	}

	st.getErr = errors.New("disk on fire")
	if _, _, err := a.LinkByName(ctx, localActor, "roadmap"); err == nil || errors.Is(err, ErrNotFound) {
		t.Errorf("a failing store gave %v, want a plain error", err)
	}
	st.getErr = nil

	// Deleted behind the App's back: the index still has the name, the
	// store does not.
	if err := st.SQLite.DeleteLink(ctx, l.ID, 0, 1); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.LinkByName(ctx, localActor, "roadmap"); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func linkNames(links []linkmodel.Link) []string {
	out := []string{}
	for _, l := range links {
		out = append(out, l.Name)
	}
	return out
}

// Over the real store, links saved in the same second: the order is then
// by ID, newest first, which is the order they were made in.
func TestYourLinksAndRecentLinksOverSQLite(t *testing.T) {
	a, st := newApp(t)
	ctx := context.Background()
	ben := st.addUser(t, "Ben")
	for _, name := range []string{"alpha", "charlie", "bravo"} {
		mustCreate(t, a, draft(name, "https://example.com/"+name))
	}
	if _, err := a.CreateLink(ctx, ben, draft("bens", "https://example.com/ben")); err != nil {
		t.Fatal(err)
	}
	gone := mustCreate(t, a, draft("gone", "https://example.com/gone"))
	if err := a.DeleteLink(ctx, localActor, gone.ID, 0); err != nil {
		t.Fatal(err)
	}
	st.calls.Store(0)

	tests := []struct {
		name string
		get  func() ([]linkmodel.Link, error)
		want []string
	}{
		{"yours", func() ([]linkmodel.Link, error) { return a.YourLinks(ctx, localActor, 0) }, []string{"bravo", "charlie", "alpha"}},
		{"yours, limited", func() ([]linkmodel.Link, error) { return a.YourLinks(ctx, localActor, 2) }, []string{"bravo", "charlie"}},
		{"Ben's", func() ([]linkmodel.Link, error) { return a.YourLinks(ctx, ben, 0) }, []string{"bens"}},
		{"nobody's", func() ([]linkmodel.Link, error) { return a.YourLinks(ctx, Actor{}, 0) }, []string{}},
		{"recent", func() ([]linkmodel.Link, error) { return a.RecentLinks(ctx, 0) }, []string{"bens", "bravo", "charlie", "alpha"}},
		{"recent, limited", func() ([]linkmodel.Link, error) { return a.RecentLinks(ctx, 1) }, []string{"bens"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			links, err := tc.get()
			if err != nil {
				t.Fatal(err)
			}
			if got := linkNames(links); !slices.Equal(got, tc.want) {
				t.Errorf("got %q, want %q", got, tc.want)
			}
			for _, l := range links {
				if len(l.Routes) != 1 || l.Version != 1 || l.CreatedAt.IsZero() {
					t.Errorf("link %q is not whole: %+v", l.Name, l)
				}
			}
		})
	}
	// The lists come from the index.
	if n := st.calls.Load(); n != 0 {
		t.Errorf("the lists made %d store calls, want 0", n)
	}
}

// The order by time, with times that differ, on an index built directly.
func TestNewestOrder(t *testing.T) {
	at := func(minute int) time.Time { return time.Date(2026, 10, 14, 9, minute, 0, 0, time.UTC) }
	l := func(id int64, name string, owner int64, created, updated int) linkmodel.Link {
		return linkmodel.Link{ID: id, Name: name, OwnerID: owner, Version: 1, CreatedAt: at(created), UpdatedAt: at(updated),
			Routes: []linkmodel.Route{{ID: id, Pattern: name, Destination: "https://example.com/" + name}}}
	}
	a := &App{index: index.New(index.Build([]linkmodel.Link{
		l(1, "oldest", 1, 0, 50), // created first, changed last
		l(2, "middle", 1, 10, 10),
		l(3, "newest", 1, 20, 20),
		l(4, "tie-low", 1, 30, 30),
		l(5, "tie-high", 1, 30, 30), // same second as tie-low, higher ID
		l(6, "not-mine", 2, 40, 60),
	}))}
	ctx := context.Background()

	yours, _ := a.YourLinks(ctx, Actor{UserID: 1}, 0)
	if got, want := linkNames(yours), []string{"oldest", "tie-high", "tie-low", "newest", "middle"}; !slices.Equal(got, want) {
		t.Errorf("YourLinks = %q, want %q", got, want)
	}
	recent, _ := a.RecentLinks(ctx, 0)
	if got, want := linkNames(recent), []string{"not-mine", "tie-high", "tie-low", "newest", "middle", "oldest"}; !slices.Equal(got, want) {
		t.Errorf("RecentLinks = %q, want %q", got, want)
	}
	for limit := 1; limit <= 7; limit++ {
		got, _ := a.RecentLinks(ctx, limit)
		if want := linkNames(recent)[:min(limit, len(recent))]; !slices.Equal(linkNames(got), want) {
			t.Errorf("RecentLinks(%d) = %q, want %q", limit, linkNames(got), want)
		}
	}

	// The result is the caller's.
	recent[0].Routes[0].Destination = "https://evil.example.com"
	if again, _ := a.RecentLinks(ctx, 1); again[0].Routes[0].Destination != "https://example.com/not-mine" {
		t.Errorf("the index changed through a returned link")
	}
}

func TestOwnerName(t *testing.T) {
	a, st := newApp(t)
	ctx := context.Background()
	ben := st.addUser(t, "Ben")
	if got := a.OwnerName(ctx, localActor.UserID); got != "Local user" {
		t.Errorf("OwnerName(local) = %q", got)
	}
	// Only the built-in user can be named until accounts exist.
	if got := a.OwnerName(ctx, ben.UserID); got != "" {
		t.Errorf("OwnerName(Ben) = %q, want \"\"", got)
	}
	if got := a.OwnerName(ctx, 0); got != "" {
		t.Errorf("OwnerName(0) = %q, want \"\"", got)
	}

	// A user that cannot be read is no name, not a failure.
	b, st2 := newApp(t)
	st2.userErr = errors.New("unreadable")
	if got := b.OwnerName(ctx, 1); got != "" {
		t.Errorf("OwnerName with a failing store = %q", got)
	}
}
