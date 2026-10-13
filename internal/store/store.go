// Package store persists links and users. It defines the Store interface the
// rest of Ogre depends on and its SQLite implementation.
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
//
// Every change to a link (create, update, delete) also writes one row of
// history, a Revision, in the same transaction as the change: either both are
// stored or neither is. The history has no screen yet (that is L5), but it is
// recorded from the first change on, so nothing is missing when it arrives.
package store

import (
	"context"
	"errors"
	"time"

	"github.com/tfrydrychewicz/ogre/internal/linkmodel"
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
	// ErrVersionConflict means the link exists but is not at the version the
	// caller expected: someone else changed it since the caller read it.
	// Nothing was written; the caller reads the link again and decides.
	ErrVersionConflict = errors.New("store: link was changed since it was read")
	// ErrRouteNotInLink means an update named a route, by ID, that is not a
	// route of the link being updated, or named the same route twice. See
	// Store.UpdateLink for why this is refused and not silently repaired.
	ErrRouteNotInLink = errors.New("store: route does not belong to the link")
	// ErrVariableMismatch means a route was handed over with metadata for a
	// variable its pattern does not declare, or for the same variable twice.
	// Such an entry could be stored but never read back, so it is refused
	// rather than lost without a word.
	ErrVariableMismatch = errors.New("store: variable metadata does not match the route's pattern")
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
//
// Three rules hold for every method that returns links:
//
//   - Routes are ordered by Position, which counts 0, 1, 2, ... without gaps.
//   - A route's Variables has exactly one entry per variable of its pattern,
//     in pattern order, whatever was handed in: the pattern text is the one
//     place that says which variables a route has, and the store keeps only
//     the description and example that the text does not carry. A variable
//     without stored metadata has both empty.
//   - Neither Routes nor Variables is ever nil.
//
// Every method that changes a link is one transaction: it is applied in full,
// together with its Revision, or not at all.
type Store interface {
	// LocalUser returns the built-in user of local mode.
	LocalUser(ctx context.Context) (User, error)

	// CreateLink saves l and its routes atomically: either the link and all
	// of its routes are stored, or nothing is. It ignores l.ID, l.Version,
	// the timestamps and each route's ID and Position, and returns the link
	// with those set: Version 1, both timestamps the same UTC instant, and
	// route positions 0, 1, 2, ... in slice order. l is not modified.
	//
	// The ID is one no link has had before, deleted links included: history
	// is kept by link ID and outlives the link, and an edit is addressed by
	// ID and version, so an ID that came back would hand a new link the
	// history, and the stale edits, of an old one.
	//
	// editorID is the user making the change; it is recorded in the
	// "create" revision and must be an existing user.
	//
	// The name must already be normalized (linkmodel.NormalizeName);
	// otherwise CreateLink stores nothing and returns ErrNameNotNormalized.
	// It returns ErrNameTaken when a link with the same name exists, and
	// ErrVariableMismatch when a route carries metadata for a variable its
	// pattern does not have.
	CreateLink(ctx context.Context, l linkmodel.Link, editorID int64) (linkmodel.Link, error)

	// GetLink returns the link with that ID and its routes, or ErrNotFound.
	GetLink(ctx context.Context, id int64) (linkmodel.Link, error)

	// UpdateLink replaces the name, description and routes of the link l.ID
	// with those of l, provided the stored link is at expectedVersion, and
	// returns the link as it now is: Version one higher, UpdatedAt the time
	// of the change. It ignores l.OwnerID, l.Version and the timestamps: an
	// update changes neither the owner nor CreatedAt. l is not modified.
	//
	// A rename is an update whose Name differs. The store does not check
	// that the routes' patterns start with the name; that is a link rule.
	//
	// Routes are matched to the stored ones by ID. A route whose ID is that
	// of a route of this link keeps the ID and takes the new pattern,
	// destination, variables and position; a route with ID 0 is new and
	// gets a fresh ID; every stored route not named is deleted with its
	// variables. Positions follow slice order. A route ID that is not zero
	// and is not a route of this link, or that appears twice, is refused
	// with ErrRouteNotInLink and nothing is changed. It is refused, and not
	// quietly treated as a new route, because route identity is what later
	// per-route data (usage counts) hangs on: a caller that sends the wrong
	// IDs would otherwise replace every route and lose that data unnoticed.
	// Because the version is checked first, a caller that read the link at
	// expectedVersion and sends back the IDs it read never sees this error.
	//
	// A new route never gets the ID of a route the same update removes. An
	// implementation should go further and never hand out a route ID twice
	// at all, but the suite does not demand it, and the SQLite store cannot
	// promise it with the schema of 0001_init: the ID of the newest route in
	// the whole table, once that route is removed, goes to the next route
	// created. Nothing depends on it today; whatever first keys lasting
	// data on a route ID (usage counts, L5) must close that gap.
	//
	// Errors, checked in this order: ErrNameNotNormalized and
	// ErrVariableMismatch (as for CreateLink); ErrNotFound when no link has
	// that ID; ErrVersionConflict when it is at another version;
	// ErrNameTaken when another link holds the new name; ErrRouteNotInLink.
	// Any error means nothing was changed and no revision was written.
	UpdateLink(ctx context.Context, l linkmodel.Link, expectedVersion int, editorID int64) (linkmodel.Link, error)

	// DeleteLink removes the link with its routes and their variables. Its
	// revisions stay: history outlives the link. A "delete" revision is
	// added whose snapshot is the link as it was just before.
	//
	// expectedVersion 0 deletes whatever version is stored. Any other value
	// must equal the stored version, or DeleteLink returns
	// ErrVersionConflict and removes nothing. It returns ErrNotFound when
	// no link has that ID.
	DeleteLink(ctx context.Context, id int64, expectedVersion int, editorID int64) error

	// ListLinks returns every link ordered by name, each with its routes
	// ordered by position. The result is never nil.
	ListLinks(ctx context.Context) ([]linkmodel.Link, error)

	// Close releases the store. Data acknowledged by an earlier call is
	// already durable; Close is not what makes it so.
	Close() error
}

// Revision actions written by this milestone. The column also allows
// "restore", "import" and "transfer", which arrive with their features.
const (
	RevisionCreate = "create"
	RevisionUpdate = "update"
	RevisionDelete = "delete"
)

// Revision is one entry of a link's history: who changed it, when, how, and
// what the link looked like as a result.
type Revision struct {
	ID       int64
	LinkID   int64
	EditorID int64
	// At is when the change was made, UTC. For a create or an update it
	// equals the link's UpdatedAt.
	At time.Time
	// Action is one of the Revision* constants.
	Action string
	// Snapshot is the whole link as a JSON document in the format of
	// EncodeSnapshot: the link after the change, or, for a delete, the link
	// as it was before. Read it with DecodeSnapshot.
	Snapshot string
}

// RevisionReader reads the history that every Store writes.
//
// It is kept apart from Store because no use case reads history before L5:
// the Store interface holds only what package app calls today, which is what
// keeps fakes and a second implementation cheap. An implementation is
// nevertheless expected to provide it, and the conformance suite in package
// storetest requires it, since that is the only way to check through the
// interface that revisions are written.
type RevisionReader interface {
	// Revisions returns the revisions of the link with that ID, oldest
	// first. The link may have been deleted; an ID that never existed
	// simply has no revisions. The result is never nil.
	Revisions(ctx context.Context, linkID int64) ([]Revision, error)
}
