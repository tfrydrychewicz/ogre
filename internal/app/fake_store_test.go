package app

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tfrydrychewicz/ogre/internal/linkmodel"
	"github.com/tfrydrychewicz/ogre/internal/store"
	"github.com/tfrydrychewicz/ogre/internal/store/storetest"
)

// fakeStore is an in-memory store.Store for the use-case tests. It follows
// the contract of the real one closely enough to stand in for it: it assigns
// IDs, versions, positions and UTC timestamps, refuses a duplicate name with
// store.ErrNameTaken, and returns copies so callers cannot reach its state.
// TestFakeStoreConformance holds it to that by running the same suite the
// SQLite store passes.
//
// It also counts calls, which is how the tests prove that Resolve never
// touches the store.
type fakeStore struct {
	mu     sync.Mutex
	links  map[string]linkmodel.Link
	nextID int64
	local  store.User
	// owners are the user ids a link may belong to: the local user, plus
	// whoever a test adds. The real store has a foreign key for this.
	owners map[int64]bool

	calls atomic.Int64 // every method call

	// Failures to inject; nil means the call succeeds.
	createErr error
	listErr   error
	userErr   error

	// beforeCommit, when set, runs inside CreateLink after the checks and
	// before the link becomes visible in the fake.
	beforeCommit func()
}

var _ store.Store = (*fakeStore)(nil)

func newFakeStore(existing ...linkmodel.Link) *fakeStore {
	f := &fakeStore{
		links:  map[string]linkmodel.Link{},
		local:  store.User{ID: 1, Email: store.LocalUserEmail, DisplayName: "Local user", Role: "admin", Status: "active"},
		owners: map[int64]bool{1: true},
	}
	for _, l := range existing {
		if _, err := f.CreateLink(context.Background(), l); err != nil {
			panic(err)
		}
	}
	f.calls.Store(0)
	return f
}

func (f *fakeStore) LocalUser(context.Context) (store.User, error) {
	f.calls.Add(1)
	if f.userErr != nil {
		return store.User{}, f.userErr
	}
	return f.local, nil
}

func (f *fakeStore) CreateLink(_ context.Context, l linkmodel.Link) (linkmodel.Link, error) {
	f.calls.Add(1)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.createErr != nil {
		return linkmodel.Link{}, f.createErr
	}
	if l.Name != linkmodel.NormalizeName(l.Name) {
		return linkmodel.Link{}, store.ErrNameNotNormalized
	}
	if _, taken := f.links[l.Name]; taken {
		return linkmodel.Link{}, store.ErrNameTaken
	}
	if !f.owners[l.OwnerID] {
		return linkmodel.Link{}, fmt.Errorf("fake store: user %d does not exist", l.OwnerID)
	}
	shapes := map[string]bool{}
	for i, r := range l.Routes {
		shape := linkmodel.Shape(r.Pattern)
		if shapes[shape] {
			return linkmodel.Link{}, fmt.Errorf("fake store: route %d repeats a pattern of the link", i+1)
		}
		shapes[shape] = true
	}
	if f.beforeCommit != nil {
		f.beforeCommit()
	}
	f.nextID++
	now := time.Now().UTC().Truncate(time.Second)
	l.ID, l.Version, l.CreatedAt, l.UpdatedAt = f.nextID, 1, now, now
	l.Routes = slices.Clone(l.Routes)
	for i := range l.Routes {
		l.Routes[i].ID = f.nextID*100 + int64(i)
		l.Routes[i].Position = i
	}
	f.links[l.Name] = l
	return clone(l), nil
}

func (f *fakeStore) ListLinks(context.Context) ([]linkmodel.Link, error) {
	f.calls.Add(1)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listErr != nil {
		return nil, f.listErr
	}
	out := make([]linkmodel.Link, 0, len(f.links))
	for _, l := range f.links {
		out = append(out, clone(l))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (f *fakeStore) Close() error { return nil }

func clone(l linkmodel.Link) linkmodel.Link {
	l.Routes = slices.Clone(l.Routes)
	return l
}

// The fake is only worth testing against if it behaves like a Store. It
// passes the suite every implementation must pass (plan section 3.10), so a
// use-case test that is green on the fake means the same on SQLite.
func TestFakeStoreConformance(t *testing.T) {
	storetest.Run(t, func(*testing.T) store.Store { return newFakeStore() })
}
