package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// backupBeforeMigrating copies the database aside before its schema changes,
// to path + ".bak-<version>", where version is the schema version the copy
// holds. A database with nothing in it is skipped.
//
// The copy is made with VACUUM INTO rather than by copying files: the live
// data is spread over the database file and its write-ahead log, and VACUUM
// INTO writes one consistent, self-contained file from both.
func (s *SQLite) backupBeforeMigrating(ctx context.Context, version int) error {
	var objects int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_schema`).Scan(&objects); err != nil {
		return err
	}
	if objects == 0 {
		return nil // an empty file someone created; there is nothing to lose
	}

	dest, err := freeBackupPath(s.path, version)
	if err != nil {
		return err
	}
	// Write under a temporary name and rename, so a file with a backup's name
	// is always a complete backup, even if ogre is killed while writing it.
	tmp := dest + ".tmp"
	if err := os.Remove(tmp); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `VACUUM INTO ?`, tmp); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := syncFile(tmp); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	// Make the rename itself durable. Not every platform can sync a
	// directory, and the backup's content is already safe, so a failure here
	// is not worth refusing to start over.
	_ = syncFile(filepath.Dir(dest))
	return nil
}

// freeBackupPath picks the backup's name. An earlier backup of the same
// schema version (left by an upgrade that failed and was retried) is never
// overwritten; later copies get ".2", ".3", ... appended.
func freeBackupPath(dbPath string, version int) (string, error) {
	base := fmt.Sprintf("%s.bak-%d", dbPath, version)
	candidate := base
	for n := 2; ; n++ {
		_, err := os.Lstat(candidate)
		if errors.Is(err, os.ErrNotExist) {
			return candidate, nil
		}
		if err != nil {
			return "", err
		}
		candidate = fmt.Sprintf("%s.%d", base, n)
	}
}

// syncFile flushes a file (or directory) to stable storage.
func syncFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}
