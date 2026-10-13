package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strconv"
)

// ErrSchemaNewer means the database was written by a newer Ogre than this
// binary. Open refuses it: running old code against an unknown schema risks
// the data, and the fix is simply to run the newer Ogre.
var ErrSchemaNewer = errors.New("the database was written by a newer version of ogre")

//go:embed migrations/*.sql
var embeddedMigrations embed.FS

// migration is one numbered schema change.
type migration struct {
	version int
	name    string // "0001_init", the file name without ".sql"
	sql     string
}

// migrationFile is the required file name: a four-digit version, an
// underscore, and a lowercase name.
var migrationFile = regexp.MustCompile(`^(\d{4})_[a-z0-9_]+\.sql$`)

// loadMigrations reads every migration in dir, ordered by version.
//
// It is strict on purpose. A file that does not match NNNN_name.sql, a
// repeated version or a gap is an error rather than something to skip: a
// silently ignored migration would leave a database that claims a schema it
// does not have.
func loadMigrations(fsys fs.FS, dir string) ([]migration, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, fmt.Errorf("read migrations: %w", err)
	}
	var out []migration
	for _, e := range entries {
		m := migrationFile.FindStringSubmatch(e.Name())
		if e.IsDir() || m == nil {
			return nil, fmt.Errorf("migration %q: the name must look like 0001_name.sql", e.Name())
		}
		version, err := strconv.Atoi(m[1])
		if err != nil {
			return nil, fmt.Errorf("migration %q: %w", e.Name(), err)
		}
		body, err := fs.ReadFile(fsys, path.Join(dir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("migration %q: %w", e.Name(), err)
		}
		name := e.Name()[:len(e.Name())-len(".sql")]
		out = append(out, migration{version: version, name: name, sql: string(body)})
	}
	if len(out) == 0 {
		return nil, errors.New("no migrations found")
	}
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	for i, m := range out {
		if m.version != i+1 {
			return nil, fmt.Errorf("migration %q: versions must run 1, 2, 3, ... without gaps or repeats; expected %04d here", m.name, i+1)
		}
	}
	return out, nil
}

// appliedMigration is one row of schema_migrations.
type appliedMigration struct {
	version int
	name    string
}

// queryer is what reading the migration history needs; *sql.DB and *sql.Tx
// both satisfy it.
type queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// appliedMigrations returns the recorded history, oldest first. A database
// that has never been migrated has no schema_migrations table and no history.
func appliedMigrations(ctx context.Context, q queryer) ([]appliedMigration, error) {
	var tables int
	err := q.QueryRowContext(ctx,
		`SELECT count(*) FROM sqlite_schema WHERE type = 'table' AND name = 'schema_migrations'`).Scan(&tables)
	if err != nil {
		return nil, err
	}
	if tables == 0 {
		return nil, nil
	}
	rows, err := q.QueryContext(ctx, `SELECT version, name FROM schema_migrations ORDER BY version`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []appliedMigration
	for rows.Next() {
		var a appliedMigration
		if err := rows.Scan(&a.version, &a.name); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// SchemaVersion returns the number of the newest migration applied to the
// database, or 0 for a database that has none.
func (s *SQLite) SchemaVersion(ctx context.Context) (int, error) {
	applied, err := appliedMigrations(ctx, s.db)
	if err != nil {
		return 0, fmt.Errorf("store: schema version: %w", err)
	}
	if len(applied) == 0 {
		return 0, nil
	}
	return applied[len(applied)-1].version, nil
}

// pendingAfter checks the recorded history against the migrations this binary
// knows and returns the ones still to apply.
func pendingAfter(applied []appliedMigration, known []migration) ([]migration, error) {
	if n := len(applied); n > 0 && applied[n-1].version > len(known) {
		return nil, fmt.Errorf("%w: it is at schema version %d and this ogre knows up to %d",
			ErrSchemaNewer, applied[n-1].version, len(known))
	}
	for i, a := range applied {
		// The newest version is within range, so a longer history has repeats
		// or gaps; it cannot be indexed against known.
		if i >= len(known) || a.version != known[i].version || a.name != known[i].name {
			return nil, fmt.Errorf("the migration history does not match this ogre: entry %d is %04d %q", i+1, a.version, a.name)
		}
	}
	return known[len(applied):], nil
}

// migrate brings the schema up to date. existed says whether the database
// file was there before Open; only then is there anything worth copying aside.
func (s *SQLite) migrate(ctx context.Context, existed bool, known []migration) error {
	applied, err := appliedMigrations(ctx, s.db)
	if err != nil {
		return fmt.Errorf("read migration history: %w", err)
	}
	pending, err := pendingAfter(applied, known)
	if err != nil {
		return err
	}
	if len(pending) == 0 {
		return nil
	}
	if existed {
		if err := s.backupBeforeMigrating(ctx, len(applied)); err != nil {
			return fmt.Errorf("back up before migrating: %w", err)
		}
	}
	for _, m := range pending {
		if err := s.applyMigration(ctx, m); err != nil {
			return fmt.Errorf("migration %s: %w", m.name, err)
		}
	}
	return nil
}

// applyMigration runs one migration and records it, in one transaction: a
// migration that fails halfway leaves the database exactly as it was.
func (s *SQLite) applyMigration(ctx context.Context, m migration) (err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	if _, err = tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
  version    INTEGER PRIMARY KEY,
  name       TEXT    NOT NULL,
  applied_at TEXT    NOT NULL
)`); err != nil {
		return err
	}
	// Holding the write lock, look again: another ogre starting at the same
	// moment may have applied this migration while we waited for the lock.
	var current int
	if err = tx.QueryRowContext(ctx, `SELECT coalesce(max(version), 0) FROM schema_migrations`).Scan(&current); err != nil {
		return err
	}
	switch {
	case current >= m.version:
		return tx.Rollback()
	case current != m.version-1:
		return fmt.Errorf("the database is at schema version %d, expected %d", current, m.version-1)
	}

	if _, err = tx.ExecContext(ctx, m.sql); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx,
		`INSERT INTO schema_migrations (version, name, applied_at) VALUES (?, ?, ?)`,
		m.version, m.name, formatTime(s.timestamp())); err != nil {
		return err
	}
	return tx.Commit()
}
