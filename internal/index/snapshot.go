// Package index holds every link in memory, compiled for matching, so the
// resolver can answer a request without touching the database or parsing a
// pattern.
//
// A Snapshot is immutable: a write builds a new one and Index swaps it in
// atomically. Readers therefore never take a lock and never observe a
// half-applied change (plan section 3.2).
package index

import (
	"maps"
	"slices"

	"github.com/tfrydrychewicz/ogre/internal/linkmodel"
)

// Snapshot is the set of links at one moment, keyed by normalized name. It
// never changes after it is built, so any number of goroutines may read it.
// The zero value and a nil *Snapshot are both a valid empty snapshot.
type Snapshot struct {
	byName map[string]linkmodel.CompiledLink
}

// Build returns a snapshot of links, each compiled once (linkmodel.Compile)
// so that no request has to parse a pattern. It keeps its own copy of each
// link's routes, so the caller may go on using the slice it passed in. If two links
// normalize to the same name the later one wins; the store's unique
// constraint means that does not happen with real data.
func Build(links []linkmodel.Link) *Snapshot {
	s := &Snapshot{byName: make(map[string]linkmodel.CompiledLink, len(links))}
	for _, l := range links {
		s.byName[linkmodel.NormalizeName(l.Name)] = own(l)
	}
	return s
}

// Find returns the link with the given name, ignoring case (PRD 5.5 step 2),
// in its compiled form: what the resolver matches a request against.
//
// The result shares its routes with the snapshot: read them, never modify
// them. Nothing is copied because Find runs on every resolved request.
func (s *Snapshot) Find(name string) (linkmodel.CompiledLink, bool) {
	if s == nil {
		return linkmodel.CompiledLink{}, false
	}
	c, ok := s.byName[linkmodel.NormalizeName(name)]
	return c, ok
}

// Lookup is Find for callers that want the link itself and will not match
// against it. The same sharing rule applies to its Routes.
func (s *Snapshot) Lookup(name string) (linkmodel.Link, bool) {
	c, ok := s.Find(name)
	return c.Link, ok
}

// With returns a new snapshot that also contains l, replacing any link of the
// same name. The receiver is left untouched, which is what lets readers keep
// using it while a write is in progress.
//
// It copies the whole map, so one write costs time proportional to the number
// of links. Writes are rare next to reads and a copy of 100,000 entries takes
// milliseconds; a persistent map can replace this if that ever matters.
func (s *Snapshot) With(l linkmodel.Link) *Snapshot {
	next := &Snapshot{byName: make(map[string]linkmodel.CompiledLink, s.Len()+1)}
	if s != nil {
		maps.Copy(next.byName, s.byName)
	}
	next.byName[linkmodel.NormalizeName(l.Name)] = own(l)
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
// from later changes to the caller's slice.
func own(l linkmodel.Link) linkmodel.CompiledLink {
	l.Routes = slices.Clone(l.Routes)
	return linkmodel.Compile(l)
}
