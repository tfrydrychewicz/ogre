package app

import (
	"context"
	"database/sql"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tfrydrychewicz/ogre/internal/linkmodel"
	"github.com/tfrydrychewicz/ogre/internal/store"
)

// spyStore is the store the use-case tests run against: the real SQLite
// store, on a database in the test's temporary directory, wrapped so that a
// test can count calls and make one fail.
//
// The use cases depend on a good deal of store behaviour (versions, route
// identity, revisions, the order in which refusals are checked). A fake would
// have to copy all of it and could drift; wrapping the real one cannot. The
// count is how the tests prove that Resolve and Validate never touch the
// store.
type spyStore struct {
	*store.SQLite
	path string

	calls atomic.Int64 // every call of a Store method except Close

	// Failures to inject; nil means the call goes through.
	createErr, updateErr, deleteErr error
	getErr, listErr, userErr        error
	// localRole, when set, replaces the role of the built-in user.
	localRole string
	// beforeWrite, when set, runs inside CreateLink, UpdateLink and
	// DeleteLink before the real store is called: the moment at which the
	// use case holds its lock and nothing is committed yet.
	beforeWrite func()
}

var _ store.Store = (*spyStore)(nil)

// newSpyStore opens a new, empty database that is closed when the test ends.
func newSpyStore(t testing.TB) *spyStore {
	t.Helper()
	return openSpyStore(t, filepath.Join(t.TempDir(), "ogre.db"))
}

func openSpyStore(t testing.TB, path string) *spyStore {
	t.Helper()
	st, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	s := &spyStore{SQLite: st, path: path}
	t.Cleanup(func() { st.Close() })
	return s
}

// addUser inserts a second, non-admin user and returns an Actor for them. No
// Store method can do that before sign-in exists, so the row is written
// through a second connection; importing package store has registered the
// driver.
func (s *spyStore) addUser(t testing.TB, name string) Actor {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(s.path)+"?_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)")
	if err != nil {
		t.Fatalf("open %s: %v", s.path, err)
	}
	defer db.Close()
	res, err := db.Exec(
		`INSERT INTO users (email, display_name, role, status, created_at) VALUES (?, ?, 'user', 'active', ?)`,
		name+"@example.com", name, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		t.Fatalf("add user %s: %v", name, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return Actor{UserID: id, DisplayName: name}
}

// revisions returns how many revisions the link has.
func (s *spyStore) revisions(t testing.TB, linkID int64) []store.Revision {
	t.Helper()
	revs, err := s.SQLite.Revisions(context.Background(), linkID)
	if err != nil {
		t.Fatalf("Revisions(%d): %v", linkID, err)
	}
	return revs
}

func (s *spyStore) LocalUser(ctx context.Context) (store.User, error) {
	s.calls.Add(1)
	if s.userErr != nil {
		return store.User{}, s.userErr
	}
	u, err := s.SQLite.LocalUser(ctx)
	if s.localRole != "" {
		u.Role = s.localRole
	}
	return u, err
}

func (s *spyStore) CreateLink(ctx context.Context, l linkmodel.Link, editorID int64) (linkmodel.Link, error) {
	s.calls.Add(1)
	if s.createErr != nil {
		return linkmodel.Link{}, s.createErr
	}
	if s.beforeWrite != nil {
		s.beforeWrite()
	}
	return s.SQLite.CreateLink(ctx, l, editorID)
}

func (s *spyStore) GetLink(ctx context.Context, id int64) (linkmodel.Link, error) {
	s.calls.Add(1)
	if s.getErr != nil {
		return linkmodel.Link{}, s.getErr
	}
	return s.SQLite.GetLink(ctx, id)
}

func (s *spyStore) UpdateLink(ctx context.Context, l linkmodel.Link, expectedVersion int, editorID int64) (linkmodel.Link, error) {
	s.calls.Add(1)
	if s.updateErr != nil {
		return linkmodel.Link{}, s.updateErr
	}
	if s.beforeWrite != nil {
		s.beforeWrite()
	}
	return s.SQLite.UpdateLink(ctx, l, expectedVersion, editorID)
}

func (s *spyStore) DeleteLink(ctx context.Context, id int64, expectedVersion int, editorID int64) error {
	s.calls.Add(1)
	if s.deleteErr != nil {
		return s.deleteErr
	}
	if s.beforeWrite != nil {
		s.beforeWrite()
	}
	return s.SQLite.DeleteLink(ctx, id, expectedVersion, editorID)
}

func (s *spyStore) ListLinks(ctx context.Context) ([]linkmodel.Link, error) {
	s.calls.Add(1)
	if s.listErr != nil {
		return nil, s.listErr
	}
	return s.SQLite.ListLinks(ctx)
}
