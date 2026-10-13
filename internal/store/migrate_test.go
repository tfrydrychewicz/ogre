package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
)

// known returns the migrations shipped in the binary.
func known(t *testing.T) []migration {
	t.Helper()
	m, err := loadMigrations(embeddedMigrations, "migrations")
	if err != nil {
		t.Fatalf("loadMigrations: %v", err)
	}
	return m
}

// withNext returns the shipped migrations followed by one more, standing in
// for the next godir release.
func withNext(t *testing.T, name, sql string) []migration {
	t.Helper()
	m := known(t)
	return append(m, migration{version: len(m) + 1, name: name, sql: sql})
}

func openWith(t *testing.T, path string, migrations []migration) *SQLite {
	t.Helper()
	s, err := open(context.Background(), path, migrations)
	if err != nil {
		t.Fatalf("open(%s): %v", path, err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestEmbeddedMigrations(t *testing.T) {
	m := known(t)
	if len(m) != 1 || m[0].version != 1 || m[0].name != "0001_init" {
		t.Fatalf("embedded migrations = %+v, want only 0001_init", m)
	}
	if !strings.Contains(m[0].sql, "CREATE TABLE links") {
		t.Error("0001_init does not create the links table")
	}
}

func TestLoadMigrations(t *testing.T) {
	file := func(body string) *fstest.MapFile { return &fstest.MapFile{Data: []byte(body)} }

	tests := []struct {
		name    string
		files   fstest.MapFS
		want    []string // names in order; nil when an error is expected
		wantErr string
	}{
		{
			name:  "ordered by version, not by listing order",
			files: fstest.MapFS{"m/0002_b.sql": file("B"), "m/0001_a.sql": file("A"), "m/0003_c_d.sql": file("C")},
			want:  []string{"0001_a", "0002_b", "0003_c_d"},
		},
		{
			name:    "no migrations",
			files:   fstest.MapFS{"m/.keep": file("")},
			wantErr: "must look like",
		},
		{
			name:    "empty directory",
			files:   fstest.MapFS{"m": &fstest.MapFile{Mode: os.ModeDir}},
			wantErr: "no migrations",
		},
		{
			name:    "missing directory",
			files:   fstest.MapFS{},
			wantErr: "read migrations",
		},
		{
			name:    "gap",
			files:   fstest.MapFS{"m/0001_a.sql": file("A"), "m/0003_c.sql": file("C")},
			wantErr: "without gaps",
		},
		{
			name:    "does not start at 1",
			files:   fstest.MapFS{"m/0002_b.sql": file("B")},
			wantErr: "without gaps",
		},
		{
			name:    "repeated version",
			files:   fstest.MapFS{"m/0001_a.sql": file("A"), "m/0001_b.sql": file("B")},
			wantErr: "repeats",
		},
		{
			name:    "three-digit version",
			files:   fstest.MapFS{"m/001_a.sql": file("A")},
			wantErr: "must look like",
		},
		{
			name:    "no name",
			files:   fstest.MapFS{"m/0001.sql": file("A")},
			wantErr: "must look like",
		},
		{
			name:    "uppercase name",
			files:   fstest.MapFS{"m/0001_Init.sql": file("A")},
			wantErr: "must look like",
		},
		{
			name:    "stray file",
			files:   fstest.MapFS{"m/0001_a.sql": file("A"), "m/README.md": file("x")},
			wantErr: "must look like",
		},
		{
			name:    "subdirectory",
			files:   fstest.MapFS{"m/0001_a.sql": file("A"), "m/0002_b.sql/x": file("x")},
			wantErr: "must look like",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := loadMigrations(tt.files, "m")
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want one containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("loadMigrations: %v", err)
			}
			var names []string
			for i, m := range got {
				names = append(names, m.name)
				if m.version != i+1 {
					t.Errorf("%s has version %d, want %d", m.name, m.version, i+1)
				}
			}
			if !reflect.DeepEqual(names, tt.want) {
				t.Errorf("names = %v, want %v", names, tt.want)
			}
			if got[0].sql != "A" {
				t.Errorf("first migration body = %q, want %q", got[0].sql, "A")
			}
		})
	}
}

func TestPendingAfter(t *testing.T) {
	known := []migration{{version: 1, name: "0001_init"}, {version: 2, name: "0002_usage"}}

	tests := []struct {
		name      string
		applied   []appliedMigration
		want      int // number of pending migrations
		wantNewer bool
		wantErr   bool
	}{
		{name: "new database", applied: nil, want: 2},
		{name: "one behind", applied: []appliedMigration{{1, "0001_init"}}, want: 1},
		{name: "up to date", applied: []appliedMigration{{1, "0001_init"}, {2, "0002_usage"}}, want: 0},
		{
			name:      "newer than the binary",
			applied:   []appliedMigration{{1, "0001_init"}, {2, "0002_usage"}, {3, "0003_soft_delete"}},
			wantNewer: true, wantErr: true,
		},
		{name: "renamed migration", applied: []appliedMigration{{1, "0001_other"}}, wantErr: true},
		{name: "history with a gap", applied: []appliedMigration{{2, "0002_usage"}}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pending, err := pendingAfter(tt.applied, known)
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tt.wantErr)
			}
			if got := errors.Is(err, ErrSchemaNewer); got != tt.wantNewer {
				t.Errorf("errors.Is(err, ErrSchemaNewer) = %v, want %v (err: %v)", got, tt.wantNewer, err)
			}
			if err == nil && len(pending) != tt.want {
				t.Errorf("%d pending, want %d", len(pending), tt.want)
			}
		})
	}
}

// TestBackupBeforePendingMigration plays an upgrade: a database written by
// this godir is opened by one that knows a further migration.
func TestBackupBeforePendingMigration(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "godir.db")

	s := openWith(t, path, known(t))
	link := sampleLink(t, s, "roadmap")
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	next := withNext(t, "0002_test", `CREATE TABLE added_in_two (x INTEGER); INSERT INTO added_in_two VALUES (7);`)
	s = openWith(t, path, next)
	if got := schemaVersion(t, s); got != 2 {
		t.Errorf("schema version after the upgrade = %d, want 2", got)
	}
	if n := count(t, s, `SELECT x FROM added_in_two`); n != 7 {
		t.Errorf("the new migration's data = %d, want 7", n)
	}
	if n := count(t, s, `SELECT count(*) FROM links`); n != 1 {
		t.Errorf("the upgraded database has %d links, want 1", n)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	if got, want := backups(t, path), []string{"godir.db.bak-1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("backups = %v, want %v", got, want)
	}

	// The backup is a complete version-1 database: the old godir opens it,
	// finds the link, and has nothing to migrate.
	backup := path + ".bak-1"
	b := openWith(t, backup, known(t))
	if got := schemaVersion(t, b); got != 1 {
		t.Errorf("backup schema version = %d, want 1", got)
	}
	if n := count(t, b, `SELECT count(*) FROM sqlite_schema WHERE name = 'added_in_two'`); n != 0 {
		t.Error("the backup already contains the new migration's table")
	}
	links, err := b.ListLinks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 1 || links[0].ID != link.ID || len(links[0].Routes) != 2 {
		t.Errorf("backup links = %+v, want the saved one", links)
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}

	// Opening again with nothing pending makes no further backup.
	s = openWith(t, path, next)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if got := backups(t, path); len(got) != 1 {
		t.Errorf("backups after a second open = %v, want still one", got)
	}
}

// TestBackupIncludesUncheckpointedWrites upgrades a database whose latest
// link is still only in the write-ahead log, as after a crash.
func TestBackupIncludesUncheckpointedWrites(t *testing.T) {
	src := filepath.Join(t.TempDir(), "godir.db")
	s := openWith(t, src, known(t))
	sampleLink(t, s, "roadmap")

	path := filepath.Join(t.TempDir(), "godir.db")
	copyFile(t, src, path)
	copyFile(t, src+"-wal", path+"-wal")

	up := openWith(t, path, withNext(t, "0002_test", `CREATE TABLE added_in_two (x INTEGER)`))
	if err := up.Close(); err != nil {
		t.Fatal(err)
	}

	b := openWith(t, path+".bak-1", known(t))
	if n := count(t, b, `SELECT count(*) FROM links WHERE name = 'roadmap'`); n != 1 {
		t.Errorf("the backup has %d matching links, want 1", n)
	}
}

func TestBackupNeverOverwritesAnEarlierOne(t *testing.T) {
	path := filepath.Join(t.TempDir(), "godir.db")
	s := openWith(t, path, known(t))
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{path + ".bak-1", path + ".bak-1.2"} {
		if err := os.WriteFile(name, []byte("earlier backup"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// A leftover from a backup that was interrupted must not get in the way.
	if err := os.WriteFile(path+".bak-1.3.tmp", []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}

	s = openWith(t, path, withNext(t, "0002_test", `CREATE TABLE added_in_two (x INTEGER)`))
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	want := []string{"godir.db.bak-1", "godir.db.bak-1.2", "godir.db.bak-1.3"}
	if got := backups(t, path); !reflect.DeepEqual(got, want) {
		t.Fatalf("backups = %v, want %v", got, want)
	}
	for _, name := range []string{path + ".bak-1", path + ".bak-1.2"} {
		if data, _ := os.ReadFile(name); string(data) != "earlier backup" {
			t.Errorf("%s was overwritten", filepath.Base(name))
		}
	}
	b := openWith(t, path+".bak-1.3", known(t))
	if got := schemaVersion(t, b); got != 1 {
		t.Errorf("new backup schema version = %d, want 1", got)
	}
}

// TestBackupOfUnmigratedDatabase covers a file that holds data but has never
// been migrated (schema version 0): it is copied aside as .bak-0.
func TestBackupOfUnmigratedDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "godir.db")
	raw, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`CREATE TABLE legacy (note TEXT); INSERT INTO legacy VALUES ('keep me');`); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	s := openWith(t, path, known(t))
	if got := schemaVersion(t, s); got != 1 {
		t.Errorf("schema version = %d, want 1", got)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if got, want := backups(t, path), []string{"godir.db.bak-0"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("backups = %v, want %v", got, want)
	}

	// Read the backup directly: it is at version 0, so opening it as a store
	// would migrate it.
	b, err := sql.Open("sqlite", dsn(path+".bak-0"))
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	var note string
	if err := b.QueryRow(`SELECT note FROM legacy`).Scan(&note); err != nil || note != "keep me" {
		t.Errorf("backup legacy note = %q, err %v", note, err)
	}
	var tables int
	if err := b.QueryRow(`SELECT count(*) FROM sqlite_schema WHERE name IN ('links', 'schema_migrations')`).Scan(&tables); err != nil || tables != 0 {
		t.Errorf("backup has %d of the new tables (err %v), want 0", tables, err)
	}
}

func TestNoBackupOfAnEmptyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "godir.db")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	s := openWith(t, path, known(t))
	if got := schemaVersion(t, s); got != 1 {
		t.Errorf("schema version = %d, want 1", got)
	}
	if got := backups(t, path); len(got) != 0 {
		t.Errorf("an empty file was backed up: %v", got)
	}
}

func TestRefusesANewerDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "godir.db")
	s := openWith(t, path, withNext(t, "0002_test", `CREATE TABLE added_in_two (x INTEGER)`))
	sampleLink(t, s, "roadmap")
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	old, err := open(context.Background(), path, known(t))
	if err == nil {
		_ = old.Close()
		t.Fatal("an older godir opened a database with a newer schema")
	}
	if !errors.Is(err, ErrSchemaNewer) {
		t.Errorf("error = %v, want ErrSchemaNewer", err)
	}
	t.Logf("error: %v", err)

	if got := backups(t, path); len(got) != 0 {
		t.Errorf("the refused open made backups: %v", got)
	}
	// The newer godir still finds everything as it left it.
	s = openWith(t, path, withNext(t, "0002_test", `CREATE TABLE added_in_two (x INTEGER)`))
	if got := schemaVersion(t, s); got != 2 {
		t.Errorf("schema version = %d, want 2", got)
	}
	if n := count(t, s, `SELECT count(*) FROM links`); n != 1 {
		t.Errorf("links = %d, want 1", n)
	}
}

func TestFailedMigrationChangesNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "godir.db")
	s := openWith(t, path, known(t))
	sampleLink(t, s, "roadmap")
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	// The first statement works, the second cannot.
	broken := withNext(t, "0002_broken", `CREATE TABLE half_done (x INTEGER); INSERT INTO no_such_table VALUES (1);`)
	failed, err := open(context.Background(), path, broken)
	if err == nil {
		_ = failed.Close()
		t.Fatal("open succeeded although the migration cannot run")
	}
	if !strings.Contains(err.Error(), "0002_broken") {
		t.Errorf("error = %v, want it to name the migration", err)
	}

	s = openWith(t, path, known(t))
	if got := schemaVersion(t, s); got != 1 {
		t.Errorf("schema version after the failed migration = %d, want 1", got)
	}
	if n := count(t, s, `SELECT count(*) FROM sqlite_schema WHERE name = 'half_done'`); n != 0 {
		t.Error("half of the failed migration was kept")
	}
	if n := count(t, s, `SELECT count(*) FROM links`); n != 1 {
		t.Errorf("links = %d, want 1", n)
	}
	if got, want := backups(t, path), []string{"godir.db.bak-1"}; !reflect.DeepEqual(got, want) {
		t.Errorf("backups = %v, want %v (taken before the attempt)", got, want)
	}
}

func TestRefusesAMismatchedHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "godir.db")
	s := openWith(t, path, known(t))
	if _, err := s.db.Exec(`UPDATE schema_migrations SET name = '0001_something_else'`); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	other, err := open(context.Background(), path, known(t))
	if err == nil {
		_ = other.Close()
		t.Fatal("open accepted a database with a different migration history")
	}
	if errors.Is(err, ErrSchemaNewer) {
		t.Errorf("error = %v, want a history mismatch, not ErrSchemaNewer", err)
	}
}

// TestApplyMigrationSkipsOneAlreadyApplied covers two godir processes
// starting together: the loser of the race finds the migration done.
func TestApplyMigrationSkipsOneAlreadyApplied(t *testing.T) {
	s := openWith(t, filepath.Join(t.TempDir(), "godir.db"), known(t))
	if err := s.applyMigration(context.Background(), known(t)[0]); err != nil {
		t.Fatalf("applying an already applied migration: %v", err)
	}
	if n := count(t, s, `SELECT count(*) FROM schema_migrations`); n != 1 {
		t.Errorf("schema_migrations has %d rows, want 1", n)
	}

	err := s.applyMigration(context.Background(), migration{version: 3, name: "0003_skips_two", sql: `SELECT 1`})
	if err == nil {
		t.Error("a migration that skips a version was applied")
	}
}

// No copy, no upgrade: when the database cannot be copied aside, the pending
// migration must not run. The copy is the only safety net for links that
// exist nowhere else, and this is exactly when it would be missing.
func TestFailedBackupStopsTheMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "godir.db")
	s := openWith(t, path, known(t))
	sampleLink(t, s, "roadmap")
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	// A non-empty directory where the copy is first written can be neither
	// removed nor overwritten, so the backup fails.
	if err := os.MkdirAll(filepath.Join(path+".bak-1.tmp", "in-the-way"), 0o700); err != nil {
		t.Fatal(err)
	}

	next := withNext(t, "0002_test", `CREATE TABLE added_in_two (x INTEGER);`)
	upgraded, err := open(context.Background(), path, next)
	if err == nil {
		_ = upgraded.Close()
		t.Fatal("the database was migrated although it could not be backed up")
	}
	if !strings.Contains(err.Error(), "back up before migrating") {
		t.Errorf("error = %v, want it to say the backup failed", err)
	}

	s = openWith(t, path, known(t))
	if got := schemaVersion(t, s); got != 1 {
		t.Errorf("schema version = %d, want 1 (untouched)", got)
	}
	if n := count(t, s, `SELECT count(*) FROM sqlite_schema WHERE name = 'added_in_two'`); n != 0 {
		t.Error("the migration ran without a backup")
	}
	if n := count(t, s, `SELECT count(*) FROM links`); n != 1 {
		t.Errorf("links = %d, want 1", n)
	}
	// Only the obstacle is there: nothing that could be taken for a backup.
	if got := backups(t, path); len(got) != 1 || got[0] != "godir.db.bak-1.tmp" {
		t.Errorf("a failed backup left files that look like backups: %v", got)
	}
}
