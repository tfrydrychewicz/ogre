package linkmodel

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// prdNames are the link names of the worked examples in PRD 5.7.
var prdNames = []string{"reports", "gh", "jira", "search", "on-call"}

func TestDefaultSuggestOptions(t *testing.T) {
	want := SuggestOptions{Thresholds: [3]int{0, 1, 2}, Limit: 5}
	if got := DefaultSuggestOptions(); got != want {
		t.Errorf("DefaultSuggestOptions() = %+v, want %+v", got, want)
	}
	if got := (SuggestOptions{}).withDefaults(); got != want {
		t.Errorf("the zero value stands for %+v, want %+v", got, want)
	}
}

func TestAllowedEdits(t *testing.T) {
	def := SuggestOptions{}
	custom := SuggestOptions{Thresholds: [3]int{1, 2, 3}}
	off := SuggestOptions{Thresholds: [3]int{-1, -1, -1}}
	tests := []struct {
		name string
		len  int
		opts SuggestOptions
		want int
	}{
		// PRD 5.6: 0 for 1-2 characters, 1 for 3-5, 2 for 6 or more.
		{"default, empty", 0, def, 0},
		{"default, 1", 1, def, 0},
		{"default, 2", 2, def, 0},
		{"default, 3", 3, def, 1},
		{"default, 4", 4, def, 1},
		{"default, 5", 5, def, 1},
		{"default, 6", 6, def, 2},
		{"default, 7", 7, def, 2},
		{"default, longest pattern", 200, def, 2},
		{"default, negative length", -1, def, 0},

		{"spelled-out defaults, 5", 5, DefaultSuggestOptions(), 1},
		{"spelled-out defaults, 6", 6, DefaultSuggestOptions(), 2},

		// Configured thresholds move the allowance, not the length classes.
		{"custom, empty", 0, custom, 0},
		{"custom, 1", 1, custom, 1},
		{"custom, 2", 2, custom, 1},
		{"custom, 3", 3, custom, 2},
		{"custom, 5", 5, custom, 2},
		{"custom, 6", 6, custom, 3},
		{"custom, 200", 200, custom, 3},

		// One non-zero entry is a configuration, not the zero value.
		{"only long names, 5", 5, SuggestOptions{Thresholds: [3]int{0, 0, 1}}, 0},
		{"only long names, 6", 6, SuggestOptions{Thresholds: [3]int{0, 0, 1}}, 1},

		// Negative means none, which is how suggestions are switched off.
		{"off, 2", 2, off, 0},
		{"off, 5", 5, off, 0},
		{"off, 6", 6, off, 0},

		// The limit has no say in the allowance.
		{"limit only, 6", 6, SuggestOptions{Limit: 1}, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := AllowedEdits(tt.len, tt.opts); got != tt.want {
				t.Errorf("AllowedEdits(%d, %+v) = %d, want %d", tt.len, tt.opts, got, tt.want)
			}
		})
	}
}

// statusNames surround "status" at every distance, listed in no useful order.
//
//	0: status
//	1: stats (delete), statue (substitute), sttaus (swap), tsatus (swap)
//	2: state (substitute and delete), statuses (two insertions)
//	3 or more: stadium, search
var statusNames = []string{
	"tsatus", "stadium", "statuses", "status", "stats", "search", "state", "sttaus", "statue",
}

func TestSuggestNames(t *testing.T) {
	sug := func(pairs ...any) []NameSuggestion {
		var out []NameSuggestion
		for i := 0; i < len(pairs); i += 2 {
			out = append(out, NameSuggestion{Name: pairs[i].(string), Edits: pairs[i+1].(int)})
		}
		return out
	}
	tests := []struct {
		name      string
		requested string
		names     []string
		opts      SuggestOptions
		want      []NameSuggestion
	}{
		// PRD 5.7.
		{"prd 6: two swapped letters", "reprots", prdNames, SuggestOptions{}, sug("reports", 1)},
		{"prd 15a: missing separator", "oncall", prdNames, SuggestOptions{}, sug("on-call", 1)},
		{"prd 15a: different separator", "on_call", prdNames, SuggestOptions{}, sug("on-call", 1)},
		{"prd 16: nothing near", "xyzzy", prdNames, SuggestOptions{}, nil},

		// The allowance by length of the requested name.
		{"two letters, one edit away: nothing", "gj", prdNames, SuggestOptions{}, nil},
		{"one letter: nothing", "g", prdNames, SuggestOptions{}, nil},
		{"empty: nothing", "", prdNames, SuggestOptions{}, nil},
		{"four letters, one edit", "jria", prdNames, SuggestOptions{}, sug("jira", 1)},
		{"four letters, two edits: nothing", "jrai", prdNames, SuggestOptions{}, nil},
		{"five letters, one edit", "serch", prdNames, SuggestOptions{}, sug("search", 1)},
		{"five letters, two edits: nothing", "sarhc", prdNames, SuggestOptions{}, nil},
		{"six letters, two edits", "saerhc", prdNames, SuggestOptions{}, sug("search", 2)},
		{"six letters, three edits: nothing", "saerhx", prdNames, SuggestOptions{}, nil},
		{"shorter by one", "report", prdNames, SuggestOptions{}, sug("reports", 1)},
		{"longer by two", "reportsss", prdNames, SuggestOptions{}, sug("reports", 2)},
		{"longer by three: nothing", "reportssss", prdNames, SuggestOptions{}, nil},

		// The requested name itself is never suggested.
		{"exact match only", "reports", prdNames, SuggestOptions{}, nil},
		{"exact match among others", "status", []string{"status", "statue"}, SuggestOptions{}, sug("statue", 1)},

		// Order: fewest edits, then by name. At most five by default.
		{
			"ordered by edits then name, cut at five", "status", statusNames, SuggestOptions{},
			sug("stats", 1, "statue", 1, "sttaus", 1, "tsatus", 1, "state", 2),
		},
		{
			"larger limit shows all", "status", statusNames, SuggestOptions{Limit: 10},
			sug("stats", 1, "statue", 1, "sttaus", 1, "tsatus", 1, "state", 2, "statuses", 2),
		},
		{
			"limit three", "status", statusNames, SuggestOptions{Limit: 3},
			sug("stats", 1, "statue", 1, "sttaus", 1),
		},
		{
			"limit one", "status", statusNames, SuggestOptions{Limit: 1},
			sug("stats", 1),
		},
		{
			"negative limit means the default", "status", statusNames, SuggestOptions{Limit: -3},
			sug("stats", 1, "statue", 1, "sttaus", 1, "tsatus", 1, "state", 2),
		},
		{
			"ties broken by name", "abcdef", []string{"abcdez", "abcdea", "abcdem", "abcde", "abcdefg"}, SuggestOptions{},
			sug("abcde", 1, "abcdea", 1, "abcdefg", 1, "abcdem", 1, "abcdez", 1),
		},
		{
			"a closer name late in the list displaces a farther one", "abcdef",
			[]string{"abcdxx", "abcdyy", "abcdzz", "abcdef0"}, SuggestOptions{Limit: 2},
			sug("abcdef0", 1, "abcdxx", 2),
		},

		// Configuration.
		{"custom thresholds allow a two-letter request", "gj", prdNames, SuggestOptions{Thresholds: [3]int{1, 1, 1}}, sug("gh", 1)},
		{"custom thresholds tighten a long request", "saerhc", prdNames, SuggestOptions{Thresholds: [3]int{0, 1, 1}}, nil},
		{"switched off", "reprots", prdNames, SuggestOptions{Thresholds: [3]int{-1, -1, -1}}, nil},

		// Edges.
		{"no names", "reprots", nil, SuggestOptions{}, nil},
		{"separators are three names", "oncall", []string{"on-call", "on_call", "on.call"}, SuggestOptions{}, sug("on-call", 1, "on.call", 1, "on_call", 1)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := append([]string(nil), tt.names...)
			got := SuggestNames(tt.requested, tt.names, tt.opts)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("SuggestNames(%q) =\n  %v, want\n  %v", tt.requested, got, tt.want)
			}
			if !reflect.DeepEqual(tt.names, before) {
				t.Errorf("SuggestNames changed its input: %v, was %v", tt.names, before)
			}
		})
	}
}

// TestSuggestNamesIgnoresInputOrder: the result is defined by distance and
// name alone, so the index may hand the names over in any order.
func TestSuggestNamesIgnoresInputOrder(t *testing.T) {
	want := SuggestNames("status", statusNames, SuggestOptions{})
	if len(want) != 5 {
		t.Fatalf("got %d suggestions for the fixture, want 5", len(want))
	}
	r := rand.New(rand.NewSource(3))
	names := append([]string(nil), statusNames...)
	for n := 0; n < 200; n++ {
		r.Shuffle(len(names), func(i, j int) { names[i], names[j] = names[j], names[i] })
		if got := SuggestNames("status", names, SuggestOptions{}); !reflect.DeepEqual(got, want) {
			t.Fatalf("names in the order %v gave\n  %v, want\n  %v", names, got, want)
		}
	}
}

// bruteForceSuggest is SuggestNames without any of its shortcuts: the full
// distance to every name, a filter, a sort, a cut.
func bruteForceSuggest(requested string, names []string, o SuggestOptions) []NameSuggestion {
	o = o.withDefaults()
	allowed := AllowedEdits(len(requested), o)
	var out []NameSuggestion
	for _, name := range names {
		if d := referenceOSA(requested, name); d > 0 && d <= allowed {
			out = append(out, NameSuggestion{Name: name, Edits: d})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Edits != out[j].Edits {
			return out[i].Edits < out[j].Edits
		}
		return out[i].Name < out[j].Name
	})
	if len(out) > o.Limit {
		out = out[:o.Limit]
	}
	return out
}

// TestSuggestNamesMatchesBruteForce: the length filter, the bounded distance
// and the tightening bound are optimizations only. Small alphabets and short
// names make near misses, ties and overfull result lists the normal case.
func TestSuggestNamesMatchesBruteForce(t *testing.T) {
	r := rand.New(rand.NewSource(20261013))
	optionSets := []SuggestOptions{
		{},
		{Limit: 1},
		{Limit: 3},
		{Limit: 1000},
		{Thresholds: [3]int{1, 2, 3}},
		{Thresholds: [3]int{2, 2, 4}, Limit: 2},
		{Thresholds: [3]int{0, 0, 1}},
	}
	for round := 0; round < 60; round++ {
		alphabet := []string{"ab", "abc-", "abcdefgh-_"}[r.Intn(3)]
		maxLen := 3 + r.Intn(8)
		// Distinct names, as in the index. The attempts are capped because a
		// two-letter alphabet has fewer short strings than the target.
		target := 1 + r.Intn(400)
		seen := map[string]bool{}
		var names []string
		for attempts := 0; len(names) < target && attempts < 4*target; attempts++ {
			_, name := randomPair(r, alphabet, maxLen)
			if !seen[name] {
				seen[name] = true
				names = append(names, name)
			}
		}
		for q := 0; q < 10; q++ {
			requested, _ := randomPair(r, alphabet, maxLen)
			for _, o := range optionSets {
				got := SuggestNames(requested, names, o)
				want := bruteForceSuggest(requested, names, o)
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("SuggestNames(%q, %d names, %+v) =\n  %v, want\n  %v", requested, len(names), o, got, want)
				}
			}
		}
	}
}

// benchWords is the vocabulary of the generated names: the kind of words go
// links are made of.
var benchWords = strings.Fields(`
	api app auth admin alerts backlog billing board build calendar chat ci
	cloud config console dash data deploy design dev docs eng events expenses
	feedback finance forms gh git grafana help hr infra issues jira jobs
	kb kibana lab launch legal logs mail map metrics mobile monitor news
	notes okr on call ops org pager payroll perf plan platform policy portal
	pr prod qa queue release reports repo review roadmap runbook sales search
	security sprint staging standup status store support team test tickets
	time tools travel vpn web wiki zoom
`)

// benchNames generates n distinct lowercase link names of realistic shape and
// length: one to three words, joined by "-", "_" or nothing, some with a
// number. They share a vocabulary, as real names do, so many of them have a
// prefix in common with any requested name - harder on the scan than random
// letters would be. Sorted, as the index hands them over.
func benchNames(n int) []string {
	r := rand.New(rand.NewSource(1))
	seen := make(map[string]struct{}, n)
	names := make([]string, 0, n)
	for len(names) < n {
		var sb strings.Builder
		sep := []string{"-", "-", "_", ""}[r.Intn(4)]
		for w, words := 0, 1+r.Intn(3); w < words; w++ {
			if w > 0 {
				sb.WriteString(sep)
			}
			sb.WriteString(benchWords[r.Intn(len(benchWords))])
		}
		if r.Intn(3) == 0 {
			fmt.Fprintf(&sb, "%s%d", sep, r.Intn(500))
		}
		name := sb.String()
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// TestBenchNames keeps the benchmarks honest: the generated names are what
// they claim to be.
func TestBenchNames(t *testing.T) {
	names := benchNames(5000)
	if len(names) != 5000 {
		t.Fatalf("got %d names, want 5000", len(names))
	}
	seen := map[string]bool{}
	for _, name := range names {
		if seen[name] {
			t.Fatalf("name %q generated twice", name)
		}
		seen[name] = true
		if name != strings.ToLower(name) || len(name) < 2 || len(name) > 40 {
			t.Fatalf("name %q is not a realistic lowercase name", name)
		}
	}
}

// BenchmarkSuggest100k is the suggestion part of an unknown-name miss at
// 100,000 links (plan L2.13: the whole miss stays under 50 ms in process): a
// requested name one swap away from an existing one.
func BenchmarkSuggest100k(b *testing.B) {
	names := benchNames(100_000)
	const existing, requested = "platform-reports", "platform-reprots"
	if i := sort.SearchStrings(names, existing); i == len(names) || names[i] != existing {
		names[0] = existing // the generator is seeded, so this is settled once; keep the benchmark meaningful either way
		sort.Strings(names)
	}
	got := SuggestNames(requested, names, SuggestOptions{})
	if len(got) == 0 || got[0] != (NameSuggestion{Name: existing, Edits: 1}) {
		b.Fatalf("SuggestNames(%q) = %v, want %q first with 1 edit", requested, got, existing)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		SuggestNames(requested, names, SuggestOptions{})
	}
}

// BenchmarkSuggest100kNoMatch is the same scan when nothing is near, so the
// bound never tightens: PRD example 16 at scale.
func BenchmarkSuggest100kNoMatch(b *testing.B) {
	names := benchNames(100_000)
	const requested = "xyzzy-quux-wobble"
	if got := SuggestNames(requested, names, SuggestOptions{}); len(got) != 0 {
		b.Fatalf("SuggestNames(%q) = %v, want nothing", requested, got)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		SuggestNames(requested, names, SuggestOptions{})
	}
}
