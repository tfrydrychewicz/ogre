package index

import (
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/tfrydrychewicz/ogre/internal/linkmodel"
)

// check holds a snapshot to everything it promises at once: the names are
// exactly the links, sorted; All walks them in that order; and every stored
// link is found by its ID.
func check(t *testing.T, label string, s *Snapshot, want ...string) {
	t.Helper()
	if want == nil {
		want = []string{}
	}
	names := s.Names()
	if !slices.Equal(names, want) && !(len(names) == 0 && len(want) == 0) {
		t.Errorf("%s: Names() = %v, want %v", label, names, want)
	}
	if s.Len() != len(want) {
		t.Errorf("%s: Len() = %d, want %d", label, s.Len(), len(want))
	}
	var walked []string
	for c := range s.All() {
		// A link handed over in mixed case keeps its text; the key is what
		// is normalized.
		walked = append(walked, linkmodel.NormalizeName(c.Link.Name))
		byName, ok := s.Find(c.Link.Name)
		if !ok || byName != c {
			t.Errorf("%s: All yields %q, which Find does not return", label, c.Link.Name)
		}
		if c.Link.ID != 0 {
			if byID, ok := s.ByID(c.Link.ID); !ok || byID != c {
				t.Errorf("%s: ByID(%d) does not return %q", label, c.Link.ID, c.Link.Name)
			}
		}
	}
	if !slices.Equal(walked, names) {
		t.Errorf("%s: All() walked %v, want the order of Names() %v", label, walked, names)
	}
	// Search walks the docs and reads names by the same index, so the two
	// must stay aligned through every write.
	if s != nil {
		if len(s.docs) != len(s.names) {
			t.Fatalf("%s: %d docs for %d names", label, len(s.docs), len(s.names))
		}
		for i, name := range s.names {
			d := s.docs[i]
			if d.link != s.byName[name] || !strings.HasPrefix(d.text, name+string(docSep)) {
				t.Errorf("%s: doc %d is not the doc of %q", label, i, name)
			}
		}
	}
}

func TestNamesAreSortedAndFollowWrites(t *testing.T) {
	// Built from unsorted, mixed-case input: names come out lowercase, sorted.
	base := Build([]linkmodel.Link{link(1, "roadmap"), link(2, "GH"), link(3, "on-call"), link(4, "on_call")})
	check(t, "Build", base, "gh", "on-call", "on_call", "roadmap")

	tests := []struct {
		name string
		make func() *Snapshot
		want []string
	}{
		{"With at the front", func() *Snapshot { return base.With(link(5, "aaa")) },
			[]string{"aaa", "gh", "on-call", "on_call", "roadmap"}},
		{"With in the middle", func() *Snapshot { return base.With(link(5, "Jira")) },
			[]string{"gh", "jira", "on-call", "on_call", "roadmap"}},
		{"With at the end", func() *Snapshot { return base.With(link(5, "zzz")) },
			[]string{"gh", "on-call", "on_call", "roadmap", "zzz"}},
		{"With replacing a link adds no name", func() *Snapshot { return base.With(link(2, "Gh")) },
			[]string{"gh", "on-call", "on_call", "roadmap"}},
		{"Without the first", func() *Snapshot { return base.Without("GH") },
			[]string{"on-call", "on_call", "roadmap"}},
		{"Without the last", func() *Snapshot { return base.Without("roadmap") },
			[]string{"gh", "on-call", "on_call"}},
		{"Without an unknown name", func() *Snapshot { return base.Without("oncall") },
			[]string{"gh", "on-call", "on_call", "roadmap"}},
		{"a rename", func() *Snapshot { return base.Without("gh").With(link(2, "github")) },
			[]string{"github", "on-call", "on_call", "roadmap"}},
		{"Without everything", func() *Snapshot {
			return base.Without("gh").Without("on-call").Without("on_call").Without("roadmap")
		}, nil},
	}
	for _, tt := range tests {
		check(t, tt.name, tt.make(), tt.want...)
		// Whatever was derived, the snapshot it came from is as it was.
		check(t, tt.name+": receiver", base, "gh", "on-call", "on_call", "roadmap")
	}
}

func TestWithoutDoesNotMutateReceiver(t *testing.T) {
	base := Build([]linkmodel.Link{link(1, "gh"), link(2, "roadmap"), link(3, "wiki")})
	names := base.Names() // held across the write, as a reader would

	next := base.Without("Roadmap")
	if _, ok := next.Lookup("roadmap"); ok {
		t.Error("Without left the link in the new snapshot")
	}
	if _, ok := next.ByID(2); ok {
		t.Error("Without left the link findable by ID")
	}
	if l, ok := base.Lookup("roadmap"); !ok || l.ID != 2 {
		t.Error("Without removed the link from the receiver")
	}
	if _, ok := base.ByID(2); !ok {
		t.Error("Without removed the link's ID from the receiver")
	}
	if want := []string{"gh", "roadmap", "wiki"}; !slices.Equal(names, want) {
		t.Errorf("a Names() slice taken before Without now reads %v, want %v", names, want)
	}

	// With must not write into the names of the snapshot it derives from
	// either, even when that snapshot's slice has spare capacity (which a
	// snapshot made by With has).
	grown := base.With(link(4, "zeta"))
	held := slices.Clone(grown.Names())
	a, b := grown.With(link(5, "alpha")), grown.With(link(6, "beta"))
	if !slices.Equal(grown.Names(), held) {
		t.Errorf("deriving two snapshots changed the receiver's names to %v, want %v", grown.Names(), held)
	}
	check(t, "first derived", a, "alpha", "gh", "roadmap", "wiki", "zeta")
	check(t, "second derived", b, "beta", "gh", "roadmap", "wiki", "zeta")
}

func TestByID(t *testing.T) {
	s := Build([]linkmodel.Link{link(1, "gh"), link(2, "roadmap"), link(0, "unsaved")})
	if c, ok := s.ByID(2); !ok || c.Link.Name != "roadmap" {
		t.Errorf("ByID(2) = %v, %v; want roadmap", c, ok)
	}
	for _, id := range []int64{0, 3, -1} {
		if _, ok := s.ByID(id); ok {
			t.Errorf("ByID(%d) found a link", id)
		}
	}
	// A replaced link is found by its ID in its new form; the form it had is
	// still in the old snapshot.
	next := s.With(linkmodel.Link{ID: 2, Name: "roadmap", Version: 2})
	if c, _ := next.ByID(2); c == nil || c.Link.Version != 2 {
		t.Errorf("after With, ByID(2) = %+v, want version 2", c)
	}
	if c, _ := s.ByID(2); c == nil || c.Link.Version != 1 {
		t.Errorf("the old snapshot's ByID(2) = %+v, want version 1", c)
	}
	// A name that passes to a link with another ID forgets the old ID.
	taken := s.With(link(9, "gh"))
	if _, ok := taken.ByID(1); ok {
		t.Error("ByID(1) still finds the link whose name was taken over")
	}
	if c, ok := taken.ByID(9); !ok || c.Link.Name != "gh" {
		t.Errorf("ByID(9) = %v, %v; want gh", c, ok)
	}
}

func TestEmptySnapshotNamesAndAll(t *testing.T) {
	var none *Snapshot
	for label, s := range map[string]*Snapshot{"nil": none, "zero": {}, "Build(nil)": Build(nil)} {
		check(t, label, s)
		if _, ok := s.ByID(1); ok {
			t.Errorf("%s: ByID found a link", label)
		}
		if got := s.Without("gh"); got == nil || got.Len() != 0 {
			t.Errorf("%s: Without = %v, want an empty snapshot", label, got)
		}
		check(t, label+" then With", s.With(link(1, "gh")), "gh")
	}
}

// All stops when the loop body breaks.
func TestAllStopsEarly(t *testing.T) {
	s := Build([]linkmodel.Link{link(1, "a"), link(2, "b"), link(3, "c")})
	var seen []string
	for c := range s.All() {
		seen = append(seen, c.Link.Name)
		if len(seen) == 2 {
			break
		}
	}
	if !slices.Equal(seen, []string{"a", "b"}) {
		t.Errorf("All() yielded %v before the break, want [a b]", seen)
	}
}

// Names runs on every unknown-name miss and must cost nothing.
func TestNamesDoesNotAllocate(t *testing.T) {
	s := Build([]linkmodel.Link{link(1, "gh"), link(2, "roadmap")})
	var n int
	if allocs := testing.AllocsPerRun(100, func() { n += len(s.Names()) }); allocs != 0 {
		t.Errorf("Names allocated %v times per call, want 0", allocs)
	}
	_ = n
}

// Readers scan names and walk All while a writer adds, replaces and removes
// links. Under -race this fails if a write ever touches memory that a
// snapshot already handed out; each reader also checks that the snapshot it
// holds stays consistent with itself.
func TestConcurrentReadersDuringAddAndRemove(t *testing.T) {
	const (
		readers = 8
		rounds  = 300
	)
	idx := New(Build([]linkmodel.Link{link(1, "seed")}))

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
				names := s.Names()
				if len(names) != s.Len() || !slices.IsSorted(names) {
					t.Errorf("snapshot of %d links has names %v", s.Len(), names)
					return
				}
				n := 0
				for c := range s.All() {
					if c.Link.Name != names[n] {
						t.Errorf("All() yields %q at %d, Names() has %q", c.Link.Name, n, names[n])
						return
					}
					if byID, ok := s.ByID(c.Link.ID); !ok || byID != c {
						t.Errorf("ByID(%d) disagrees with All() about %q", c.Link.ID, c.Link.Name)
						return
					}
					n++
				}
				if _, found := slices.BinarySearch(names, "seed"); !found {
					t.Error("seed missing from the names of a snapshot")
					return
				}
			}
		}()
	}

	// The writer keeps a window of ten links alive: it adds one, rewrites
	// it, and removes the one that fell out of the window.
	for i := 2; i < rounds; i++ {
		name := fmt.Sprintf("link-%03d", i)
		idx.Store(idx.Load().With(link(int64(i), name)))
		idx.Store(idx.Load().With(linkmodel.Link{ID: int64(i), Name: name, Version: 2}))
		if i >= 12 {
			idx.Store(idx.Load().Without(fmt.Sprintf("link-%03d", i-10)))
		}
	}
	close(done)
	wg.Wait()

	if got := idx.Load().Len(); got != 11 {
		t.Errorf("final Len() = %d, want 11 (seed and a window of ten)", got)
	}
}
