package app

import (
	"context"
	"testing"

	"github.com/tfrydrychewicz/ogre/internal/store"
)

// BenchmarkSearch100k measures a whole search at 100,000 links (plan L3.2:
// under 50 ms in process), from the query text to the copied results. The
// links are those of the resolver benchmarks (benchLinks): names of one to
// three common words, each described as "About <name>", plus a few known
// ones such as platform-reports, "Daily reports".
func BenchmarkSearch100k(b *testing.B) {
	// The resolver's benchmark App has no store; this one shares its index
	// and knows the local user, which Search reads for owner names.
	a := &App{
		index: benchResolver(b).index,
		local: &store.User{ID: 1, DisplayName: "Local user", Role: "admin"},
	}
	ctx := context.Background()

	cases := []struct {
		name, query string
		atLeast     int // results expected, so the case measures what it says
	}{
		// One common word: prefix, name and description matches by the
		// thousand, cut to the default limit of 20.
		{"one_word/common", "reports", 20},
		// One word found nowhere: every link is scanned in full.
		{"one_word/none", "qzxvjk", 0},
		// One misspelled word: the edit distance runs for every name of a
		// near length.
		{"one_word/misspelled", "platfrom-status", 1},
		// Scenario 6's query: two words, both must occur.
		{"two_words/scenario_6", "daily report", 1},
		// Two common words, found together in thousands of links.
		{"two_words/common", "about team", 20},
		{"two_words/none", "about qzxvjk", 0},
	}
	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			results, err := a.Search(ctx, localActor, tc.query, 0)
			if err != nil || len(results) < tc.atLeast || (tc.atLeast == 0 && len(results) != 0) {
				b.Fatalf("Search(%q) = %d results, %v; want at least %d", tc.query, len(results), err, tc.atLeast)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := a.Search(ctx, localActor, tc.query, 0); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkHomeLists100k measures the two lists of the home page at 100,000
// links, all owned by the local user, which is the worst case for "your
// links".
func BenchmarkHomeLists100k(b *testing.B) {
	a := &App{index: benchResolver(b).index}
	ctx := context.Background()
	b.Run("your_links", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if links, _ := a.YourLinks(ctx, localActor, 0); len(links) != DefaultListLimit {
				b.Fatalf("got %d links", len(links))
			}
		}
	})
	b.Run("recent_links", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if links, _ := a.RecentLinks(ctx, 0); len(links) != DefaultListLimit {
				b.Fatalf("got %d links", len(links))
			}
		}
	})
}
