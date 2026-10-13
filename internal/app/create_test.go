package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tfrydrychewicz/ogre/internal/linkmodel"
)

func TestCreateLinkReturnsTheSavedLink(t *testing.T) {
	a, st := newApp(t)
	st.owners[7] = true
	in := Draft{
		Description: "  GitHub shortcuts \n",
		Routes: []DraftRoute{
			{Pattern: "GH", Destination: "https://github.com/acme"},
			{Pattern: "Gh/New", Destination: "https://github.com/Organizations/Acme/new"},
		},
	}
	before := time.Now().UTC().Add(-time.Second)

	got, err := a.CreateLink(context.Background(), Actor{UserID: 7, DisplayName: "Ada"}, in)
	if err != nil {
		t.Fatalf("CreateLink: %v", err)
	}

	if got.ID == 0 || got.Version != 1 {
		t.Errorf("ID = %d, Version = %d; want a new ID and version 1", got.ID, got.Version)
	}
	if got.Name != "gh" {
		t.Errorf("Name = %q, want %q (the shared first segment, lowercase)", got.Name, "gh")
	}
	if got.Description != "GitHub shortcuts" {
		t.Errorf("Description = %q, want it trimmed", got.Description)
	}
	if got.OwnerID != 7 {
		t.Errorf("OwnerID = %d, want 7: the creator becomes the owner (DIR-1)", got.OwnerID)
	}
	if got.CreatedAt.Before(before) || !got.CreatedAt.Equal(got.UpdatedAt) || got.CreatedAt.Location() != time.UTC {
		t.Errorf("CreatedAt = %v, UpdatedAt = %v; want the same recent UTC instant", got.CreatedAt, got.UpdatedAt)
	}

	wantRoutes := []linkmodel.Route{
		{Position: 0, Pattern: "gh", Destination: "https://github.com/acme"},
		// Patterns are saved lowercase; a destination is never altered.
		{Position: 1, Pattern: "gh/new", Destination: "https://github.com/Organizations/Acme/new"},
	}
	if len(got.Routes) != len(wantRoutes) {
		t.Fatalf("Routes = %+v, want %d routes", got.Routes, len(wantRoutes))
	}
	for i, want := range wantRoutes {
		r := got.Routes[i]
		if r.ID == 0 {
			t.Errorf("route %d has no ID", i)
		}
		r.ID = 0
		if r != want {
			t.Errorf("route %d = %+v, want %+v", i, r, want)
		}
	}

	// What the store received is what was returned.
	stored, _ := st.ListLinks(context.Background())
	if len(stored) != 1 || stored[0].Name != "gh" || stored[0].OwnerID != 7 || !slices.Equal(stored[0].Routes, got.Routes) {
		t.Errorf("store holds %+v, want the returned link", stored)
	}
	// The caller's draft is left as it was.
	if in.Routes[0].Pattern != "GH" || in.Routes[1].Pattern != "Gh/New" {
		t.Errorf("CreateLink changed the draft: %+v", in)
	}
}

func TestCreateLinkNameTaken(t *testing.T) {
	a, st := newApp(t)
	ctx := context.Background()
	mustCreate(t, a, draft("on-call", "https://pager.example.com/a"))

	// Names are unique ignoring case (PRD 5.5).
	for _, pattern := range []string{"on-call", "On-Call", "ON-CALL", "on-call/extra"} {
		_, err := a.CreateLink(ctx, localActor, draft(pattern, "https://other.example.com"))
		if !errors.Is(err, ErrNameTaken) {
			t.Errorf("CreateLink(%q) error = %v, want ErrNameTaken", pattern, err)
		}
		var invalid *ValidationError
		if errors.As(err, &invalid) {
			t.Errorf("CreateLink(%q) returned a ValidationError for a taken name", pattern)
		}
	}
	// The existing link is untouched by the failed attempts.
	if d := a.Resolve("/on-call"); d.Destination != "https://pager.example.com/a" {
		t.Errorf("Resolve(/on-call) = %+v after failed creates", d)
	}
	if d := a.Resolve("/on-call/extra"); d.Kind != DecisionMissKnown {
		t.Errorf("Resolve(/on-call/extra) = %+v, want a known-name miss", d)
	}

	// Q5: only case is ignored, so these are free names.
	for _, pattern := range []string{"oncall", "on_call", "on.call"} {
		if _, err := a.CreateLink(ctx, localActor, draft(pattern, "https://example.com/"+pattern)); err != nil {
			t.Errorf("CreateLink(%q): %v, want it to be a different name from on-call", pattern, err)
		}
	}
	links, _ := st.ListLinks(ctx)
	if len(links) != 4 {
		t.Errorf("store holds %d links, want 4 (on-call, oncall, on_call, on.call)", len(links))
	}
	for _, name := range []string{"on-call", "oncall", "on_call", "on.call"} {
		if d := a.Resolve("/" + name); d.Kind != DecisionHit || d.LinkName != name {
			t.Errorf("Resolve(/%s) = %+v, want a hit on %s", name, d, name)
		}
	}
}

func TestCreateLinkForbidden(t *testing.T) {
	a, st := newApp(t)
	// Forbidden is decided before validation: an actor who may not create
	// learns nothing about the draft.
	for _, d := range []Draft{draft("roadmap", "https://example.com"), draft("", ""), {}} {
		_, err := a.CreateLink(context.Background(), Actor{}, d)
		if !errors.Is(err, ErrForbidden) {
			t.Errorf("CreateLink as the zero Actor = %v, want ErrForbidden", err)
		}
	}
	if got := st.calls.Load(); got != 0 {
		t.Errorf("a forbidden create made %d store calls, want 0", got)
	}
	if d := a.Resolve("/roadmap"); d.Kind != DecisionMissUnknown {
		t.Errorf("Resolve(/roadmap) = %+v after a forbidden create", d)
	}
	// Admin is not what grants creation: any identified actor may create.
	st.owners[5] = true
	if _, err := a.CreateLink(context.Background(), Actor{UserID: 5}, draft("roadmap", "https://example.com")); err != nil {
		t.Errorf("CreateLink as a non-admin user: %v", err)
	}
}

func TestCreateLinkStoreFailure(t *testing.T) {
	a, st := newApp(t)
	st.createErr = errors.New("disk full")

	_, err := a.CreateLink(context.Background(), localActor, draft("roadmap", "https://example.com"))
	if !errors.Is(err, st.createErr) {
		t.Fatalf("CreateLink error = %v, want one wrapping the store's", err)
	}
	if errors.Is(err, ErrNameTaken) || errors.Is(err, ErrForbidden) {
		t.Errorf("a store failure was reported as %v", err)
	}
	// The index only changes after a commit.
	if d := a.Resolve("/roadmap"); d.Kind != DecisionMissUnknown {
		t.Errorf("Resolve(/roadmap) = %+v after a failed save, want an unknown-name miss", d)
	}

	st.createErr = nil
	mustCreate(t, a, draft("roadmap", "https://example.com"))
	if d := a.Resolve("/roadmap"); d.Kind != DecisionHit {
		t.Errorf("Resolve(/roadmap) = %+v after the retry, want a hit", d)
	}
}

// The link becomes resolvable only once the store has committed it, and is
// resolvable by the time CreateLink returns.
func TestIndexSwapsAfterCommit(t *testing.T) {
	a, st := newApp(t)
	var during Decision
	st.beforeCommit = func() { during = a.Resolve("/roadmap") }

	mustCreate(t, a, draft("roadmap", "https://example.com"))

	if during.Kind != DecisionMissUnknown {
		t.Errorf("Resolve during the store call = %+v, want an unknown-name miss", during)
	}
	if d := a.Resolve("/roadmap"); d.Kind != DecisionHit {
		t.Errorf("Resolve after CreateLink returned = %+v, want a hit", d)
	}
}

// The rules themselves are tested where they live, in package linkmodel
// (TestValidateDraftProblems). This checks what the use case adds: the
// problems arrive grouped by route, and nothing invalid is stored or indexed.
func TestCreateLinkValidation(t *testing.T) {
	const ok = "https://example.com"
	tests := []struct {
		name   string
		draft  Draft
		link   []string   // codes of the link-level problems
		routes [][]string // codes per route of the draft
	}{
		{"no routes", Draft{Description: "nothing"}, []string{linkmodel.CodeNoRoutes}, nil},
		{"one route, both fields", draft("a b", "ftp://x"), nil, [][]string{
			{linkmodel.CodePatternBadCharacter, linkmodel.CodeDestinationScheme}}},
		{"second of three routes", draft("gh", ok, "gh/{repo}", ok, "gh/new", ok), nil, [][]string{
			nil, {linkmodel.CodePatternVariablesUnsupported}, nil}},
		{"rules across routes", draft("gh", ok, "gl", "nope", "GH", ok), nil, [][]string{
			nil,
			{linkmodel.CodeNameMismatch, linkmodel.CodeDestinationNotAbsolute},
			{linkmodel.CodeDuplicatePattern}}},
		// Q5: these are two names, so they cannot be one link.
		{"hyphen and underscore", draft("on-call", ok, "on_call", ok), nil, [][]string{
			nil, {linkmodel.CodeNameMismatch}}},
	}
	codes := func(problems []linkmodel.Problem) []string {
		var out []string
		for _, p := range problems {
			out = append(out, p.Code)
		}
		return out
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, st := newApp(t)
			link, err := a.CreateLink(context.Background(), localActor, tt.draft)

			var invalid *ValidationError
			if !errors.As(err, &invalid) {
				t.Fatalf("CreateLink = %+v, %v; want a *ValidationError", link, err)
			}
			if link.ID != 0 || link.Name != "" || link.Routes != nil {
				t.Errorf("CreateLink returned link %+v with an error", link)
			}
			if got := codes(invalid.Problems); !slices.Equal(got, tt.link) {
				t.Errorf("link-level problems = %v, want %v", got, tt.link)
			}
			if len(invalid.Routes) != len(tt.draft.Routes) {
				t.Fatalf("Routes has %d entries for a draft of %d routes: %+v", len(invalid.Routes), len(tt.draft.Routes), invalid.Routes)
			}
			for i := range tt.draft.Routes {
				if got := codes(invalid.Routes[i]); !slices.Equal(got, tt.routes[i]) {
					t.Errorf("route %d problems = %v, want %v", i+1, got, tt.routes[i])
				}
				for _, p := range invalid.Routes[i] {
					if strings.HasPrefix(p.Message, "Route ") {
						t.Errorf("message %q names its route; the position in Routes already does", p.Message)
					}
				}
			}
			// Nothing invalid reaches the store or the index.
			if got := st.calls.Load(); got != 0 {
				t.Errorf("an invalid draft made %d store calls, want 0", got)
			}
			if got := a.index.Load().Len(); got != 0 {
				t.Errorf("an invalid draft left %d links in the index", got)
			}
		})
	}
}

// A destination is case-sensitive text (document ids, tokens): the use case
// hands the store exactly what was submitted, and the resolver hands it back.
func TestCreateLinkKeepsTheDestinationVerbatim(t *testing.T) {
	const destination = "https://Docs.Example.com/Road-Map/1aB_cD?Tab=A%2Fb&X=Y#Frag"
	a, st := newApp(t)
	got := mustCreate(t, a, draft("roadmap", destination))
	if got.Routes[0].Destination != destination {
		t.Errorf("CreateLink returned destination %q, want %q", got.Routes[0].Destination, destination)
	}
	stored, _ := st.ListLinks(context.Background())
	if len(stored) != 1 || stored[0].Routes[0].Destination != destination {
		t.Errorf("the store received %+v, want the destination %q", stored, destination)
	}
	if d := a.Resolve("/roadmap"); d.Destination != destination {
		t.Errorf("Resolve(/roadmap).Destination = %q, want %q", d.Destination, destination)
	}
}

// Many goroutines create links at once. Every link must end up resolvable
// (no snapshot swap may drop another writer's link), and of several creates
// of one name exactly one succeeds.
func TestConcurrentCreates(t *testing.T) {
	const n = 100
	a, st := newApp(t)

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name := fmt.Sprintf("link-%d", i)
			if _, err := a.CreateLink(context.Background(), localActor, draft(name, "https://example.com/"+name)); err != nil {
				t.Errorf("CreateLink(%s): %v", name, err)
			}
		}(i)
	}

	results := make(chan error, 10)
	for i := 0; i < cap(results); i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			pattern := "contested"
			if i%2 == 1 {
				pattern = "Contested"
			}
			_, err := a.CreateLink(context.Background(), localActor, draft(pattern, fmt.Sprintf("https://example.com/%d", i)))
			results <- err
		}(i)
	}
	wg.Wait()
	close(results)

	won := 0
	for err := range results {
		switch {
		case err == nil:
			won++
		case !errors.Is(err, ErrNameTaken):
			t.Errorf("contested create failed with %v, want ErrNameTaken", err)
		}
	}
	if won != 1 {
		t.Errorf("%d creates of the same name succeeded, want exactly 1", won)
	}

	for i := 0; i < n; i++ {
		name := fmt.Sprintf("link-%d", i)
		if d := a.Resolve("/" + name); d.Kind != DecisionHit || d.Destination != "https://example.com/"+name {
			t.Errorf("Resolve(/%s) = %+v, want a hit", name, d)
		}
	}
	if d := a.Resolve("/contested"); d.Kind != DecisionHit {
		t.Errorf("Resolve(/contested) = %+v, want a hit", d)
	}
	if got, want := a.index.Load().Len(), n+1; got != want {
		t.Errorf("index holds %d links, want %d", got, want)
	}
	if links, _ := st.ListLinks(context.Background()); len(links) != n+1 {
		t.Errorf("store holds %d links, want %d", len(links), n+1)
	}
}
