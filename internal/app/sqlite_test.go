package app

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/tfrydrychewicz/ogre/internal/store"
)

// The other tests run against a fake store. This one runs the use cases over
// the real SQLite store, on a database in a temporary directory, to check the
// seam itself: the errors app maps, the owner it supplies, and above all that
// a link survives a restart (the L1 "done when").
func TestUseCasesOverSQLite(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "ogre.db")

	st, err := store.Open(ctx, path)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	a, err := New(ctx, st)
	if err != nil {
		st.Close()
		t.Fatalf("New: %v", err)
	}
	actor, err := a.LocalActor(ctx)
	if err != nil || actor.UserID == 0 || !actor.Admin || actor.DisplayName != "Local user" {
		st.Close()
		t.Fatalf("LocalActor = %+v, %v; want the built-in admin", actor, err)
	}

	created, err := a.CreateLink(ctx, actor, Draft{Description: "Team roadmap", Routes: []DraftRoute{
		{Pattern: "Roadmap", Destination: "https://example.com/roadmap"},
		{Pattern: "roadmap/next", Destination: "https://example.com/roadmap/next"},
	}})
	if err != nil {
		st.Close()
		t.Fatalf("CreateLink: %v", err)
	}
	if created.Name != "roadmap" || created.OwnerID != actor.UserID || created.Version != 1 || len(created.Routes) != 2 {
		t.Errorf("created link = %+v", created)
	}
	if got := a.Owner(ctx, created); got != "Local user" {
		t.Errorf("Owner = %q, want %q", got, "Local user")
	}

	// The real unique constraint is reported as app.ErrNameTaken.
	if _, err := a.CreateLink(ctx, actor, draft("ROADMAP", "https://example.com/other")); !errors.Is(err, ErrNameTaken) {
		t.Errorf("second create error = %v, want ErrNameTaken", err)
	}
	// Q5 holds in the database too: a separator makes a different name.
	if _, err := a.CreateLink(ctx, actor, draft("road-map", "https://example.com/hyphen")); err != nil {
		t.Errorf("CreateLink(road-map): %v", err)
	}

	if err := st.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Restart: a new process opens the same file.
	st, err = store.Open(ctx, path)
	if err != nil {
		t.Fatalf("store.Open after restart: %v", err)
	}
	defer st.Close()
	a, err = New(ctx, st)
	if err != nil {
		t.Fatalf("New after restart: %v", err)
	}

	tests := []struct {
		path string
		want Decision
	}{
		{"/roadmap", Decision{DecisionHit, "roadmap", "https://example.com/roadmap"}},
		{"/Roadmap/Next", Decision{DecisionHit, "roadmap", "https://example.com/roadmap/next"}},
		{"/road-map", Decision{DecisionHit, "road-map", "https://example.com/hyphen"}},
		{"/road_map", Decision{DecisionMissUnknown, "road_map", ""}},
		{"/roadmap/later", Decision{DecisionMissKnown, "roadmap", ""}},
	}
	for _, tt := range tests {
		if got := a.Resolve(tt.path); got != tt.want {
			t.Errorf("after restart, Resolve(%q) = %+v, want %+v", tt.path, got, tt.want)
		}
	}

	links, err := a.ListLinks(ctx, actor)
	if err != nil || len(links) != 2 || links[0].Name != "road-map" || links[1].Name != "roadmap" {
		t.Fatalf("ListLinks after restart = %+v, %v; want road-map then roadmap", links, err)
	}
	if got := links[1]; got.ID != created.ID || got.Description != "Team roadmap" ||
		!got.CreatedAt.Equal(created.CreatedAt) || len(got.Routes) != 2 || got.Routes[1].Pattern != "roadmap/next" {
		t.Errorf("reloaded link = %+v, want what was created: %+v", got, created)
	}
}
