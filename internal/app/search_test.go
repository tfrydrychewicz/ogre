package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/tfrydrychewicz/ogre/internal/index"
	"github.com/tfrydrychewicz/ogre/internal/linkmodel"
)

// resultText renders a result as "name rank", or "name misspelled/edits".
func resultText(r SearchResult) string {
	if r.Rank == RankMisspelled {
		return fmt.Sprintf("%s %s/%d", r.Link.Name, r.Rank, r.Edits)
	}
	return r.Link.Name + " " + r.Rank
}

// searchApp is an App over the real SQLite store holding the links of
// PRD scenario 6 and a few neighbours, all created through the use case.
func searchApp(t *testing.T) (*App, *spyStore) {
	t.Helper()
	a, st := newApp(t)
	for _, d := range []Draft{
		{Description: "Daily report for a given day", Routes: []DraftRoute{{
			Pattern:     "reports/{month:int}/{day:int}",
			Destination: "https://reports.example.com/daily?month={month}&day={day}",
		}}},
		{Description: "Weekly summary", Routes: []DraftRoute{{Pattern: "reports-weekly", Destination: "https://reports.example.com/weekly"}}},
		{Description: "Source code", Routes: []DraftRoute{{Pattern: "repo", Destination: "https://git.example.com/repo"}}},
		{Description: "Team wiki", Routes: []DraftRoute{{Pattern: "wiki", Destination: "https://wiki.example.com/daily-notes"}}},
	} {
		mustCreate(t, a, d)
	}
	st.calls.Store(0)
	return a, st
}

func TestSearch(t *testing.T) {
	a, st := searchApp(t)
	ctx := context.Background()

	tests := []struct {
		query string
		limit int
		want  []string
	}{
		// PRD scenario 6: Chen searches for "daily report" and finds
		// go/reports. "report" is in its name, so that is its rank.
		{"daily report", 0, []string{"reports name"}},
		{"reports", 0, []string{"reports exact", "reports-weekly prefix"}},
		{"reprots", 0, []string{"reports misspelled/1"}},
		{"daily", 0, []string{"reports description", "wiki destination"}},
		// The owner's display name is searched: the local user owns all.
		{"local user", 2, []string{"repo owner", "reports owner"}},
		// The limit's default is 20 and its maximum 100; 0 or less is the
		// default.
		{"example", -1, []string{"repo destination", "reports destination", "reports-weekly destination", "wiki destination"}},
		{"", 0, []string{}},
		{"   ", 0, []string{}},
	}
	for _, tc := range tests {
		t.Run(tc.query, func(t *testing.T) {
			results, err := a.Search(ctx, localActor, tc.query, tc.limit)
			if err != nil {
				t.Fatalf("Search: %v", err)
			}
			got := []string{}
			for _, r := range results {
				got = append(got, resultText(r))
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("Search(%q) = %q, want %q", tc.query, got, tc.want)
			}
		})
	}
	// A search touches the index only: one database read at most, for the
	// owner's name, which is cached after the first.
	if n := st.calls.Load(); n > 1 {
		t.Errorf("searches made %d store calls, want at most 1", n)
	}
}

// What Search returns is the caller's: changing it leaves the index alone.
func TestSearchReturnsCopies(t *testing.T) {
	a, _ := searchApp(t)
	ctx := context.Background()
	first, _ := a.Search(ctx, localActor, "reports", 1)
	first[0].Link.Routes[0].Destination = "https://evil.example.com"
	first[0].Link.Description = "changed"
	again, _ := a.Search(ctx, localActor, "reports", 1)
	if again[0].Link.Description != "Daily report for a given day" ||
		again[0].Link.Routes[0].Destination != "https://reports.example.com/daily?month={month}&day={day}" {
		t.Errorf("the index changed through a search result: %+v", again[0].Link)
	}
}

// A new or changed link is found by the next search, a deleted one is not:
// search reads the snapshot every write replaces.
func TestSearchFollowsWrites(t *testing.T) {
	a, _ := searchApp(t)
	ctx := context.Background()
	names := func(q string) []string {
		results, err := a.Search(ctx, localActor, q, 0)
		if err != nil {
			t.Fatal(err)
		}
		out := []string{}
		for _, r := range results {
			out = append(out, resultText(r))
		}
		return out
	}

	created := mustCreate(t, a, Draft{Description: "Quarterly numbers", Routes: []DraftRoute{{Pattern: "q", Destination: "https://bi.example.com"}}})
	if got := names("quarterly"); !slices.Equal(got, []string{"q description"}) {
		t.Errorf("after create: %q", got)
	}
	if _, err := a.UpdateLink(ctx, localActor, created.ID, Draft{Description: "Yearly numbers",
		Routes: []DraftRoute{{Pattern: "q", Destination: "https://bi.example.com"}}}, created.Version); err != nil {
		t.Fatal(err)
	}
	if got := names("quarterly"); len(got) != 0 {
		t.Errorf("after update, the old description still matches: %q", got)
	}
	if got := names("yearly"); !slices.Equal(got, []string{"q description"}) {
		t.Errorf("after update: %q", got)
	}
	if err := a.DeleteLink(ctx, localActor, created.ID, 0); err != nil {
		t.Fatal(err)
	}
	if got := names("yearly"); len(got) != 0 {
		t.Errorf("after delete: %q", got)
	}
}

// The edit thresholds of a misspelled name are the App's "Did you mean"
// configuration.
func TestSearchUsesSuggestOptions(t *testing.T) {
	off := linkmodel.SuggestOptions{Thresholds: [3]int{-1, -1, -1}}
	a, _ := newAppWith(t, Options{Suggest: off})
	mustCreate(t, a, draft("reports", "https://reports.example.com"))
	results, err := a.Search(context.Background(), localActor, "reprots", 0)
	if err != nil || len(results) != 0 {
		t.Errorf("Search with suggestions off = %+v, %v; want nothing", results, err)
	}
}

// Without the owner's name, links are still found by everything else.
func TestSearchWithoutOwnerName(t *testing.T) {
	a, st := newApp(t)
	mustCreate(t, a, draft("reports", "https://reports.example.com"))
	a.local = nil // forget the cached user
	st.userErr = errors.New("the user table is unreadable")

	results, err := a.Search(context.Background(), localActor, "reports", 0)
	if err != nil || len(results) != 1 {
		t.Errorf("Search = %+v, %v; want the link", results, err)
	}
	results, err = a.Search(context.Background(), localActor, "local", 0)
	if err != nil || len(results) != 0 {
		t.Errorf("Search(owner) = %+v, %v; want nothing", results, err)
	}
}

// The API spells the index's ranks as app's constants.
func TestRankNames(t *testing.T) {
	want := []string{RankExact, RankPrefix, RankMisspelled, RankName, RankDescription, RankDestination, RankOwner}
	for r := index.RankExact; r <= index.RankOwner; r++ {
		if got := r.String(); got != want[r] {
			t.Errorf("index rank %d = %q, want %q", r, got, want[r])
		}
	}
}

func TestListLimit(t *testing.T) {
	for in, want := range map[int]int{-5: 20, 0: 20, 1: 1, 20: 20, 100: 100, 101: 100, 1 << 40: 100} {
		if got := listLimit(in); got != want {
			t.Errorf("listLimit(%d) = %d, want %d", in, got, want)
		}
	}
}
