package index

import (
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/tfrydrychewicz/ogre/internal/linkmodel"
)

// searchOwners are the display names the searches below may match.
var searchOwners = map[int64]string{1: "Local user", 2: "Ana Kowalska"}

// searchLinks is a small Directory in which every rank occurs.
func searchLinks() []linkmodel.Link {
	l := func(id int64, name, description string, owner int64, destinations ...string) linkmodel.Link {
		out := linkmodel.Link{ID: id, Name: name, Description: description, OwnerID: owner, Version: 1}
		for i, d := range destinations {
			pattern := name
			if i > 0 {
				pattern = fmt.Sprintf("%s/r%d", name, i)
			}
			out.Routes = append(out.Routes, linkmodel.Route{ID: id*10 + int64(i), Position: i, Pattern: pattern, Destination: d})
		}
		return out
	}
	return []linkmodel.Link{
		l(1, "reports", "Daily report for a given day", 1,
			"https://reports.example.com/daily", "https://reports.example.com/archive"),
		l(2, "reports-weekly", "Weekly summary", 1, "https://reports.example.com/weekly"),
		l(3, "report", "The quarterly business review", 1, "https://bi.example.com/q"),
		l(4, "repo", "Source repository", 1, "https://git.example.com/repo"),
		l(5, "platform-status", "Status page for the platform team", 2, "https://status.example.com/platform"),
		l(6, "wiki", "Team wiki", 2, "https://wiki.example.com/daily-notes"),
		l(7, "oncall", "Who is on call this week", 2, "https://pager.example.com/"),
		// Mixed case in, lowercase in the index: description and
		// destination are searched without regard to case.
		l(8, "zebra", "ZOO Animals", 1, "https://ZOO.example.com/Zebra"),
		l(9, "reprots", "", 1, "https://typo.example.com/"),
	}
}

// hitText renders a result as "name rank" or "name misspelled/edits", the
// form the table below pins.
func hitText(h Hit) string {
	if h.Rank == RankMisspelled {
		return fmt.Sprintf("%s %s/%d", h.Link.Name, h.Rank, h.Edits)
	}
	return h.Link.Name + " " + h.Rank.String()
}

func TestSearchOrder(t *testing.T) {
	s := Build(searchLinks())
	tests := []struct {
		query string
		limit int // 0 means 20
		want  []string
	}{
		// Rule 1 before rule 2: the name itself, then names it starts,
		// then misspelled names by edits, then by name: "report" is one
		// deletion away, "reprots" one swap.
		{"reports", 0, []string{"reports exact", "reports-weekly prefix", "report misspelled/1", "reprots misspelled/1"}},
		// Case and surrounding space do not matter. "repo" is two edits
		// away from "report" (6 characters allow two); "reports" and
		// "reports-weekly" begin with it, and the description of
		// "reports" also contains it, which the better rank hides.
		{"  REPORT ", 0, []string{"report exact", "reports prefix", "reports-weekly prefix", "repo misspelled/2", "reprots misspelled/2"}},
		// Three characters allow one edit: "rep" starts four names, and
		// no other name is one edit from it.
		{"rep", 0, []string{"repo prefix", "report prefix", "reports prefix", "reports-weekly prefix", "reprots prefix"}},
		// A name pasted with its go/ prefix and values is still a name,
		// and ranks other names as the bare name would.
		{"go/reports/10/13", 0, []string{"reports exact", "reports-weekly prefix", "report misspelled/1", "reprots misspelled/1"}},
		{"GO/Repo", 0, []string{"repo exact", "report prefix", "reports prefix", "reports-weekly prefix"}},
		// Rule 2: a word inside a name.
		{"status", 0, []string{"platform-status name"}},
		{"call", 0, []string{"oncall name"}},
		// Scenario 6: both words occur, in any order and in any case. The
		// best field one of them occurs in is the name: "report" is in
		// "reports".
		{"daily report", 0, []string{"reports name"}},
		{"report daily", 0, []string{"reports name"}},
		{"Daily Report", 0, []string{"reports name"}},
		// Both words only in the description, not side by side.
		{"given daily", 0, []string{"reports description"}},
		// One word, two links: one by its description, one only by a
		// destination; description ranks first.
		{"daily", 0, []string{"reports description", "wiki destination"}},
		// Words may be found in different fields; the best one ranks.
		{"platform team", 0, []string{"platform-status name"}},
		{"weekly summary", 0, []string{"reports-weekly name"}},
		{"archive daily", 0, []string{"reports description"}},
		{"archive bi.example", 0, nil},
		{"archive", 0, []string{"reports destination"}},
		{"kowalska oncall", 0, []string{"oncall name"}},
		{"kowalska week", 0, []string{"oncall description"}},
		// The owner's name alone, in name order.
		{"kowalska", 0, []string{"oncall owner", "platform-status owner", "wiki owner"}},
		{"ana", 0, []string{"oncall owner", "platform-status owner", "wiki owner"}},
		// Case-folded description and destination.
		{"zoo", 0, []string{"zebra description"}},
		{"zoo.example.com/zebra", 0, []string{"zebra destination"}},
		// A word that occurs nowhere excludes a link, however well the
		// others match.
		{"daily nowhere", 0, nil},
		{"qqqq", 0, nil},
		// The limit cuts the ranked list, not the scan: the exact name
		// still comes first though it is last by name.
		{"example", 3, []string{"oncall destination", "platform-status destination", "repo destination"}},
		{"reports", 2, []string{"reports exact", "reports-weekly prefix"}},
		{"reports", 3, []string{"reports exact", "reports-weekly prefix", "report misspelled/1"}},
		// Nothing to search for.
		{"", 0, nil},
		{"   \t ", 0, nil},
		{"\x00", 0, nil},
	}
	for _, tc := range tests {
		t.Run(tc.query, func(t *testing.T) {
			limit := tc.limit
			if limit == 0 {
				limit = 20
			}
			var got []string
			for _, h := range s.Search(Query{Text: tc.query, Limit: limit, Owners: searchOwners}) {
				got = append(got, hitText(h))
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("Search(%q)\n got  %q\n want %q", tc.query, got, tc.want)
			}
		})
	}
}

// The edit thresholds are the ones configured for "Did you mean", not a
// second set of numbers.
func TestSearchUsesSuggestThresholds(t *testing.T) {
	s := Build(searchLinks())
	tests := []struct {
		name string
		opts linkmodel.SuggestOptions
		want []string
	}{
		// "reportz" has 7 characters, which allows two edits by default.
		{"defaults", linkmodel.SuggestOptions{}, []string{"report misspelled/1", "reports misspelled/1", "reprots misspelled/2"}},
		{"one edit for long names", linkmodel.SuggestOptions{Thresholds: [3]int{0, 1, 1}}, []string{"report misspelled/1", "reports misspelled/1"}},
		{"off", linkmodel.SuggestOptions{Thresholds: [3]int{-1, -1, -1}}, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var got []string
			for _, h := range s.Search(Query{Text: "reportz", Limit: 20, Suggest: tc.opts}) {
				got = append(got, hitText(h))
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// Search sees every write: a link added by With is found, one removed by
// Without is not, and a changed description is searched in its new form.
func TestSearchFollowsWrites(t *testing.T) {
	base := Build(searchLinks())
	added := base.With(linkmodel.Link{ID: 20, Name: "daily-standup", Description: "Stand-up notes", OwnerID: 1,
		Routes: []linkmodel.Route{{Pattern: "daily-standup", Destination: "https://notes.example.com"}}})
	changed := added.With(linkmodel.Link{ID: 1, Name: "reports", Description: "Monthly numbers", OwnerID: 1,
		Routes: []linkmodel.Route{{Pattern: "reports", Destination: "https://reports.example.com/"}}})
	removed := changed.Without("WIKI")

	names := func(s *Snapshot, q string) []string {
		var out []string
		for _, h := range s.Search(Query{Text: q, Limit: 20}) {
			out = append(out, hitText(h))
		}
		return out
	}
	tests := []struct {
		label string
		s     *Snapshot
		query string
		want  []string
	}{
		{"base", base, "daily", []string{"reports description", "wiki destination"}},
		{"added", added, "daily", []string{"daily-standup prefix", "reports description", "wiki destination"}},
		{"changed", changed, "daily", []string{"daily-standup prefix", "wiki destination"}},
		{"changed, new text", changed, "monthly", []string{"reports description"}},
		{"removed", removed, "daily", []string{"daily-standup prefix"}},
		// The receivers are untouched.
		{"base again", base, "monthly", nil},
	}
	for _, tc := range tests {
		if got := names(tc.s, tc.query); !slices.Equal(got, tc.want) {
			t.Errorf("%s: Search(%q) = %q, want %q", tc.label, tc.query, got, tc.want)
		}
	}
}

func TestSearchEdges(t *testing.T) {
	var nilSnapshot *Snapshot
	if got := nilSnapshot.Search(Query{Text: "x", Limit: 5}); got != nil {
		t.Errorf("nil snapshot: %v", got)
	}
	s := Build(searchLinks())
	if got := s.Search(Query{Text: "reports", Limit: 0}); got != nil {
		t.Errorf("limit 0: %v", got)
	}
	// A hit points into the snapshot.
	hits := s.Search(Query{Text: "reports", Limit: 1})
	if c, _ := s.Find("reports"); len(hits) != 1 || hits[0].Link != &c.Link {
		t.Errorf("hit does not point at the snapshot's link: %v", hits)
	}
	// Words beyond MaxQueryWords are ignored, repeated ones count once.
	many := "daily " + strings.Repeat("report ", 20) + "a b c d e f g h i j"
	if got := queryWords(many); len(got) != MaxQueryWords || got[0] != "daily" || got[1] != "report" {
		t.Errorf("queryWords = %q", got)
	}
	// A control character separates words and never matches the field
	// separator.
	if got := s.Search(Query{Text: "day\x00source", Limit: 5}); got != nil {
		t.Errorf("control characters: %v", got)
	}
}

// Searches run while writes replace the snapshot, as on a server; the race
// detector checks that a search never reads what a write is building.
func TestSearchDuringWrites(t *testing.T) {
	idx := New(Build(searchLinks()))
	done := make(chan struct{})
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-done:
					return
				default:
				}
				hits := idx.Load().Search(Query{Text: "report", Limit: 20, Owners: searchOwners})
				if len(hits) == 0 || hits[0].Link.Name != "report" {
					t.Errorf("a search during writes lost the exact name: %v", hits)
					return
				}
			}
		}()
	}
	for i := range 200 {
		name := fmt.Sprintf("report-%03d", i)
		idx.Store(idx.Load().With(linkmodel.Link{ID: int64(100 + i), Name: name, Description: "Daily",
			Routes: []linkmodel.Route{{Pattern: name, Destination: "https://example.com/" + name}}}))
		if i%2 == 0 {
			idx.Store(idx.Load().Without(name))
		}
	}
	close(done)
	wg.Wait()
}
