package app

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/tfrydrychewicz/ogre/internal/index"
	"github.com/tfrydrychewicz/ogre/internal/linkmodel"
)

// benchLinkCount is the size the plan sets its targets at (L2.13): a hit
// under 1 ms and an unknown-name miss under 50 ms, in process.
const benchLinkCount = 100_000

var benchWords = []string{
	"api", "auth", "billing", "board", "build", "calendar", "chat", "ci", "cloud", "config",
	"dash", "data", "deploy", "design", "docs", "eng", "finance", "git", "grafana", "help",
	"hr", "infra", "issues", "jira", "kb", "logs", "mail", "metrics", "oncall", "ops",
	"pager", "people", "platform", "portal", "release", "reports", "roadmap", "runbook", "search", "security",
	"sre", "staging", "status", "support", "team", "tickets", "tools", "vault", "wiki", "zoom",
}

// benchLinks generates n links with names that look like real go links: one
// to three words joined by "-", "_" or nothing, some numbered. Seven in ten
// are a single URL, two in ten take a text value, and one in ten has four
// routes with an int and a regex variable. The same n always gives the same
// links. A few links with known names and shapes are put among them for the
// benchmarks to ask for.
func benchLinks(n int) []linkmodel.Link {
	links := make([]linkmodel.Link, 0, n)
	seen := make(map[string]bool, n)
	var nextRoute int64
	add := func(name, description string, pairs ...string) {
		if seen[name] {
			return
		}
		seen[name] = true
		l := linkmodel.Link{ID: int64(len(links) + 1), Name: name, Description: description, OwnerID: 1, Version: 1}
		for i := 0; i+1 < len(pairs); i += 2 {
			nextRoute++
			l.Routes = append(l.Routes, linkmodel.Route{ID: nextRoute, Position: i / 2, Pattern: pairs[i], Destination: pairs[i+1]})
		}
		links = append(links, l)
	}

	add("platform-status", "Status page", "platform-status", "https://status.example.com/platform")
	add("platform-reports", "Daily reports",
		"platform-reports/{month:regex(^(0?[1-9]|1[0-2])$)}/{day:int}", "https://reports.example.com/daily?month={month}&day={day}")
	// A link with the most routes a link can have, for the worst known-name
	// miss: every route has to say why it did not fit.
	wide := []string{"monorepo", "https://git.example.com/mono"}
	for i := 0; i < linkmodel.MaxRoutes-1; i++ {
		kind := []string{"{id:int}", "{slug:regex([a-z]+-[0-9]+)}", "{name}"}[i%3]
		wide = append(wide, fmt.Sprintf("monorepo/area%d/%s", i, kind), fmt.Sprintf("https://git.example.com/mono/area%d/{%s}",
			i, strings.SplitN(strings.Trim(kind, "{}"), ":", 2)[0]))
	}
	add("monorepo", "The monorepo", wide...)

	// A fixed linear congruential generator: the same links on every run and
	// every machine, without math/rand's version-dependent streams.
	state := uint64(42)
	pick := func(k int) int {
		state = state*6364136223846793005 + 1442695040888963407
		return int((state >> 33) % uint64(k))
	}
	for len(links) < n {
		var b strings.Builder
		for w, words := 0, 1+pick(3); w < words; w++ {
			if w > 0 {
				b.WriteString([]string{"-", "_", ""}[pick(3)])
			}
			b.WriteString(benchWords[pick(len(benchWords))])
		}
		if pick(4) == 0 {
			fmt.Fprintf(&b, "%d", pick(100))
		}
		name := b.String()
		base := "https://" + name + ".example.com"
		switch pick(10) {
		case 0:
			add(name, "About "+name,
				name, base,
				name+"/new", base+"/new",
				name+"/{id:int}", base+"/items/{id}",
				name+"/{key:regex([A-Z]+-[0-9]+)}/{tab}", base+"/browse/{key}?tab={tab}")
		case 1, 2:
			add(name, "About "+name, name+"/{q}", base+"/search?q={q}")
		default:
			add(name, "About "+name, name, base)
		}
	}
	return links
}

var (
	benchOnce sync.Once
	benchApp  *App
)

// benchResolver returns an App with benchLinkCount links in its index, built
// once for all benchmarks. It has no store: Resolve does not use one.
func benchResolver(b *testing.B) *App {
	b.Helper()
	benchOnce.Do(func() {
		benchApp = &App{index: index.New(index.Build(benchLinks(benchLinkCount)))}
	})
	if n := benchApp.index.Load().Len(); n != benchLinkCount {
		b.Fatalf("the benchmark index holds %d links, want %d", n, benchLinkCount)
	}
	return benchApp
}

// BenchmarkResolve100k measures a whole decision, from the raw path to the
// Decision, against 100,000 links.
func BenchmarkResolve100k(b *testing.B) {
	a := benchResolver(b)

	cases := []struct {
		name string
		path string
		kind DecisionKind
		// For an unknown-name miss: how many suggestions, and how many of
		// them carry the values over.
		suggestions, resolving int
	}{
		{"hit/literal", "/platform-status", DecisionHit, 0, 0},
		{"hit/regex_and_int", "/Platform-Reports/10/13", DecisionHit, 0, 0},
		{"hit/20_routes", "/monorepo/area16/ogre-7", DecisionHit, 0, 0},
		{"miss_known/1_route", "/platform-reports/13/01", DecisionMissKnown, 0, 0},
		{"miss_known/20_routes", "/monorepo/area16/a/b", DecisionMissKnown, 0, 0},
		// Nothing within two edits: every name is looked at and rejected.
		{"miss_unknown/no_near_name", "/qzxv-jkwy-qqfh/10/13", DecisionMissUnknown, 0, 0},
		// One swap away from platform-reports; the values are carried over.
		{"miss_unknown/near_name_values_carried", "/platform-reprots/10/13", DecisionMissUnknown, -1, 1},
		// A short, common word with a typo: the limit of five is reached
		// and each candidate is tried.
		{"miss_unknown/crowded", "/teamz7/x", DecisionMissUnknown, 5, -1},
	}
	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			// The case measures what its name says.
			d := a.Resolve(tc.path)
			resolving := 0
			for _, s := range d.Suggestions {
				if s.Resolves {
					resolving++
				}
			}
			if d.Kind != tc.kind ||
				(tc.suggestions >= 0 && len(d.Suggestions) != tc.suggestions) ||
				(tc.resolving >= 0 && resolving != tc.resolving) {
				b.Fatalf("Resolve(%s) = %v with %d suggestions (%d resolving): %+v", tc.path, d.Kind, len(d.Suggestions), resolving, d.Suggestions)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if d := a.Resolve(tc.path); d.Kind != tc.kind {
					b.Fatalf("got %v", d.Kind)
				}
			}
		})
	}

	// Many goroutines resolving at once, as a server does: readers share the
	// snapshot and take no lock.
	b.Run("parallel/hit_and_miss", func(b *testing.B) {
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			for i := 0; pb.Next(); i++ {
				if i%10 == 0 {
					a.Resolve("/platform-reprots/10/13")
				} else {
					a.Resolve("/platform-reports/10/13")
				}
			}
		})
	})
}

// BenchmarkSnapshotWrite100k measures what a write adds on top of the store's own
// work at 100,000 links: deriving the next snapshot.
func BenchmarkSnapshotWrite100k(b *testing.B) {
	snapshot := benchResolver(b).index.Load()
	l := linkmodel.Link{ID: benchLinkCount + 1, Name: "brand-new", Version: 1,
		Routes: []linkmodel.Route{{ID: 1, Pattern: "brand-new/{x:int}", Destination: "https://example.com/{x}"}}}
	b.Run("with", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			snapshot.With(l)
		}
	})
	b.Run("without", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			snapshot.Without("platform-status")
		}
	})
}

// The generated links are what the benchmarks assume: all valid by the save
// rules, all different, all compiled. Checked on a sample small enough for
// every test run.
func TestBenchLinksAreValid(t *testing.T) {
	links := benchLinks(2000)
	if len(links) != 2000 {
		t.Fatalf("benchLinks(2000) returned %d links", len(links))
	}
	a := &App{index: index.New(index.Build(links))}
	if a.index.Load().Len() != len(links) {
		t.Fatalf("the index holds %d of %d links: names repeat", a.index.Load().Len(), len(links))
	}
	for _, l := range links {
		d := Draft{Description: l.Description}
		for _, r := range l.Routes {
			d.Routes = append(d.Routes, DraftRoute{Pattern: r.Pattern, Destination: r.Destination})
		}
		valid, problems := linkmodel.ValidateDraft(d)
		if problems.HasErrors() || valid.Name != l.Name {
			first, _ := problems.First()
			t.Fatalf("generated link %s is not valid: %s", l.Name, first.Message)
		}
		for i, r := range valid.Routes {
			if r.Pattern != l.Routes[i].Pattern {
				t.Fatalf("generated pattern %q is not canonical (%q)", l.Routes[i].Pattern, r.Pattern)
			}
		}
	}
	if d := a.Resolve("/monorepo/area16/ogre-7"); d.Kind != DecisionHit || d.Destination != "https://git.example.com/mono/area16/ogre-7" {
		t.Errorf("the 20-route link answers %+v", brief(d))
	}
	if d := a.Resolve("/monorepo/area16/a/b"); d.Kind != DecisionMissKnown || len(d.Reasons) != linkmodel.MaxRoutes {
		t.Errorf("the 20-route link misses with %d reasons", len(d.Reasons))
	}
}
