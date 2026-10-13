package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/tfrydrychewicz/ogre/internal/linkmodel"
)

// localActor is the actor every test acts as unless it says otherwise.
var localActor = Actor{UserID: 1, DisplayName: "Local user", Admin: true}

// newApp returns an App over a fake store holding the given links.
func newApp(t *testing.T, existing ...linkmodel.Link) (*App, *fakeStore) {
	t.Helper()
	st := newFakeStore(existing...)
	a, err := New(context.Background(), st)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	st.calls.Store(0)
	return a, st
}

// draft builds a Draft from pattern, destination pairs.
func draft(pairs ...string) Draft {
	var d Draft
	for i := 0; i+1 < len(pairs); i += 2 {
		d.Routes = append(d.Routes, DraftRoute{Pattern: pairs[i], Destination: pairs[i+1]})
	}
	return d
}

func mustCreate(t *testing.T, a *App, d Draft) linkmodel.Link {
	t.Helper()
	l, err := a.CreateLink(context.Background(), localActor, d)
	if err != nil {
		t.Fatalf("CreateLink(%+v): %v", d, err)
	}
	return l
}

func TestNewLoadsExistingLinks(t *testing.T) {
	a, _ := newApp(t,
		linkmodel.Link{Name: "roadmap", OwnerID: 1, Routes: []linkmodel.Route{
			{Pattern: "roadmap", Destination: "https://example.com/roadmap"}}},
		linkmodel.Link{Name: "gh", OwnerID: 1, Routes: []linkmodel.Route{
			{Pattern: "gh", Destination: "https://github.com/acme"},
			{Pattern: "gh/new", Destination: "https://github.com/new"}}},
	)

	tests := []struct {
		path string
		want Decision
	}{
		{"/roadmap", Decision{DecisionHit, "roadmap", "https://example.com/roadmap"}},
		{"/gh", Decision{DecisionHit, "gh", "https://github.com/acme"}},
		{"/gh/new", Decision{DecisionHit, "gh", "https://github.com/new"}},
		{"/wiki", Decision{DecisionMissUnknown, "wiki", ""}},
	}
	for _, tt := range tests {
		if got := a.Resolve(tt.path); got != tt.want {
			t.Errorf("Resolve(%q) = %+v, want %+v", tt.path, got, tt.want)
		}
	}
}

func TestNewReportsStoreFailure(t *testing.T) {
	st := newFakeStore()
	st.listErr = errors.New("disk on fire")
	a, err := New(context.Background(), st)
	if a != nil || !errors.Is(err, st.listErr) {
		t.Errorf("New = %v, %v; want nil and an error wrapping the store's", a, err)
	}
}

func TestLocalActor(t *testing.T) {
	a, _ := newApp(t)
	got, err := a.LocalActor(context.Background())
	if err != nil {
		t.Fatalf("LocalActor: %v", err)
	}
	if want := (Actor{UserID: 1, DisplayName: "Local user", Admin: true}); got != want {
		t.Errorf("LocalActor = %+v, want %+v", got, want)
	}

	// A non-admin role does not become an admin actor.
	st2 := newFakeStore()
	st2.local.Role = "user"
	a2, _ := New(context.Background(), st2)
	if got, _ := a2.LocalActor(context.Background()); got.Admin {
		t.Errorf("LocalActor for role %q has Admin set", st2.local.Role)
	}

	// The actor a real server uses can create a link.
	if _, err := a.CreateLink(context.Background(), got, draft("roadmap", "https://example.com")); err != nil {
		t.Errorf("CreateLink as LocalActor: %v", err)
	}
}

func TestLocalActorFailureIsNotCached(t *testing.T) {
	a, st := newApp(t)
	st.userErr = errors.New("locked")
	if _, err := a.LocalActor(context.Background()); !errors.Is(err, st.userErr) {
		t.Fatalf("LocalActor error = %v, want one wrapping the store's", err)
	}
	if got := a.Owner(context.Background(), linkmodel.Link{OwnerID: 1}); got != "" {
		t.Errorf("Owner while the store fails = %q, want \"\"", got)
	}
	st.userErr = nil
	if _, err := a.LocalActor(context.Background()); err != nil {
		t.Errorf("LocalActor after the store recovered: %v", err)
	}
}

func TestOwner(t *testing.T) {
	a, st := newApp(t)
	ctx := context.Background()
	l := mustCreate(t, a, draft("roadmap", "https://example.com"))

	if got := a.Owner(ctx, l); got != "Local user" {
		t.Errorf("Owner = %q, want %q", got, "Local user")
	}
	if got := a.Owner(ctx, linkmodel.Link{OwnerID: 99}); got != "" {
		t.Errorf("Owner of an unknown user = %q, want \"\"", got)
	}

	// Naming owners for a long list must not cost a query per link.
	st.calls.Store(0)
	for i := 0; i < 50; i++ {
		a.Owner(ctx, l)
	}
	if got := st.calls.Load(); got > 1 {
		t.Errorf("50 Owner calls made %d store calls, want at most 1", got)
	}
}

func TestListLinks(t *testing.T) {
	a, st := newApp(t)
	ctx := context.Background()

	links, err := a.ListLinks(ctx, localActor)
	if err != nil || len(links) != 0 {
		t.Fatalf("ListLinks on an empty store = %v, %v", links, err)
	}

	mustCreate(t, a, draft("wiki", "https://wiki.example.com"))
	mustCreate(t, a, draft("gh", "https://github.com/acme", "gh/new", "https://github.com/new"))

	links, err = a.ListLinks(ctx, localActor)
	if err != nil {
		t.Fatalf("ListLinks: %v", err)
	}
	if len(links) != 2 || links[0].Name != "gh" || links[1].Name != "wiki" {
		t.Fatalf("ListLinks = %+v, want gh then wiki", links)
	}
	if got := links[0].Routes; len(got) != 2 || got[0].Pattern != "gh" || got[1].Pattern != "gh/new" {
		t.Errorf("routes of gh = %+v, want gh then gh/new", got)
	}

	st.listErr = errors.New("disk on fire")
	if _, err := a.ListLinks(ctx, localActor); !errors.Is(err, st.listErr) {
		t.Errorf("ListLinks error = %v, want one wrapping the store's", err)
	}
}

func TestDecisionKindString(t *testing.T) {
	want := map[DecisionKind]string{
		DecisionHome: "home", DecisionHit: "hit", DecisionMissUnknown: "miss_unknown",
		DecisionMissKnown: "miss_known", DecisionBadRequest: "bad_request", DecisionKind(99): "unknown",
	}
	for kind, s := range want {
		if got := kind.String(); got != s {
			t.Errorf("DecisionKind(%d).String() = %q, want %q", int(kind), got, s)
		}
	}
	// The zero Decision is the home page, the harmless default.
	if (Decision{}).Kind != DecisionHome {
		t.Error("the zero Decision is not DecisionHome")
	}
}

func TestValidationErrorMessage(t *testing.T) {
	p := func(msg string) linkmodel.Problem { return linkmodel.Problem{Message: msg} }
	tests := []struct {
		err  ValidationError
		want string
	}{
		{ValidationError{}, "link is not valid"},
		{ValidationError{Routes: [][]linkmodel.Problem{nil, nil}}, "link is not valid"},
		{ValidationError{Problems: []linkmodel.Problem{p("First.")}}, "link is not valid: First."},
		{ValidationError{Routes: [][]linkmodel.Problem{nil, {p("Second route.")}}}, "link is not valid: Second route."},
		{ValidationError{Routes: [][]linkmodel.Problem{{p("First."), p("Second.")}, {p("Third.")}}}, "link is not valid: First. (and 2 more)"},
		{ValidationError{Problems: []linkmodel.Problem{p("Link.")}, Routes: [][]linkmodel.Problem{{p("Route.")}}}, "link is not valid: Link. (and 1 more)"},
	}
	for _, tt := range tests {
		err := error(&tt.err)
		if got := err.Error(); got != tt.want {
			t.Errorf("Error() = %q, want %q", got, tt.want)
		}
	}
	if strings.Contains((&ValidationError{}).Error(), "%!") {
		t.Error("Error() has a formatting mistake")
	}
}
