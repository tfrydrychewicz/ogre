// Package index holds every link in memory, compiled for matching, so the
// resolver can answer a request without touching the database or parsing a
// pattern.
//
// A Snapshot is immutable: a write builds a new one and Index swaps it in
// atomically. Readers therefore never take a lock and never observe a
// half-applied change (plan section 3.2).
package index

import (
	"iter"
	"maps"
	"slices"

	"github.com/tfrydrychewicz/ogre/internal/linkmodel"
)

// Snapshot is the set of links at one moment, keyed by normalized name. It
// never changes after it is built, so any number of goroutines may read it.
// The zero value and a nil *Snapshot are both a valid empty snapshot.
type Snapshot struct {
	// Each link is compiled once and held by pointer, so that a lookup hands
	// out the address of something that already exists instead of copying a
	// link per request.
	byName map[string]*linkmodel.CompiledLink
	// byID finds a stored link by its ID. Links without an ID (0) are not in
	// it.
	byID map[int64]*linkmodel.CompiledLink
	// names is every key of byName, sorted. "Did you mean" scans all names on
	// every unknown-name miss; keeping the list here means a miss does not
	// collect and sort it again.
	names []string
	// docs[i] is the search document of the link called names[i]: the link
	// and its searchable text, lowercased once when the link enters the
	// snapshot, so that a search, which runs on every keystroke, scans
	// memory in name order and folds no case (search.go).
	docs []*doc
}

// Build returns a snapshot of links, each compiled once (linkmodel.Compile)
// so that no request has to parse a pattern or compile a regular expression.
// It keeps its own copy of each link's routes, so the caller may go on using
// the slice it passed in. If two links normalize to the same name the later
// one wins; the store's unique constraint means that does not happen with
// real data.
func Build(links []linkmodel.Link) *Snapshot {
	s := &Snapshot{
		byName: make(map[string]*linkmodel.CompiledLink, len(links)),
		byID:   make(map[int64]*linkmodel.CompiledLink, len(links)),
	}
	for _, l := range links {
		s.put(linkmodel.NormalizeName(l.Name), own(l))
	}
	s.names = slices.Sorted(maps.Keys(s.byName))
	s.docs = make([]*doc, len(s.names))
	for i, name := range s.names {
		s.docs[i] = newDoc(name, s.byName[name])
	}
	return s
}

// put adds c under name to the maps, replacing what the name held. It does
// not touch names, and is only for a snapshot nobody else can see yet.
func (s *Snapshot) put(name string, c *linkmodel.CompiledLink) {
	s.drop(name)
	s.byName[name] = c
	if c.Link.ID != 0 {
		s.byID[c.Link.ID] = c
	}
}

// drop removes the link called name from the maps and reports whether there
// was one. Like put, it is only for a snapshot under construction.
func (s *Snapshot) drop(name string) bool {
	old, ok := s.byName[name]
	if !ok {
		return false
	}
	delete(s.byName, name)
	// Only forget the ID if it still leads to this link: a later link may
	// have taken it over.
	if s.byID[old.Link.ID] == old {
		delete(s.byID, old.Link.ID)
	}
	return true
}

// Find returns the link with the given name, ignoring case (PRD 5.5 step 2),
// in its compiled form: what the resolver matches a request against.
//
// The result is the snapshot's own: read it, never modify it. Nothing is
// copied because Find runs on every resolved request.
func (s *Snapshot) Find(name string) (*linkmodel.CompiledLink, bool) {
	if s == nil {
		return nil, false
	}
	c, ok := s.byName[linkmodel.NormalizeName(name)]
	return c, ok
}

// Lookup is Find for callers that want the link itself and will not match
// against it. Its Routes are shared with the snapshot: read them, never
// modify them.
func (s *Snapshot) Lookup(name string) (linkmodel.Link, bool) {
	c, ok := s.Find(name)
	if !ok {
		return linkmodel.Link{}, false
	}
	return c.Link, true
}

// ByID returns the link with the given stored ID, under the same sharing
// rule as Find. ID 0 belongs to no stored link and is never found.
func (s *Snapshot) ByID(id int64) (*linkmodel.CompiledLink, bool) {
	if s == nil || id == 0 {
		return nil, false
	}
	c, ok := s.byID[id]
	return c, ok
}

// Names returns the name of every link, lowercase and sorted. The slice is
// the snapshot's own and is shared by every caller: read it, never modify
// it. Returning it costs nothing, which is the point: every unknown-name
// miss scans it for suggestions.
func (s *Snapshot) Names() []string {
	if s == nil {
		return nil
	}
	return s.names
}

// All iterates over every link in order of name, under the same sharing rule
// as Find.
func (s *Snapshot) All() iter.Seq[*linkmodel.CompiledLink] {
	return func(yield func(*linkmodel.CompiledLink) bool) {
		if s == nil {
			return
		}
		for _, d := range s.docs {
			if !yield(d.link) {
				return
			}
		}
	}
}

// With returns a new snapshot that also contains l, replacing any link of the
// same name. The receiver is left untouched, which is what lets readers keep
// using it while a write is in progress.
//
// Links are keyed by name. A link that was renamed is therefore a new entry
// to With: remove the old name with Without as well.
//
// It copies the maps and the list of names, so one write costs time
// proportional to the number of links. Writes are rare next to reads and a
// copy of 100,000 entries takes milliseconds; a persistent map can replace
// this if that ever matters.
func (s *Snapshot) With(l linkmodel.Link) *Snapshot {
	name := linkmodel.NormalizeName(l.Name)
	next := s.copy(1)
	c := own(l)
	next.put(name, c)
	at, found := slices.BinarySearch(next.names, name)
	if found {
		next.docs[at] = newDoc(name, c)
	} else {
		// The clone has room for one more (see copy), so this does not copy
		// the names a second time.
		next.names = slices.Insert(next.names, at, name)
		next.docs = slices.Insert(next.docs, at, newDoc(name, c))
	}
	return next
}

// Without returns a new snapshot that lacks the link with the given name,
// ignoring case. The receiver is left untouched. Removing a name that is not
// there yields a snapshot equal to the receiver.
func (s *Snapshot) Without(name string) *Snapshot {
	name = linkmodel.NormalizeName(name)
	next := s.copy(0)
	if next.drop(name) {
		if at, found := slices.BinarySearch(next.names, name); found {
			next.names = slices.Delete(next.names, at, at+1)
			next.docs = slices.Delete(next.docs, at, at+1)
		}
	}
	return next
}

// copy returns a snapshot with the receiver's content that shares nothing
// mutable with it, with room for extra more links.
func (s *Snapshot) copy(extra int) *Snapshot {
	n := s.Len() + extra
	next := &Snapshot{
		byName: make(map[string]*linkmodel.CompiledLink, n),
		byID:   make(map[int64]*linkmodel.CompiledLink, n),
		names:  make([]string, s.Len(), n),
		docs:   make([]*doc, s.Len(), n),
	}
	if s != nil {
		maps.Copy(next.byName, s.byName)
		maps.Copy(next.byID, s.byID)
		copy(next.names, s.names)
		// A doc is immutable like the link it describes, so the new
		// snapshot shares the old one's docs and replaces only what changes.
		copy(next.docs, s.docs)
	}
	return next
}

// Len returns the number of links in the snapshot.
func (s *Snapshot) Len() int {
	if s == nil {
		return 0
	}
	return len(s.byName)
}

// own compiles l over a private copy of its routes, cutting the snapshot off
// from later changes to the caller's slice. The variable descriptions inside
// each route are copied too, for the same reason.
func own(l linkmodel.Link) *linkmodel.CompiledLink {
	l.Routes = slices.Clone(l.Routes)
	for i := range l.Routes {
		l.Routes[i].Variables = slices.Clone(l.Routes[i].Variables)
	}
	c := linkmodel.Compile(l)
	return &c
}
