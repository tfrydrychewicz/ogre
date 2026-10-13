// Package store persists links and users. It defines the Store interface the
// rest of godir depends on and its SQLite implementation.
//
// It is the only package that imports the database driver, so a second
// implementation (the plan allows for Postgres) can be added without touching
// the use cases. Any implementation must pass the suite in package storetest.
//
// The store keeps data; it does not own the link rules. It does not
// normalize a link's name: that is the job of the link model, applied by
// package app before it writes. What the store does is refuse a name that
// was not normalized (ErrNameNotNormalized), so that no caller, present or
// future, can leave two spellings of one name side by side.
package store

import (
	"context"
	"errors"

	"github.com/tfrydrychewicz/godir/internal/linkmodel"
)

// Errors a Store returns for conditions a caller is expected to handle. Test
// for them with errors.Is; implementations may wrap them with detail.
var (
	// ErrNotFound means the requested record does not exist.
	ErrNotFound = errors.New("store: not found")
	// ErrNameTaken means a link with exactly that name already exists.
	ErrNameTaken = errors.New("store: link name already taken")
	// ErrNameNotNormalized means a link was handed over under a name that is
	// not in its stored form (linkmodel.NormalizeName). Names are unique
	// ignoring case, and uniqueness is enforced on the stored text, so a
	// name in another spelling would get past it.
	ErrNameNotNormalized = errors.New("store: link name is not in its normalized (lowercase) form")
)

// LocalUserEmail identifies the built-in user that owns everything in local
// mode. It is not a real address: local mode has no sign-in, so the value only
// has to be unique and recognisable in an export.
const LocalUserEmail = "local"

// User is an account that can own links. Local mode has exactly one, the
// built-in admin; the cloud track adds real ones without changing this shape.
type User struct {
	ID          int64
	Email       string
	DisplayName string
	// Role is "user" or "admin".
	Role string
	// Status is "active" or "deactivated".
	Status string
}

// Store is the persistence seam. It is deliberately small: every method maps
// to one use case of package app, so a second implementation stays cheap.
type Store interface {
	// LocalUser returns the built-in user of local mode.
	LocalUser(ctx context.Context) (User, error)

	// CreateLink saves l and its routes atomically: either the link and all
	// of its routes are stored, or nothing is. It ignores l.ID, l.Version,
	// the timestamps and each route's ID and Position, and returns the link
	// with those set: Version 1, both timestamps the same UTC instant, and
	// route positions 0, 1, 2, ... in slice order.
	//
	// The name must already be normalized (linkmodel.NormalizeName);
	// otherwise CreateLink stores nothing and returns ErrNameNotNormalized.
	// It returns ErrNameTaken when a link with the same name exists.
	CreateLink(ctx context.Context, l linkmodel.Link) (linkmodel.Link, error)

	// ListLinks returns every link ordered by name, each with its routes
	// ordered by position. The result is never nil.
	ListLinks(ctx context.Context) ([]linkmodel.Link, error)

	// Close releases the store. Data acknowledged by an earlier call is
	// already durable; Close is not what makes it so.
	Close() error
}
