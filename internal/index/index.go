package index

import "sync/atomic"

// Index is the holder of the current Snapshot. Readers call Load and work
// with what they get for as long as they like; a writer calls Store once its
// change is committed. Both are safe from any goroutine.
//
// Index does not order writers. Whoever derives a new snapshot from the
// current one (Load, With, Store) must make sure no other writer does the
// same at the same time, or one of the two changes is lost; package app does
// that with a mutex around each write.
type Index struct {
	current atomic.Pointer[Snapshot]
}

// New returns an index holding s. A nil s means an empty index.
func New(s *Snapshot) *Index {
	i := &Index{}
	i.Store(s)
	return i
}

// Load returns the current snapshot. It is never nil.
func (i *Index) Load() *Snapshot {
	return i.current.Load()
}

// Store replaces the current snapshot. Readers that already loaded the old
// one keep a consistent view of it. A nil s stores an empty snapshot, so Load
// never has to be checked.
func (i *Index) Store(s *Snapshot) {
	if s == nil {
		s = Build(nil)
	}
	i.current.Store(s)
}
