package app

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/tfrydrychewicz/ogre/internal/index"
	"github.com/tfrydrychewicz/ogre/internal/linkmodel"
)

// memLink builds a link the way a save would (ValidateDraft), for an App
// that only resolves: description, then pattern and destination pairs.
func memLink(t testing.TB, id int64, description string, pairs ...string) linkmodel.Link {
	t.Helper()
	d := draft(pairs...)
	d.Description = description
	l, problems := linkmodel.ValidateDraft(d)
	if problems.HasErrors() {
		first, _ := problems.First()
		t.Fatalf("memLink(%v): %s", pairs, first.Message)
	}
	l.ID, l.Version, l.OwnerID = id, 1, 1
	for i := range l.Routes {
		l.Routes[i].ID = id*100 + int64(i)
	}
	return l
}

// resolver returns an App with the given links in its index and no store at
// all. Resolve must work on it: a call that reached for the store would
// panic on the nil interface.
func resolver(opts Options, links ...linkmodel.Link) *App {
	return &App{index: index.New(index.Build(links)), suggest: opts.Suggest}
}

// named returns one single-route link per name, each leading to its own URL.
func named(t testing.TB, names ...string) []linkmodel.Link {
	t.Helper()
	var links []linkmodel.Link
	for i, name := range names {
		links = append(links, memLink(t, int64(i+1), "About "+name, name, "https://example.com/"+name))
	}
	return links
}

func suggestionNames(d Decision) []string {
	var names []string
	for _, s := range d.Suggestions {
		names = append(names, fmt.Sprintf("%s:%d", s.Name, s.Edits))
	}
	return names
}

// PRD 5.6: when the rest of the request resolves against a suggested link,
// the suggestion is the complete corrected request; otherwise it is the bare
// name.
func TestSuggestionsCarryValuesOver(t *testing.T) {
	a := resolver(Options{},
		memLink(t, 1, "Daily reports",
			"reports/{month:regex(^(0?[1-9]|1[0-2])$)}/{day:int}", "https://reports.example.com/daily?month={month}&day={day}"),
		memLink(t, 2, "Search", "search/{q}", "https://search.example.com/?q={q}"),
		memLink(t, 3, "Jira issues", "jira/{key:regex([A-Z]+-[0-9]+)}", "https://acme.atlassian.net/browse/{key}"),
		memLink(t, 4, "GitHub",
			"github", "https://github.com/acme",
			"github/{repo}", "https://github.com/acme/{repo}",
			"github/{repo}/pr/{num:int}", "https://github.com/acme/{repo}/pull/{num}"),
		memLink(t, 5, "On-call schedule", "on-call", "https://pager.example.com/schedule"),
	)

	tests := []struct {
		name string
		path string
		want Suggestion
	}{
		{"both values carried over", "/reprots/10/13",
			Suggestion{Name: "reports", Path: "reports/10/13", Edits: 1, Resolves: true,
				Destination: "https://reports.example.com/daily?month=10&day=13", Description: "Daily reports"}},
		{"the name's case is corrected too, a trailing slash dropped", "/RePrOtS/10/13/",
			Suggestion{Name: "reports", Path: "reports/10/13", Edits: 1, Resolves: true,
				Destination: "https://reports.example.com/daily?month=10&day=13", Description: "Daily reports"}},
		{"a value keeps its case", "/jiar/OPS-12",
			Suggestion{Name: "jira", Path: "jira/OPS-12", Edits: 1, Resolves: true,
				Destination: "https://acme.atlassian.net/browse/OPS-12", Description: "Jira issues"}},
		{"a value is encoded again in the path and in the destination", "/serach/a%20b%2Fc%3Fd%26e",
			Suggestion{Name: "search", Path: "search/a%20b%2Fc%3Fd&e", Edits: 1, Resolves: true,
				Destination: "https://search.example.com/?q=a%20b%2Fc%3Fd%26e", Description: "Search"}},
		{"a literal of the rest is matched ignoring case", "/githbu/ogre/PR/7",
			Suggestion{Name: "github", Path: "github/ogre/PR/7", Edits: 1, Resolves: true,
				Destination: "https://github.com/acme/ogre/pull/7", Description: "GitHub"}},
		{"nothing after the name, and the bare name resolves", "/oncall",
			Suggestion{Name: "on-call", Path: "on-call", Edits: 1, Resolves: true,
				Destination: "https://pager.example.com/schedule", Description: "On-call schedule"}},
		{"the query of the request is not carried", "/gihtub/ogre?tab=readme",
			Suggestion{Name: "github", Path: "github/ogre", Edits: 1, Resolves: true,
				Destination: "https://github.com/acme/ogre", Description: "GitHub"}},

		// The rest does not resolve: the suggestion is the bare name, and it
		// claims no destination.
		{"a value its variable rejects", "/reprots/13/01",
			Suggestion{Name: "reports", Path: "reports", Edits: 1, Description: "Daily reports"}},
		{"too few values", "/reprots/10",
			Suggestion{Name: "reports", Path: "reports", Edits: 1, Description: "Daily reports"}},
		{"nothing after the name, and the link has no bare route", "/reprots",
			Suggestion{Name: "reports", Path: "reports", Edits: 1, Description: "Daily reports"}},
		{"too many segments, though the bare name would resolve", "/githbu/a/b/c/d",
			Suggestion{Name: "github", Path: "github", Edits: 1, Description: "GitHub"}},
		{"a value that is lower case where the route wants upper", "/jiar/ops-12",
			Suggestion{Name: "jira", Path: "jira", Edits: 1, Description: "Jira issues"}},
		// A browser would remove these segments from the corrected link.
		{"a dot-dot value is not carried", "/serach/..",
			Suggestion{Name: "search", Path: "search", Edits: 1, Description: "Search"}},
		{"a dot value is not carried", "/githbu/./pr/7",
			Suggestion{Name: "github", Path: "github", Edits: 1, Description: "GitHub"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := a.Resolve(tt.path)
			if d.Kind != DecisionMissUnknown || len(d.Suggestions) != 1 {
				t.Fatalf("Resolve(%q) = %+v, want an unknown-name miss with one suggestion", tt.path, d)
			}
			got := bare(t, d.Suggestions)[0]
			if got != tt.want {
				t.Errorf("suggestion = %+v\n               want %+v", got, tt.want)
			}
			// A suggestion never redirects by itself (PRD 5.6), and what it
			// promises holds: following Path gives exactly Destination.
			if d.Destination != "" {
				t.Errorf("the miss itself carries a destination: %q", d.Destination)
			}
			followed := a.Resolve("/" + got.Path)
			if got.Resolves {
				if followed.Kind != DecisionHit || followed.Destination != got.Destination {
					t.Errorf("following %q = %v %q, want a hit on %q", got.Path, followed.Kind, followed.Destination, got.Destination)
				}
			} else if got.Destination != "" || got.Path != got.Name {
				t.Errorf("a suggestion that does not resolve has path %q and destination %q", got.Path, got.Destination)
			}
		})
	}
}

// PRD 5.6: the allowed distance grows with the length of the requested name:
// 0 for 1-2 characters, 1 for 3-5, 2 for 6 or more. Both that and the limit
// are configuration.
func TestSuggestionThresholdsByNameLength(t *testing.T) {
	links := named(t, "g", "gh", "ab", "jira", "wiki", "roadmap", "reports", "on-call")
	off := Options{Suggest: linkmodel.SuggestOptions{Thresholds: [3]int{-1, -1, -1}}}
	wide := Options{Suggest: linkmodel.SuggestOptions{Thresholds: [3]int{1, 2, 3}}}

	tests := []struct {
		name      string
		opts      Options
		requested string
		want      []string // name:edits, in order
	}{
		// 1-2 characters: nothing but the name itself would do.
		{"one character", Options{}, "h", nil},
		{"two characters, one edit away", Options{}, "gg", nil},
		{"two characters, a swap away", Options{}, "ba", nil},
		// 3-5 characters: one edit.
		{"three characters, one missing", Options{}, "jia", []string{"jira:1"}},
		{"three characters, two missing", Options{}, "roa", nil},
		{"four characters, one substituted", Options{}, "jirb", []string{"jira:1"}},
		{"four characters, a swap", Options{}, "wkii", []string{"wiki:1"}},
		{"five characters, one inserted", Options{}, "wikii", []string{"wiki:1"}},
		{"four characters, two edits", Options{}, "jxrb", nil},
		{"five characters, two edits", Options{}, "wikxx", nil},
		// 6 or more: two edits.
		{"six characters, two edits", Options{}, "wikixx", []string{"wiki:2"}},
		{"seven characters, two edits", Options{}, "rodamxp", []string{"roadmap:2"}},
		{"seven characters, three edits", Options{}, "rxxdmxp", nil},
		{"a missing separator is one edit", Options{}, "oncall", []string{"on-call:1"}},
		{"another separator is one edit", Options{}, "on_call", []string{"on-call:1"}},
		{"case is no edit, so the name is found, not suggested", Options{}, "ROADMAP", nil},

		// Configured.
		{"suggestions switched off", off, "jirb", nil},
		{"suggestions switched off, long name", off, "oncall", nil},
		{"wider: two characters, one edit", wide, "gg", []string{"g:1", "gh:1"}},
		{"wider: four characters, two edits", wide, "jxrb", []string{"jira:2"}},
		{"wider: seven characters, three edits", wide, "rxxdmxp", []string{"roadmap:3"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := resolver(tt.opts, links...).Resolve("/" + tt.requested)
			if tt.name == "case is no edit, so the name is found, not suggested" {
				if d.Kind != DecisionHit {
					t.Errorf("Resolve(%q) = %v, want a hit", tt.requested, d.Kind)
				}
				return
			}
			if d.Kind != DecisionMissUnknown || d.RequestedName != strings.ToLower(tt.requested) {
				t.Fatalf("Resolve(%q) = %v, requested %q; want an unknown-name miss", tt.requested, d.Kind, d.RequestedName)
			}
			if got := suggestionNames(d); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("suggestions for %q = %v, want %v", tt.requested, got, tt.want)
			}
		})
	}
}

// PRD 5.6: fewest edits first, then alphabetically; at most five.
func TestSuggestionOrderAndLimit(t *testing.T) {
	// Around "reportz": nine names one substitution away, one a deletion
	// away, one that sorts before them all, and one two edits away that
	// would sort first of all by name.
	names := []string{"aaportz", "aeportz", "report", "reports"}
	for i := 9; i >= 1; i-- {
		names = append(names, fmt.Sprintf("report%d", i))
	}
	names = append(names, "unrelated", "rep", "zzzzzzz")
	links := named(t, names...)

	tests := []struct {
		name  string
		limit int
		want  []string
	}{
		{"the default is five", 0, []string{"aeportz:1", "report:1", "report1:1", "report2:1", "report3:1"}},
		{"a limit of two", 2, []string{"aeportz:1", "report:1"}},
		{"a limit of one", 1, []string{"aeportz:1"}},
		{"a limit that takes them all: the two-edit name comes last, whatever its name", 50, []string{
			"aeportz:1", "report:1", "report1:1", "report2:1", "report3:1", "report4:1", "report5:1",
			"report6:1", "report7:1", "report8:1", "report9:1", "reports:1", "aaportz:2"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := resolver(Options{Suggest: linkmodel.SuggestOptions{Limit: tt.limit}}, links...)
			d := a.Resolve("/reportz")
			if got := suggestionNames(d); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("suggestions = %v\n        want %v", got, tt.want)
			}
			for _, s := range d.Suggestions {
				if !s.Resolves || s.Path != s.Name || s.Destination != "https://example.com/"+s.Name || s.Description != "About "+s.Name {
					t.Errorf("suggestion %+v does not describe its link", s)
				}
			}
		})
	}

	// Among several candidates, each is checked against its own link: some
	// take the rest of the request and some do not, and the order stays by
	// edits and name.
	a := resolver(Options{},
		memLink(t, 1, "", "team", "https://example.com/team"),
		memLink(t, 2, "", "teen/{name}", "https://example.com/teen/{name}"),
		memLink(t, 3, "", "tram/{line:int}", "https://example.com/tram/{line}"),
		memLink(t, 4, "", "term/{word}/{lang}", "https://example.com/{lang}/{word}"),
	)
	got := bare(t, a.Resolve("/teem/blue").Suggestions)
	want := []Suggestion{
		{Name: "team", Path: "team", Edits: 1},
		{Name: "teen", Path: "teen/blue", Edits: 1, Resolves: true, Destination: "https://example.com/teen/blue"},
		{Name: "term", Path: "term", Edits: 1},
		// tram is two edits from "teem", and a four-letter name allows one.
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("suggestions for teem/blue = %+v\n                     want %+v", got, want)
	}
}

// Nothing is suggested for a first segment that could not be a name, however
// close it is to one: nothing of such a segment is repeated (NFR-17), and an
// empty index suggests nothing.
func TestNoSuggestionsWithoutAName(t *testing.T) {
	a := resolver(Options{}, named(t, "reports", "roadmap")...)
	for _, path := range []string{
		"/reports%20", "/reports%2Fx", "/-reports", "/report%C5%9B", "/reports%0A", "/" + strings.Repeat("r", 300),
	} {
		d := a.Resolve(path)
		if d.Kind != DecisionMissUnknown || d.RequestedName != "" || d.LinkName != "" || d.Suggestions != nil {
			t.Errorf("Resolve(%q) = %+v, want an unknown-name miss that names and suggests nothing", path, d)
		}
	}
	for _, empty := range []*App{resolver(Options{}), {index: index.New(nil)}} {
		if d := empty.Resolve("/reports"); d.Kind != DecisionMissUnknown || d.Suggestions != nil || d.RequestedName != "reports" {
			t.Errorf("Resolve on an empty index = %+v", d)
		}
	}
}

// Computing suggestions leaves the request and the index alone: the same
// miss twice gives the same answer, and the link it suggests still resolves
// under its own name only.
func TestSuggestionsDoNotChangeAnything(t *testing.T) {
	a := resolver(Options{}, memLink(t, 1, "Daily reports", "reports/{m:int}", "https://example.com/r/{m}"))
	first := a.Resolve("/reprots/7")
	second := a.Resolve("/reprots/7")
	if !reflect.DeepEqual(first, second) || len(first.Suggestions) != 1 {
		t.Errorf("the same miss answered differently:\n%+v\n%+v", first, second)
	}
	if d := a.Resolve("/reports/7"); d.Kind != DecisionHit || d.Link.Name != "reports" {
		t.Errorf("Resolve(/reports/7) after a miss = %+v", d)
	}
}

// bare checks that every suggestion carries its link, the one in the index
// with the suggested name and its routes, and returns the suggestions without
// it, so that a test can compare the rest as plain values.
func bare(t *testing.T, suggestions []Suggestion) []Suggestion {
	t.Helper()
	if suggestions == nil {
		return nil
	}
	out := make([]Suggestion, len(suggestions))
	for i, s := range suggestions {
		if s.Link == nil || s.Link.Name != s.Name || s.Link.Description != s.Description || len(s.Link.Routes) == 0 {
			t.Errorf("suggestion %q does not carry its link: %+v", s.Name, s.Link)
		}
		s.Link = nil
		out[i] = s
	}
	return out
}
