package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite" // registers the "sqlite" database/sql driver
)

// busyTimeoutMillis is how long a statement waits for another process holding
// the database (a second ogre, a backup tool) before giving up.
const busyTimeoutMillis = 5000

// SQLite is the Store kept in a single SQLite file.
//
// It uses one database connection. SQLite allows one writer at a time anyway,
// and a single connection turns concurrent writes into an orderly queue inside
// database/sql instead of SQLITE_BUSY errors. Nothing is lost by it: go links
// are resolved from the in-memory index and never read the database.
type SQLite struct {
	db   *sql.DB
	path string
	// now is the clock; tests replace it to get fixed timestamps.
	now func() time.Time
}

var _ Store = (*SQLite)(nil)

// Open opens the database file at path, creating it and its parent directory
// when missing, and brings it to the schema this binary expects.
//
// If the file already holds data and schema changes are pending, the database
// is first copied to path + ".bak-<current schema version>": a personal
// Directory holds links that exist nowhere else, so a failed upgrade must
// never be the only copy. Open refuses a database written by a newer Ogre
// (ErrSchemaNewer) rather than guess at a schema it does not know.
func Open(ctx context.Context, path string) (*SQLite, error) {
	migrations, err := loadMigrations(embeddedMigrations, "migrations")
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}
	return open(ctx, path, migrations)
}

// open is Open with the migration list supplied, so tests can simulate a
// binary that knows more (or fewer) schema versions.
func open(ctx context.Context, path string, migrations []migration) (*SQLite, error) {
	if path == "" {
		return nil, errors.New("store: no database path given")
	}
	if path == ":memory:" || strings.HasPrefix(path, "file:") {
		// SQLite's special names are refused: an in-memory database would
		// silently lose every link on exit, and a URI could carry options
		// that undo the settings below.
		return nil, fmt.Errorf("store: %q is not a file path; in-memory databases and file: URIs are not supported", path)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}
	// 0700: the directory holds one person's data.
	if err := os.MkdirAll(filepath.Dir(abs), 0o700); err != nil {
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}
	existed, err := regularFileExists(abs)
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}

	db, err := sql.Open("sqlite", dsn(abs))
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}
	db.SetMaxOpenConns(1)
	// Keep the one connection for the life of the store: reopening it would
	// re-run the pragmas and checkpoint the write-ahead log for no benefit.
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)
	db.SetConnMaxIdleTime(0)

	s := &SQLite{db: db, path: abs, now: time.Now}
	if err := s.prepare(ctx, existed, migrations); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}
	return s, nil
}

// prepare runs everything Open must finish before the store is usable.
func (s *SQLite) prepare(ctx context.Context, existed bool, migrations []migration) error {
	if err := s.checkJournalMode(ctx); err != nil {
		return err
	}
	if err := s.migrate(ctx, existed, migrations); err != nil {
		return err
	}
	return s.ensureLocalUser(ctx)
}

// checkJournalMode confirms the write-ahead log is really in use. SQLite
// reports, rather than fails, when a filesystem cannot support it, and the
// durability promise of NFR-7 was designed and tested with it on.
func (s *SQLite) checkJournalMode(ctx context.Context) error {
	var mode string
	if err := s.db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil {
		return err
	}
	if !strings.EqualFold(mode, "wal") {
		return fmt.Errorf("the database could not be switched to write-ahead logging (journal mode is %q)", mode)
	}
	return nil
}

// Close closes the database. Every acknowledged write is already on disk.
func (s *SQLite) Close() error {
	return s.db.Close()
}

// dsn builds the driver's data source name for the file at abs.
//
// The file name is passed as a "file:" URI with the path percent-encoded
// because the driver cuts a plain name at its first "?", and real data
// directories contain characters that need care ("Application Support").
//
// The pragmas apply to every connection the pool opens:
//   - journal_mode=WAL and synchronous=FULL: every commit is synced to the
//     disk before it is acknowledged, so a saved link survives a crash or a
//     restart (NFR-7).
//   - fullfsync and checkpoint_fullfsync: on macOS a plain fsync only hands
//     the data to the drive, which may still hold it in a volatile cache;
//     SQLite asks for F_FULLFSYNC, which waits for the drive, only when these
//     are on. Without them an acknowledged save could be lost to a power
//     cut. They do nothing on other systems. A save costs a few milliseconds
//     more, and saves are rare: go links resolve from memory.
//   - foreign_keys=ON: SQLite does not enforce references unless asked.
//   - busy_timeout: wait for another process instead of failing at once.
//
// _txlock=immediate makes write transactions take the write lock when they
// begin. A transaction that reads first and upgrades later can fail with
// SQLITE_BUSY without the busy timeout ever applying.
func dsn(abs string) string {
	p := filepath.ToSlash(abs)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p // Windows: C:/x becomes /C:/x, the form SQLite expects
	}
	u := url.URL{Scheme: "file", Path: p}
	q := url.Values{}
	q.Add("_pragma", fmt.Sprintf("busy_timeout(%d)", busyTimeoutMillis))
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "synchronous(FULL)")
	q.Add("_pragma", "fullfsync(ON)")
	q.Add("_pragma", "checkpoint_fullfsync(ON)")
	q.Add("_pragma", "foreign_keys(ON)")
	q.Set("_txlock", "immediate")
	return u.String() + "?" + q.Encode()
}

// regularFileExists reports whether path is an existing regular file. Anything
// else that occupies the name (a directory, a socket) is an error: SQLite's
// own message for that case does not say what is wrong.
func regularFileExists(path string) (bool, error) {
	info, err := os.Stat(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return false, nil
	case err != nil:
		return false, err
	case !info.Mode().IsRegular():
		return false, errors.New("the path exists and is not a regular file")
	}
	return true, nil
}

// timestamp is the current time as stored and returned: UTC, whole seconds,
// so a value read back equals the value handed out when it was written.
func (s *SQLite) timestamp() time.Time {
	return s.now().UTC().Truncate(time.Second)
}

// formatTime renders t the way every time column stores it: RFC 3339, UTC.
func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}

// parseTime reads a time column.
func parseTime(column, value string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("store: %s holds %q, which is not an RFC 3339 time", column, value)
	}
	return t.UTC(), nil
}
