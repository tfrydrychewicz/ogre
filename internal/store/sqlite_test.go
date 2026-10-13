package store

import (
	"bufio"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/tfrydrychewicz/godir/internal/linkmodel"
)

// openTemp opens a store on a fresh temporary database and closes it when the
// test ends.
func openTemp(t *testing.T) *SQLite {
	t.Helper()
	return openAt(t, filepath.Join(t.TempDir(), "godir.db"))
}

func openAt(t *testing.T, path string) *SQLite {
	t.Helper()
	s, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open(%s): %v", path, err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// sampleLink creates a link with two routes and returns it as stored.
func sampleLink(t *testing.T, s *SQLite, name string) linkmodel.Link {
	t.Helper()
	ctx := context.Background()
	u, err := s.LocalUser(ctx)
	if err != nil {
		t.Fatalf("LocalUser: %v", err)
	}
	l, err := s.CreateLink(ctx, linkmodel.Link{
		Name: name, Description: "sample", OwnerID: u.ID,
		Routes: []linkmodel.Route{
			{Pattern: name, Destination: "https://example.com/" + name},
			{Pattern: name + "/sub", Destination: "https://example.com/" + name + "/sub"},
		},
	})
	if err != nil {
		t.Fatalf("CreateLink(%q): %v", name, err)
	}
	return l
}

func schemaVersion(t *testing.T, s *SQLite) int {
	t.Helper()
	v, err := s.SchemaVersion(context.Background())
	if err != nil {
		t.Fatalf("SchemaVersion: %v", err)
	}
	return v
}

// count returns the number of rows a "SELECT count(*) ..." query reports.
func count(t *testing.T, s *SQLite, query string, args ...any) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

// siblings lists the files next to the database whose names start with its
// name, without the directory: the database, its -wal and -shm, and backups.
func siblings(t *testing.T, dbPath string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Dir(dbPath))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), filepath.Base(dbPath)) {
			names = append(names, e.Name())
		}
	}
	return names
}

func backups(t *testing.T, dbPath string) []string {
	t.Helper()
	var out []string
	for _, n := range siblings(t, dbPath) {
		if strings.Contains(n, ".bak-") {
			out = append(out, n)
		}
	}
	return out
}

func TestOpenFresh(t *testing.T) {
	// A parent directory that does not exist yet, as on a first run.
	path := filepath.Join(t.TempDir(), "data", "godir", "godir.db")
	s := openAt(t, path)

	if got := schemaVersion(t, s); got != 1 {
		t.Errorf("schema version = %d, want 1", got)
	}
	u, err := s.LocalUser(context.Background())
	if err != nil {
		t.Fatalf("LocalUser: %v", err)
	}
	if u.Email != "local" || u.DisplayName != "Local user" || u.Role != "admin" || u.Status != "active" {
		t.Errorf("local user = %+v", u)
	}
	if n := count(t, s, `SELECT count(*) FROM users`); n != 1 {
		t.Errorf("users has %d rows, want 1", n)
	}
	for _, table := range []string{"users", "links", "routes", "variables", "revisions", "schema_migrations"} {
		if n := count(t, s, `SELECT count(*) FROM sqlite_schema WHERE type = 'table' AND name = ?`, table); n != 1 {
			t.Errorf("table %s is missing", table)
		}
	}
	for _, index := range []string{"routes_by_link", "revisions_by_link"} {
		if n := count(t, s, `SELECT count(*) FROM sqlite_schema WHERE type = 'index' AND name = ?`, index); n != 1 {
			t.Errorf("index %s is missing", index)
		}
	}
	// The directory holds one person's links: nobody else may list or read it.
	if runtime.GOOS != "windows" {
		for _, dir := range []string{filepath.Dir(path), filepath.Dir(filepath.Dir(path))} {
			info, err := os.Stat(dir)
			if err != nil {
				t.Fatal(err)
			}
			if perm := info.Mode().Perm(); perm != 0o700 {
				t.Errorf("%s was created with mode %o, want 700", dir, perm)
			}
		}
	}
	if n := count(t, s, `SELECT count(*) FROM pragma_table_info('links') WHERE name = 'name_norm'`); n != 0 {
		t.Error("links has a name_norm column; names are only lowercased (PRD Q5)")
	}
	if b := backups(t, path); len(b) != 0 {
		t.Errorf("a new database was backed up: %v", b)
	}
	if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() {
		t.Errorf("database file %s: info %v, err %v", path, info, err)
	}
}

func TestPragmas(t *testing.T) {
	s := openTemp(t)
	tests := []struct {
		pragma string
		want   string
	}{
		{"journal_mode", "wal"},
		{"foreign_keys", "1"},
		{"synchronous", "2"}, // FULL
		// macOS: a plain fsync leaves the data in the drive's cache, and
		// SQLite only asks for F_FULLFSYNC when these are on. Elsewhere they
		// do nothing, but the setting is still reported.
		{"fullfsync", "1"},
		{"checkpoint_fullfsync", "1"},
		{"busy_timeout", "5000"},
	}
	for _, tt := range tests {
		t.Run(tt.pragma, func(t *testing.T) {
			var got string
			if err := s.db.QueryRow("PRAGMA " + tt.pragma).Scan(&got); err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("PRAGMA %s = %q, want %q", tt.pragma, got, tt.want)
			}
		})
	}
	if n := s.db.Stats().MaxOpenConnections; n != 1 {
		t.Errorf("MaxOpenConnections = %d, want 1 (single writer)", n)
	}
}

func TestReopenIsIdempotent(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "godir.db")

	s := openAt(t, path)
	user, err := s.LocalUser(ctx)
	if err != nil {
		t.Fatal(err)
	}
	link := sampleLink(t, s, "roadmap")
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	for round := 1; round <= 2; round++ {
		s = openAt(t, path)
		if got := schemaVersion(t, s); got != 1 {
			t.Errorf("round %d: schema version = %d, want 1", round, got)
		}
		if n := count(t, s, `SELECT count(*) FROM schema_migrations`); n != 1 {
			t.Errorf("round %d: schema_migrations has %d rows, want 1", round, n)
		}
		if n := count(t, s, `SELECT count(*) FROM users`); n != 1 {
			t.Errorf("round %d: users has %d rows, want 1", round, n)
		}
		again, err := s.LocalUser(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if again != user {
			t.Errorf("round %d: local user = %+v, want %+v", round, again, user)
		}
		links, err := s.ListLinks(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(links) != 1 || links[0].ID != link.ID || len(links[0].Routes) != 2 {
			t.Errorf("round %d: links = %+v, want the one saved before", round, links)
		}
		if err := s.Close(); err != nil {
			t.Fatalf("round %d: Close: %v", round, err)
		}
	}
	if b := backups(t, path); len(b) != 0 {
		t.Errorf("reopening with nothing to migrate made backups: %v", b)
	}
}

// TestOpenAwkwardPath covers characters that are ordinary in real data
// directories ("Application Support") or special in a database URI.
func TestOpenAwkwardPath(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Application Support", "a?b#c%20d&e=f")
	path := filepath.Join(dir, "go dir.db")
	s := openAt(t, path)
	sampleLink(t, s, "roadmap")
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the database is not at the path given: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "go dir.db" {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("directory holds %q, want only %q", names, "go dir.db")
	}

	s = openAt(t, path)
	if n := count(t, s, `SELECT count(*) FROM links`); n != 1 {
		t.Errorf("reopened database has %d links, want 1", n)
	}
}

func TestOpenRejects(t *testing.T) {
	dir := t.TempDir()
	notDB := filepath.Join(dir, "notes.txt")
	garbage := []byte(strings.Repeat("these are my notes, not a database\n", 200))
	if err := os.WriteFile(notDB, garbage, 0o600); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		path string
	}{
		{"empty path", ""},
		{"in-memory", ":memory:"},
		{"uri", "file:" + filepath.Join(dir, "x.db") + "?mode=memory"},
		{"directory", dir},
		{"not a database", notDB},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, err := Open(context.Background(), tt.path)
			if err == nil {
				_ = s.Close()
				t.Fatalf("Open(%q) succeeded", tt.path)
			}
			t.Logf("error: %v", err)
		})
	}

	got, err := os.ReadFile(notDB)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(garbage) {
		t.Error("a file that is not a database was modified by the failed Open")
	}
	if names := siblings(t, notDB); len(names) != 1 {
		t.Errorf("the failed Open left files behind: %v", names)
	}
}

func TestForeignKeysAreEnforced(t *testing.T) {
	s := openTemp(t)
	link := sampleLink(t, s, "roadmap")

	t.Run("route needs a link", func(t *testing.T) {
		_, err := s.db.Exec(
			`INSERT INTO routes (link_id, position, pattern, shape, destination) VALUES (?, 0, 'x', 'x', 'https://example.com/')`,
			link.ID+1000)
		if err == nil {
			t.Fatal("a route for a link that does not exist was accepted")
		}
		if isUniqueViolation(err) {
			t.Errorf("a foreign-key failure was classified as a unique violation: %v", err)
		}
	})
	t.Run("link needs an owner", func(t *testing.T) {
		_, err := s.CreateLink(context.Background(), linkmodel.Link{Name: "orphan", OwnerID: 424242})
		if err == nil {
			t.Fatal("a link whose owner does not exist was accepted")
		}
		if errors.Is(err, ErrNameTaken) {
			t.Errorf("error = %v, want a plain failure", err)
		}
	})
	t.Run("an owner with links cannot be removed", func(t *testing.T) {
		if _, err := s.db.Exec(`DELETE FROM users`); err == nil {
			t.Fatal("deleting a user who owns a link was accepted")
		}
	})
	t.Run("deleting a link removes its routes", func(t *testing.T) {
		if n := count(t, s, `SELECT count(*) FROM routes WHERE link_id = ?`, link.ID); n != 2 {
			t.Fatalf("link has %d routes before the delete, want 2", n)
		}
		if _, err := s.db.Exec(`DELETE FROM links WHERE id = ?`, link.ID); err != nil {
			t.Fatal(err)
		}
		if n := count(t, s, `SELECT count(*) FROM routes`); n != 0 {
			t.Errorf("%d routes outlived their link", n)
		}
	})
}

// TestFailedCreateWritesNoRows looks underneath ListLinks: a refused link
// must not leave rows in any table.
func TestFailedCreateWritesNoRows(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	kept := sampleLink(t, s, "kept")
	owner := kept.OwnerID

	tests := []struct {
		name      string
		link      linkmodel.Link
		nameTaken bool
	}{
		{
			name: "duplicate name",
			link: linkmodel.Link{Name: "kept", OwnerID: owner, Routes: []linkmodel.Route{
				{Pattern: "kept/other", Destination: "https://example.com/x"},
			}},
			nameTaken: true,
		},
		{
			name: "same pattern twice",
			link: linkmodel.Link{Name: "twice", OwnerID: owner, Routes: []linkmodel.Route{
				{Pattern: "twice", Destination: "https://example.com/1"},
				{Pattern: "twice", Destination: "https://example.com/2"},
			}},
		},
		{
			name: "unknown owner",
			link: linkmodel.Link{Name: "orphan", OwnerID: owner + 1000, Routes: []linkmodel.Route{
				{Pattern: "orphan", Destination: "https://example.com/"},
			}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := s.CreateLink(ctx, tt.link)
			if err == nil {
				t.Fatal("CreateLink succeeded")
			}
			if got := errors.Is(err, ErrNameTaken); got != tt.nameTaken {
				t.Errorf("errors.Is(err, ErrNameTaken) = %v, want %v (err: %v)", got, tt.nameTaken, err)
			}
			if n := count(t, s, `SELECT count(*) FROM links`); n != 1 {
				t.Errorf("links has %d rows, want 1", n)
			}
			if n := count(t, s, `SELECT count(*) FROM routes`); n != 2 {
				t.Errorf("routes has %d rows, want 2", n)
			}
		})
	}
}

func TestTimesAreStoredAsRFC3339UTC(t *testing.T) {
	s := openTemp(t)
	// A clock in another zone, with a fraction of a second.
	zone := time.FixedZone("UTC+2", 2*60*60)
	s.now = func() time.Time { return time.Date(2026, 10, 13, 11, 0, 0, 987654321, zone) }
	want := time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)

	link := sampleLink(t, s, "roadmap")
	if !link.CreatedAt.Equal(want) || !link.UpdatedAt.Equal(want) {
		t.Errorf("CreateLink returned times %v / %v, want %v", link.CreatedAt, link.UpdatedAt, want)
	}

	var created, updated string
	if err := s.db.QueryRow(`SELECT created_at, updated_at FROM links`).Scan(&created, &updated); err != nil {
		t.Fatal(err)
	}
	if created != "2026-10-13T09:00:00Z" || updated != "2026-10-13T09:00:00Z" {
		t.Errorf("stored created_at, updated_at = %q, %q; want 2026-10-13T09:00:00Z", created, updated)
	}

	links, err := s.ListLinks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !links[0].CreatedAt.Equal(want) || links[0].CreatedAt.Location() != time.UTC {
		t.Errorf("ListLinks returned CreatedAt %v, want %v in UTC", links[0].CreatedAt, want)
	}
}

func TestListLinksRejectsACorruptTime(t *testing.T) {
	s := openTemp(t)
	sampleLink(t, s, "roadmap")
	if _, err := s.db.Exec(`UPDATE links SET updated_at = 'yesterday'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ListLinks(context.Background()); err == nil {
		t.Error("ListLinks accepted a time that is not RFC 3339")
	}
}

// TestSavedLinkSurvivesWithoutClose copies the database and its write-ahead
// log while the store is still open, as a crash would leave them, and opens
// the copy. The saved link must be there although nothing was checkpointed
// or closed.
func TestSavedLinkSurvivesWithoutClose(t *testing.T) {
	path := filepath.Join(t.TempDir(), "godir.db")
	s := openAt(t, path)
	link := sampleLink(t, s, "roadmap")

	wal, err := os.Stat(path + "-wal")
	if err != nil || wal.Size() == 0 {
		t.Fatalf("expected a non-empty write-ahead log next to the open database: info %v, err %v", wal, err)
	}

	crashed := filepath.Join(t.TempDir(), "godir.db")
	copyFile(t, path, crashed)
	copyFile(t, path+"-wal", crashed+"-wal")
	// The -shm file is left out on purpose: it does not survive a reboot.

	recovered := openAt(t, crashed)
	links, err := recovered.ListLinks(context.Background())
	if err != nil {
		t.Fatalf("ListLinks on the recovered copy: %v", err)
	}
	if len(links) != 1 || links[0].Name != "roadmap" || links[0].ID != link.ID || len(links[0].Routes) != 2 {
		t.Errorf("recovered links = %+v, want the saved one", links)
	}
	if got := schemaVersion(t, recovered); got != 1 {
		t.Errorf("recovered schema version = %d, want 1", got)
	}
}

// helperDBEnv, when set, turns TestKillHelper into the child process of
// TestSavedLinkSurvivesKill.
const helperDBEnv = "GODIR_STORE_TEST_HELPER_DB"

// TestSavedLinkSurvivesKill is NFR-7 for a crashing process: a child saves a
// link, reports that the save was acknowledged, and is killed with SIGKILL
// before it can close anything.
func TestSavedLinkSurvivesKill(t *testing.T) {
	path := filepath.Join(t.TempDir(), "godir.db")

	cmd := exec.Command(os.Args[0], "-test.run=^TestKillHelper$")
	cmd.Env = append(os.Environ(), helperDBEnv+"="+path)
	cmd.Stderr = os.Stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	killed := false
	t.Cleanup(func() {
		if !killed {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})

	acknowledged := false
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		if scanner.Text() == "saved" {
			acknowledged = true
			break
		}
	}
	if !acknowledged {
		t.Fatalf("the helper exited without saving (scan error: %v)", scanner.Err())
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("kill: %v", err)
	}
	_ = cmd.Wait()
	killed = true

	s := openAt(t, path)
	links, err := s.ListLinks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 1 || links[0].Name != "survivor" || len(links[0].Routes) != 2 {
		t.Errorf("after the kill the database holds %+v, want the link \"survivor\"", links)
	}
}

// TestKillHelper is not a test of its own; see TestSavedLinkSurvivesKill.
func TestKillHelper(t *testing.T) {
	path := os.Getenv(helperDBEnv)
	if path == "" {
		t.Skip("helper process for TestSavedLinkSurvivesKill")
	}
	s, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	sampleLink(t, s, "survivor")
	os.Stdout.WriteString("saved\n")
	// Wait to be killed. The limit only stops a stray helper from lingering
	// if the parent test dies first.
	time.Sleep(30 * time.Second)
}

func copyFile(t *testing.T, from, to string) {
	t.Helper()
	data, err := os.ReadFile(from)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(to, data, 0o600); err != nil {
		t.Fatal(err)
	}
}
