package index

import (
	"fmt"
	"sync"
	"testing"

	"github.com/tfrydrychewicz/ogre/internal/linkmodel"
)

func link(id int64, name string, routes ...linkmodel.Route) linkmodel.Link {
	if len(routes) == 0 {
		routes = []linkmodel.Route{{ID: id * 10, Pattern: name, Destination: "https://example.com/" + name}}
	}
	return linkmodel.Link{ID: id, Name: name, Version: 1, Routes: routes}
}

func TestBuildAndLookup(t *testing.T) {
	s := Build([]linkmodel.Link{link(1, "on-call"), link(2, "gh"), link(3, "roadmap")})

	if got := s.Len(); got != 3 {
		t.Errorf("Len() = %d, want 3", got)
	}

	tests := []struct {
		name   string
		wantID int64
		wantOK bool
	}{
		{"on-call", 1, true},
		{"On-Call", 1, true},
		{"ON-CALL", 1, true},
		{"gh", 2, true},
		{"GH", 2, true},
		{"roadmap", 3, true},

		// Q5: only case is ignored.
		{"oncall", 0, false},
		{"on_call", 0, false},
		{"on.call", 0, false},
		{"on-call ", 0, false},
		{"g", 0, false},
		{"ghx", 0, false},
		{"", 0, false},
		{"K", 0, false},
	}
	for _, tt := range tests {
		got, ok := s.Lookup(tt.name)
		if ok != tt.wantOK || got.ID != tt.wantID {
			t.Errorf("Lookup(%q) = link %d, %v; want link %d, %v", tt.name, got.ID, ok, tt.wantID, tt.wantOK)
		}
	}
}

// A name that reaches Build in mixed case (a database edited by hand, say)
// is still found: the key is normalized, not trusted.
func TestBuildNormalizesKeys(t *testing.T) {
	s := Build([]linkmodel.Link{link(1, "Roadmap")})
	if _, ok := s.Lookup("roadmap"); !ok {
		t.Error(`Lookup("roadmap") did not find the link stored as "Roadmap"`)
	}
}

func TestEmptySnapshots(t *testing.T) {
	var nilSnapshot *Snapshot
	for name, s := range map[string]*Snapshot{
		"Build(nil)": Build(nil),
		"zero value": {},
		"nil":        nilSnapshot,
	} {
		if got := s.Len(); got != 0 {
			t.Errorf("%s: Len() = %d, want 0", name, got)
		}
		if _, ok := s.Lookup("gh"); ok {
			t.Errorf("%s: Lookup found a link in an empty snapshot", name)
		}
		next := s.With(link(1, "gh"))
		if _, ok := next.Lookup("gh"); !ok || next.Len() != 1 {
			t.Errorf("%s: With did not add the link (Len %d)", name, next.Len())
		}
		if s.Len() != 0 {
			t.Errorf("%s: With changed the receiver", name)
		}
	}
}

func TestWithDoesNotMutateReceiver(t *testing.T) {
	base := Build([]linkmodel.Link{link(1, "gh"), link(2, "roadmap")})

	added := base.With(link(3, "wiki"))
	if _, ok := base.Lookup("wiki"); ok {
		t.Error("With added the link to the receiver")
	}
	if base.Len() != 2 {
		t.Errorf("receiver Len() = %d after With, want 2", base.Len())
	}
	if _, ok := added.Lookup("wiki"); !ok || added.Len() != 3 {
		t.Errorf("new snapshot: Lookup(wiki) ok=%v, Len()=%d; want true, 3", ok, added.Len())
	}
	for _, name := range []string{"gh", "roadmap"} {
		if _, ok := added.Lookup(name); !ok {
			t.Errorf("new snapshot lost %q", name)
		}
	}

	// Replacing a link leaves the old version visible in the old snapshot.
	replaced := added.With(linkmodel.Link{ID: 1, Name: "GH", Version: 2,
		Routes: []linkmodel.Route{{Pattern: "gh", Destination: "https://example.com/v2"}}})
	if replaced.Len() != 3 {
		t.Errorf("Len() = %d after replacing a link, want 3", replaced.Len())
	}
	if got, _ := replaced.Lookup("gh"); got.Version != 2 || got.Routes[0].Destination != "https://example.com/v2" {
		t.Errorf("new snapshot has %+v, want version 2", got)
	}
	if got, _ := added.Lookup("gh"); got.Version != 1 || got.Routes[0].Destination != "https://example.com/gh" {
		t.Errorf("old snapshot changed to %+v, want version 1", got)
	}
	if got, _ := base.Lookup("gh"); got.Version != 1 {
		t.Errorf("base snapshot changed to %+v, want version 1", got)
	}
}

// The snapshot keeps its own routes: changing the slice that was passed in
// must not change what the resolver sees.
func TestSnapshotOwnsItsRoutes(t *testing.T) {
	routes := []linkmodel.Route{{Pattern: "gh", Destination: "https://example.com/before"}}

	built := Build([]linkmodel.Link{{ID: 1, Name: "gh", Routes: routes}})
	with := Build(nil).With(linkmodel.Link{ID: 1, Name: "gh", Routes: routes})
	routes[0].Destination = "https://evil.example/after"

	for name, s := range map[string]*Snapshot{"Build": built, "With": with} {
		got, _ := s.Lookup("gh")
		if got.Routes[0].Destination != "https://example.com/before" {
			t.Errorf("%s: snapshot route changed to %q through the caller's slice", name, got.Routes[0].Destination)
		}
	}
}

func TestIndex(t *testing.T) {
	idx := New(nil)
	if s := idx.Load(); s == nil || s.Len() != 0 {
		t.Fatalf("New(nil).Load() = %v, want an empty snapshot", s)
	}

	first := Build([]linkmodel.Link{link(1, "gh")})
	idx.Store(first)
	if idx.Load() != first {
		t.Error("Load did not return the stored snapshot")
	}

	held := idx.Load()
	idx.Store(held.With(link(2, "wiki")))
	if _, ok := held.Lookup("wiki"); ok {
		t.Error("a snapshot loaded before the swap sees the later link")
	}
	if _, ok := idx.Load().Lookup("wiki"); !ok {
		t.Error("Load after the swap does not see the new link")
	}

	idx.Store(nil)
	if s := idx.Load(); s == nil || s.Len() != 0 {
		t.Errorf("after Store(nil), Load() = %v, want an empty snapshot", s)
	}

	if got := New(first).Load(); got != first {
		t.Error("New(s).Load() did not return s")
	}
}

// Readers run while one writer keeps swapping snapshots. Under -race this
// fails if With or Store ever touches memory a reader can see. Each reader
// also checks that a snapshot never changes underneath it.
func TestConcurrentReadersDuringWrites(t *testing.T) {
	const (
		readers = 8
		writes  = 300
	)
	idx := New(Build([]linkmodel.Link{link(0, "seed")}))

	done := make(chan struct{})
	var wg sync.WaitGroup
	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-done:
					return
				default:
				}
				s := idx.Load()
				n := s.Len()
				if _, ok := s.Lookup("SEED"); !ok {
					t.Error("seed link missing from a snapshot")
					return
				}
				// Links are added in order, so a snapshot of n links holds
				// exactly seed and link-1 … link-(n-1).
				if n > 1 {
					last := fmt.Sprintf("link-%d", n-1)
					l, ok := s.Lookup(last)
					if !ok || len(l.Routes) != 1 || l.Routes[0].Pattern != last {
						t.Errorf("snapshot of %d links: Lookup(%q) = %+v, %v", n, last, l, ok)
						return
					}
				}
				if _, ok := s.Lookup(fmt.Sprintf("link-%d", n)); ok {
					t.Errorf("snapshot of %d links already holds link-%d", n, n)
					return
				}
				if s.Len() != n {
					t.Errorf("snapshot grew from %d to %d links while being read", n, s.Len())
					return
				}
			}
		}()
	}

	for i := 1; i <= writes; i++ {
		idx.Store(idx.Load().With(link(int64(i), fmt.Sprintf("link-%d", i))))
	}
	close(done)
	wg.Wait()

	if got := idx.Load().Len(); got != writes+1 {
		t.Errorf("final Len() = %d, want %d", got, writes+1)
	}
}

// The snapshot holds each link compiled: Find returns something a request
// can be matched against without parsing a pattern, and it agrees with
// Lookup about which link a name means.
func TestFindReturnsTheCompiledLink(t *testing.T) {
	gh := link(2, "gh",
		linkmodel.Route{ID: 20, Pattern: "gh", Destination: "https://github.com/acme"},
		linkmodel.Route{ID: 21, Position: 1, Pattern: "gh/new", Destination: "https://github.com/new"},
	)
	s := Build([]linkmodel.Link{link(1, "on-call"), gh})

	for _, name := range []string{"gh", "GH", "Gh"} {
		c, ok := s.Find(name)
		if !ok || c.Link.ID != 2 {
			t.Fatalf("Find(%q) = link %d, %v; want link 2", name, c.Link.ID, ok)
		}
		if route, ok := c.Match([]string{"GH", "New"}); !ok || route.ID != 21 {
			t.Errorf("Find(%q).Match(GH/New) = %+v, %v; want route 21", name, route, ok)
		}
		if route, ok := c.Match([]string{"gh", "old"}); ok {
			t.Errorf("Find(%q).Match(gh/old) matched %+v", name, route)
		}
		looked, _ := s.Lookup(name)
		if looked.ID != c.Link.ID || len(looked.Routes) != len(c.Link.Routes) {
			t.Errorf("Lookup(%q) = %+v, want the same link as Find", name, looked)
		}
	}

	// Q5: only case is ignored.
	for _, name := range []string{"on_call", "oncall", "g", ""} {
		if c, ok := s.Find(name); ok {
			t.Errorf("Find(%q) found link %d", name, c.Link.ID)
		}
	}
	var none *Snapshot
	if _, ok := none.Find("gh"); ok {
		t.Error("Find on a nil snapshot found a link")
	}

	// A link added with With is compiled like one given to Build.
	next := s.With(link(3, "wiki", linkmodel.Route{ID: 30, Pattern: "wiki/home", Destination: "https://wiki.example.com/"}))
	c, ok := next.Find("WIKI")
	if route, matched := c.Match([]string{"wiki", "Home"}); !ok || !matched || route.ID != 30 {
		t.Errorf("after With, Find(WIKI).Match(wiki/Home) = %+v, %v, %v; want route 30", route, ok, matched)
	}
}

// Find and Match run on every resolved request.
func TestFindDoesNotAllocate(t *testing.T) {
	s := Build([]linkmodel.Link{link(1, "gh")})
	segments := []string{"gh"}
	allocs := testing.AllocsPerRun(100, func() {
		c, _ := s.Find("gh")
		c.Match(segments)
	})
	if allocs != 0 {
		t.Errorf("Find and Match allocated %v times per call, want 0", allocs)
	}
}
