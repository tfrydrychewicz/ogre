// Package storetest is the conformance suite for store.Store: the behaviour
// the rest of godir relies on, written against the interface alone.
//
// It lives in its own package, outside any _test file, so that a second
// implementation in another package can run exactly the same checks as the
// SQLite one (implementation plan, section 3.10).
package storetest

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/tfrydrychewicz/godir/internal/linkmodel"
	"github.com/tfrydrychewicz/godir/internal/store"
)

// Factory returns a new, empty store for one test and arranges, with
// t.Cleanup, for it to be closed. "Empty" means freshly opened: the built-in
// local user exists and there are no links.
type Factory func(t *testing.T) store.Store

// Run runs the whole suite. Each case gets its own store from newStore.
func Run(t *testing.T, newStore Factory) {
	t.Helper()
	cases := []struct {
		name string
		run  func(t *testing.T, st store.Store)
	}{
		{"LocalUser", testLocalUser},
		{"ListLinksEmpty", testListLinksEmpty},
		{"CreateLinkSetsFields", testCreateLinkSetsFields},
		{"CreateLinkLeavesInputAlone", testCreateLinkLeavesInputAlone},
		{"RoundTrip", testRoundTrip},
		{"TextIsStoredVerbatim", testTextIsStoredVerbatim},
		{"DuplicateName", testDuplicateName},
		{"NameMustBeNormalized", testNameMustBeNormalized},
		{"UnknownOwner", testUnknownOwner},
		{"FailingRouteStoresNothing", testFailingRouteStoresNothing},
		{"ConcurrentCreates", testConcurrentCreates},
		{"ConcurrentSameName", testConcurrentSameName},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			c.run(t, newStore(t))
		})
	}
}

func testLocalUser(t *testing.T, st store.Store) {
	ctx := context.Background()
	u, err := st.LocalUser(ctx)
	if err != nil {
		t.Fatalf("LocalUser: %v", err)
	}
	want := store.User{ID: u.ID, Email: store.LocalUserEmail, DisplayName: "Local user", Role: "admin", Status: "active"}
	if u != want {
		t.Errorf("LocalUser = %+v, want %+v", u, want)
	}
	if u.ID <= 0 {
		t.Errorf("LocalUser ID = %d, want a positive id", u.ID)
	}
	again, err := st.LocalUser(ctx)
	if err != nil {
		t.Fatalf("LocalUser, second call: %v", err)
	}
	if again != u {
		t.Errorf("LocalUser changed between calls: %+v then %+v", u, again)
	}
}

func testListLinksEmpty(t *testing.T, st store.Store) {
	links, err := st.ListLinks(context.Background())
	if err != nil {
		t.Fatalf("ListLinks: %v", err)
	}
	if links == nil {
		t.Error("ListLinks on an empty store returned nil, want an empty slice")
	}
	if len(links) != 0 {
		t.Errorf("ListLinks on an empty store returned %d links", len(links))
	}
}

func testCreateLinkSetsFields(t *testing.T, st store.Store) {
	ctx := context.Background()
	owner := localUserID(t, st)

	before := time.Now().Add(-2 * time.Second)
	got, err := st.CreateLink(ctx, linkmodel.Link{
		// Values the store must ignore and replace.
		ID:        987654,
		Version:   42,
		CreatedAt: time.Date(1999, 1, 1, 0, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(1999, 1, 1, 0, 0, 0, 0, time.UTC),

		Name:        "roadmap",
		Description: "Where we are going",
		OwnerID:     owner,
		Routes: []linkmodel.Route{
			{ID: 55, Position: 9, Pattern: "roadmap", Destination: "https://example.com/roadmap"},
			{ID: 55, Position: 9, Pattern: "roadmap/q1", Destination: "https://example.com/roadmap/q1"},
			{ID: 55, Position: 9, Pattern: "roadmap/archive", Destination: "https://example.com/old"},
		},
	})
	after := time.Now().Add(2 * time.Second)
	if err != nil {
		t.Fatalf("CreateLink: %v", err)
	}

	if got.ID <= 0 || got.ID == 987654 {
		t.Errorf("ID = %d, want an id assigned by the store", got.ID)
	}
	if got.Version != 1 {
		t.Errorf("Version = %d, want 1", got.Version)
	}
	if got.Name != "roadmap" || got.Description != "Where we are going" || got.OwnerID != owner {
		t.Errorf("name, description, owner = %q, %q, %d", got.Name, got.Description, got.OwnerID)
	}
	if got.CreatedAt.Before(before) || got.CreatedAt.After(after) {
		t.Errorf("CreatedAt = %v, want a time between %v and %v", got.CreatedAt, before, after)
	}
	if !got.UpdatedAt.Equal(got.CreatedAt) {
		t.Errorf("UpdatedAt = %v, want it equal to CreatedAt %v", got.UpdatedAt, got.CreatedAt)
	}
	if got.CreatedAt.Location() != time.UTC {
		t.Errorf("CreatedAt is in %v, want UTC", got.CreatedAt.Location())
	}

	wantPatterns := []string{"roadmap", "roadmap/q1", "roadmap/archive"}
	if len(got.Routes) != len(wantPatterns) {
		t.Fatalf("got %d routes, want %d", len(got.Routes), len(wantPatterns))
	}
	seen := map[int64]bool{}
	for i, r := range got.Routes {
		if r.Pattern != wantPatterns[i] {
			t.Errorf("route %d pattern = %q, want %q", i, r.Pattern, wantPatterns[i])
		}
		if r.Position != i {
			t.Errorf("route %d position = %d, want %d", i, r.Position, i)
		}
		if r.ID <= 0 || seen[r.ID] {
			t.Errorf("route %d id = %d, want a distinct id assigned by the store", i, r.ID)
		}
		seen[r.ID] = true
	}
}

func testCreateLinkLeavesInputAlone(t *testing.T, st store.Store) {
	in := linkmodel.Link{
		Name:    "docs",
		OwnerID: localUserID(t, st),
		Routes:  []linkmodel.Route{{Pattern: "docs", Destination: "https://example.com/docs"}},
	}
	if _, err := st.CreateLink(context.Background(), in); err != nil {
		t.Fatalf("CreateLink: %v", err)
	}
	if in.ID != 0 || in.Routes[0].ID != 0 {
		t.Errorf("CreateLink modified its argument: %+v", in)
	}
}

func testRoundTrip(t *testing.T, st store.Store) {
	ctx := context.Background()
	owner := localUserID(t, st)

	// Created out of name order, and with routes whose patterns do not sort
	// the way they are positioned, to tell stored order from accidental order.
	drafts := []linkmodel.Link{
		{Name: "wiki", Description: "The wiki", OwnerID: owner, Routes: []linkmodel.Route{
			{Pattern: "wiki/z", Destination: "https://wiki.example.com/z"},
			{Pattern: "wiki", Destination: "https://wiki.example.com/"},
			{Pattern: "wiki/a", Destination: "https://wiki.example.com/a"},
		}},
		{Name: "gh", OwnerID: owner, Routes: []linkmodel.Route{
			{Pattern: "gh", Destination: "https://github.com/acme"},
		}},
		{Name: "on-call", Description: "Who to wake", OwnerID: owner, Routes: []linkmodel.Route{
			{Pattern: "on-call/b", Destination: "https://example.com/2"},
			{Pattern: "on-call/a", Destination: "https://example.com/1"},
		}},
	}
	created := map[string]linkmodel.Link{}
	for _, d := range drafts {
		l, err := st.CreateLink(ctx, d)
		if err != nil {
			t.Fatalf("CreateLink(%q): %v", d.Name, err)
		}
		created[l.Name] = l
	}

	got, err := st.ListLinks(ctx)
	if err != nil {
		t.Fatalf("ListLinks: %v", err)
	}
	wantOrder := []string{"gh", "on-call", "wiki"}
	if len(got) != len(wantOrder) {
		t.Fatalf("ListLinks returned %d links, want %d", len(got), len(wantOrder))
	}
	for i, name := range wantOrder {
		if got[i].Name != name {
			t.Errorf("link %d is %q, want %q (ordered by name)", i, got[i].Name, name)
			continue
		}
		assertSameLink(t, got[i], created[name])
	}
}

func testTextIsStoredVerbatim(t *testing.T, st store.Store) {
	ctx := context.Background()
	in := linkmodel.Link{
		Name:        "odd",
		Description: "It's \"quoted\"; DROP TABLE links;-- naïve café 日本語\nsecond line",
		OwnerID:     localUserID(t, st),
		Routes: []linkmodel.Route{
			// Mixed case in every part, an escape, a quote and a fragment: a
			// destination is case-sensitive and must come back byte for byte.
			{Pattern: "odd", Destination: "https://Docs.Example.com/Road-Map/1aB_cD?Tab=A%2Fb&q=a%20b&X='Y'#Frag"},
		},
	}
	if _, err := st.CreateLink(ctx, in); err != nil {
		t.Fatalf("CreateLink: %v", err)
	}
	got := mustList(t, st)
	if len(got) != 1 {
		t.Fatalf("ListLinks returned %d links, want 1", len(got))
	}
	if got[0].Description != in.Description {
		t.Errorf("description = %q, want %q", got[0].Description, in.Description)
	}
	if got[0].Routes[0].Destination != in.Routes[0].Destination {
		t.Errorf("destination = %q, want %q", got[0].Routes[0].Destination, in.Routes[0].Destination)
	}
}

func testDuplicateName(t *testing.T, st store.Store) {
	ctx := context.Background()
	owner := localUserID(t, st)

	first, err := st.CreateLink(ctx, linkmodel.Link{
		Name: "roadmap", Description: "the original", OwnerID: owner,
		Routes: []linkmodel.Route{{Pattern: "roadmap", Destination: "https://example.com/1"}},
	})
	if err != nil {
		t.Fatalf("CreateLink: %v", err)
	}

	_, err = st.CreateLink(ctx, linkmodel.Link{
		Name: "roadmap", Description: "the impostor", OwnerID: owner,
		Routes: []linkmodel.Route{
			{Pattern: "roadmap", Destination: "https://example.com/2"},
			{Pattern: "roadmap/x", Destination: "https://example.com/3"},
		},
	})
	if !errors.Is(err, store.ErrNameTaken) {
		t.Fatalf("second CreateLink error = %v, want ErrNameTaken", err)
	}

	got := mustList(t, st)
	if len(got) != 1 {
		t.Fatalf("after a refused duplicate, ListLinks returned %d links, want 1", len(got))
	}
	assertSameLink(t, got[0], first)

	// The store is still usable for a different name.
	if _, err := st.CreateLink(ctx, linkmodel.Link{
		Name: "other", OwnerID: owner,
		Routes: []linkmodel.Route{{Pattern: "other", Destination: "https://example.com/o"}},
	}); err != nil {
		t.Errorf("CreateLink after a refused duplicate: %v", err)
	}
}

// testNameMustBeNormalized pins down the store's part in "names ignore case":
// it does not lowercase a name (that is the link model's rule), but it
// refuses one that was not, so two spellings of a name can never be stored
// side by side.
func testNameMustBeNormalized(t *testing.T, st store.Store) {
	ctx := context.Background()
	owner := localUserID(t, st)
	route := func(p string) []linkmodel.Route {
		return []linkmodel.Route{{Pattern: p, Destination: "https://example.com/"}}
	}

	if _, err := st.CreateLink(ctx, linkmodel.Link{Name: "docs", OwnerID: owner, Routes: route("docs")}); err != nil {
		t.Fatalf("CreateLink(%q): %v", "docs", err)
	}
	for _, name := range []string{"Docs", "DOCS", "docS", "Other"} {
		_, err := st.CreateLink(ctx, linkmodel.Link{Name: name, OwnerID: owner, Routes: route(linkmodel.NormalizeName(name))})
		if !errors.Is(err, store.ErrNameNotNormalized) {
			t.Errorf("CreateLink(%q) error = %v, want ErrNameNotNormalized", name, err)
		}
		if errors.Is(err, store.ErrNameTaken) {
			t.Errorf("CreateLink(%q) error = %v; a name in the wrong form is not a taken name", name, err)
		}
	}
	// Q5: only case is folded. A separator is part of the name, so these are
	// normalized names of their own.
	for _, name := range []string{"on-call", "on_call", "oncall", "v1.2"} {
		if _, err := st.CreateLink(ctx, linkmodel.Link{Name: name, OwnerID: owner, Routes: route(name)}); err != nil {
			t.Errorf("CreateLink(%q): %v", name, err)
		}
	}

	var names []string
	for _, l := range mustList(t, st) {
		names = append(names, l.Name)
	}
	if fmt.Sprint(names) != "[docs on-call on_call oncall v1.2]" {
		t.Errorf("stored names = %v, want [docs on-call on_call oncall v1.2]", names)
	}
}

func testUnknownOwner(t *testing.T, st store.Store) {
	_, err := st.CreateLink(context.Background(), linkmodel.Link{
		Name: "orphan", OwnerID: localUserID(t, st) + 1000,
		Routes: []linkmodel.Route{{Pattern: "orphan", Destination: "https://example.com/"}},
	})
	if err == nil {
		t.Fatal("CreateLink with an owner that does not exist succeeded")
	}
	if errors.Is(err, store.ErrNameTaken) {
		t.Errorf("error = %v; an unknown owner must not be reported as a taken name", err)
	}
	if got := mustList(t, st); len(got) != 0 {
		t.Errorf("a refused link was stored: %+v", got)
	}
}

// testFailingRouteStoresNothing makes the link row succeed and a later route
// fail, which is the case that needs the transaction: one link may not hold
// the same pattern twice.
func testFailingRouteStoresNothing(t *testing.T, st store.Store) {
	ctx := context.Background()
	owner := localUserID(t, st)

	_, err := st.CreateLink(ctx, linkmodel.Link{
		Name: "twice", OwnerID: owner,
		Routes: []linkmodel.Route{
			{Pattern: "twice", Destination: "https://example.com/1"},
			{Pattern: "twice/a", Destination: "https://example.com/2"},
			{Pattern: "twice", Destination: "https://example.com/3"},
		},
	})
	if err == nil {
		t.Fatal("CreateLink with the same pattern twice succeeded")
	}
	if errors.Is(err, store.ErrNameTaken) {
		t.Errorf("error = %v; a repeated pattern must not be reported as a taken name", err)
	}
	if got := mustList(t, st); len(got) != 0 {
		t.Fatalf("a link whose routes failed was partly stored: %+v", got)
	}

	// The name is free again, so the corrected link can be saved.
	if _, err := st.CreateLink(ctx, linkmodel.Link{
		Name: "twice", OwnerID: owner,
		Routes: []linkmodel.Route{{Pattern: "twice", Destination: "https://example.com/1"}},
	}); err != nil {
		t.Errorf("CreateLink after the failed attempt: %v", err)
	}
}

func testConcurrentCreates(t *testing.T, st store.Store) {
	ctx := context.Background()
	owner := localUserID(t, st)

	const n = 20
	errs := make([]error, n)
	ids := make([]int64, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			name := fmt.Sprintf("link-%02d", i)
			l, err := st.CreateLink(ctx, linkmodel.Link{
				Name: name, OwnerID: owner,
				Routes: []linkmodel.Route{
					{Pattern: name, Destination: "https://example.com/" + name},
					{Pattern: name + "/sub", Destination: "https://example.com/" + name + "/sub"},
				},
			})
			errs[i], ids[i] = err, l.ID
		}()
	}
	wg.Wait()

	seen := map[int64]bool{}
	for i := range n {
		if errs[i] != nil {
			t.Errorf("concurrent CreateLink %d: %v", i, errs[i])
			continue
		}
		if seen[ids[i]] {
			t.Errorf("id %d was handed out twice", ids[i])
		}
		seen[ids[i]] = true
	}

	got := mustList(t, st)
	if len(got) != n {
		t.Fatalf("ListLinks returned %d links, want %d", len(got), n)
	}
	for i, l := range got {
		if want := fmt.Sprintf("link-%02d", i); l.Name != want {
			t.Errorf("link %d is %q, want %q", i, l.Name, want)
		}
		if len(l.Routes) != 2 {
			t.Errorf("link %q has %d routes, want 2", l.Name, len(l.Routes))
		}
	}
}

func testConcurrentSameName(t *testing.T, st store.Store) {
	ctx := context.Background()
	owner := localUserID(t, st)

	const n = 10
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = st.CreateLink(ctx, linkmodel.Link{
				Name: "contested", OwnerID: owner,
				Routes: []linkmodel.Route{{Pattern: "contested", Destination: fmt.Sprintf("https://example.com/%d", i)}},
			})
		}()
	}
	wg.Wait()

	won := 0
	for i, err := range errs {
		switch {
		case err == nil:
			won++
		case !errors.Is(err, store.ErrNameTaken):
			t.Errorf("CreateLink %d: error = %v, want nil or ErrNameTaken", i, err)
		}
	}
	if won != 1 {
		t.Errorf("%d of %d concurrent creates of one name succeeded, want exactly 1", won, n)
	}
	if got := mustList(t, st); len(got) != 1 || len(got[0].Routes) != 1 {
		t.Errorf("after the race the store holds %+v, want one link with one route", got)
	}
}

func localUserID(t *testing.T, st store.Store) int64 {
	t.Helper()
	u, err := st.LocalUser(context.Background())
	if err != nil {
		t.Fatalf("LocalUser: %v", err)
	}
	return u.ID
}

func mustList(t *testing.T, st store.Store) []linkmodel.Link {
	t.Helper()
	links, err := st.ListLinks(context.Background())
	if err != nil {
		t.Fatalf("ListLinks: %v", err)
	}
	return links
}

// assertSameLink compares a link read back with the one CreateLink returned.
// Times are compared as instants, not as struct values.
func assertSameLink(t *testing.T, got, want linkmodel.Link) {
	t.Helper()
	if got.ID != want.ID || got.Name != want.Name || got.Description != want.Description ||
		got.OwnerID != want.OwnerID || got.Version != want.Version {
		t.Errorf("link %q read back as %+v, want %+v", want.Name, got, want)
	}
	if !got.CreatedAt.Equal(want.CreatedAt) || !got.UpdatedAt.Equal(want.UpdatedAt) {
		t.Errorf("link %q times read back as %v / %v, want %v / %v",
			want.Name, got.CreatedAt, got.UpdatedAt, want.CreatedAt, want.UpdatedAt)
	}
	if len(got.Routes) != len(want.Routes) {
		t.Errorf("link %q read back with %d routes, want %d", want.Name, len(got.Routes), len(want.Routes))
		return
	}
	for i := range want.Routes {
		if got.Routes[i] != want.Routes[i] {
			t.Errorf("link %q route %d read back as %+v, want %+v", want.Name, i, got.Routes[i], want.Routes[i])
		}
	}
}
