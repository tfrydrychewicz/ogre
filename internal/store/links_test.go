package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tfrydrychewicz/ogre/internal/linkmodel"
)

// The tests in this file look underneath the Store interface, at the rows
// themselves: what package storetest proves through the interface is checked
// here in the tables, where a half-applied change or a leftover row would
// show even if no read method returned it.

// dumpTables renders every row of the tables a link change can touch, in a
// fixed order, so that two dumps are equal exactly when the data is.
func dumpTables(t *testing.T, s *SQLite) string {
	t.Helper()
	var b strings.Builder
	for _, query := range []string{
		`SELECT 'link', id, name, description, owner_id, version, created_at, updated_at FROM links ORDER BY id`,
		`SELECT 'route', id, link_id, position, pattern, shape, destination FROM routes ORDER BY id`,
		`SELECT 'variable', route_id, name, description, example FROM variables ORDER BY route_id, name`,
		`SELECT 'revision', id, link_id, editor_id, at, action, snapshot FROM revisions ORDER BY id`,
	} {
		rows, err := s.db.Query(query)
		if err != nil {
			t.Fatalf("%s: %v", query, err)
		}
		columns, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			values := make([]any, len(columns))
			targets := make([]any, len(columns))
			for i := range values {
				targets[i] = &values[i]
			}
			if err := rows.Scan(targets...); err != nil {
				t.Fatal(err)
			}
			fmt.Fprintf(&b, "%#v\n", values)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
	}
	return b.String()
}

// addUser inserts a second user, which no Store method can do yet, so that an
// editor can be told apart from an owner.
func addUser(t *testing.T, s *SQLite, email string) int64 {
	t.Helper()
	res, err := s.db.Exec(
		`INSERT INTO users (email, display_name, role, status, created_at) VALUES (?, ?, 'user', 'active', ?)`,
		email, email, formatTime(s.timestamp()))
	if err != nil {
		t.Fatalf("add user %s: %v", email, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func localID(t *testing.T, s *SQLite) int64 {
	t.Helper()
	u, err := s.LocalUser(context.Background())
	if err != nil {
		t.Fatalf("LocalUser: %v", err)
	}
	return u.ID
}

func meta(name, description, example string) linkmodel.VariableMeta {
	return linkmodel.VariableMeta{Name: name, Description: description, Example: example}
}

// ghLink creates a link with typed variables, metadata on some of them, and a
// route without variables.
func ghLink(t *testing.T, s *SQLite) linkmodel.Link {
	t.Helper()
	owner := localID(t, s)
	l, err := s.CreateLink(context.Background(), linkmodel.Link{
		Name: "gh", Description: "GitHub <shortcuts> & more", OwnerID: owner,
		Routes: []linkmodel.Route{
			{
				Pattern: "gh/{repo}/pr/{num:int}", Destination: "https://github.com/acme/{repo}/pull/{num}?a=1&b=2",
				Variables: []linkmodel.VariableMeta{meta("repo", "Repository", "ogre")},
			},
			{Pattern: "gh", Destination: "https://github.com/acme"},
			{
				Pattern: "gh/{repo}", Destination: "https://github.com/acme/{repo}",
				Variables: []linkmodel.VariableMeta{meta("repo", "", "ogre")},
			},
		},
	}, owner)
	if err != nil {
		t.Fatalf("CreateLink(gh): %v", err)
	}
	return l
}

// TestUpdateWritesTheRows follows one update into the tables, with a fixed
// clock: what moved, what stayed, and that nothing temporary is left behind.
func TestUpdateWritesTheRows(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	zone := time.FixedZone("UTC+2", 2*60*60)
	s.now = func() time.Time { return time.Date(2026, 10, 13, 11, 0, 0, 987654321, zone) }
	created := ghLink(t, s)
	pr, home, repo := created.Routes[0], created.Routes[1], created.Routes[2]
	editor := addUser(t, s, "ada@example.com")

	s.now = func() time.Time { return time.Date(2026, 10, 13, 11, 5, 30, 123456789, zone) }
	updated, err := s.UpdateLink(ctx, linkmodel.Link{ID: created.ID, Name: "gh", Description: "after", Routes: []linkmodel.Route{
		{ID: repo.ID, Pattern: "gh/{project}", Destination: "https://github.com/acme/{project}",
			Variables: []linkmodel.VariableMeta{meta("project", "Project", "")}},
		{Pattern: "gh/{repo}/issues/{num:int}", Destination: "https://github.com/acme/{repo}/issues/{num}",
			Variables: []linkmodel.VariableMeta{meta("num", "", "7"), meta("repo", "", "")}},
		{ID: pr.ID, Pattern: "gh/{repo}/pr/{num:int}", Destination: "https://github.com/acme/{repo}/pull/{num}"},
	}}, created.Version, editor)
	if err != nil {
		t.Fatalf("UpdateLink: %v", err)
	}

	wantCreated := time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)
	wantUpdated := time.Date(2026, 10, 13, 9, 5, 30, 0, time.UTC)
	if !updated.CreatedAt.Equal(wantCreated) || !updated.UpdatedAt.Equal(wantUpdated) {
		t.Errorf("UpdateLink returned times %v / %v, want %v / %v", updated.CreatedAt, updated.UpdatedAt, wantCreated, wantUpdated)
	}
	if !updated.UpdatedAt.After(created.UpdatedAt) {
		t.Errorf("UpdatedAt did not move: %v, was %v", updated.UpdatedAt, created.UpdatedAt)
	}

	var version, ownerID int64
	var createdAt, updatedAt, description string
	if err := s.db.QueryRow(`SELECT version, owner_id, created_at, updated_at, description FROM links WHERE id = ?`, created.ID).
		Scan(&version, &ownerID, &createdAt, &updatedAt, &description); err != nil {
		t.Fatal(err)
	}
	if version != 2 || ownerID != created.OwnerID || description != "after" ||
		createdAt != "2026-10-13T09:00:00Z" || updatedAt != "2026-10-13T09:05:30Z" {
		t.Errorf("links row: version %d, owner %d, description %q, created_at %q, updated_at %q",
			version, ownerID, description, createdAt, updatedAt)
	}

	// Routes: the two kept rows under their old ids, one new row, the
	// removed one gone, positions in slice order, and every shape the real
	// one: no placeholder from the rearranging survived.
	type row struct {
		id       int64
		position int
		pattern  string
		shape    string
	}
	rows, err := s.db.Query(`SELECT id, position, pattern, shape FROM routes WHERE link_id = ? ORDER BY position`, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	var got []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.position, &r.pattern, &r.shape); err != nil {
			t.Fatal(err)
		}
		got = append(got, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	added := updated.Routes[1].ID
	want := []row{
		{repo.ID, 0, "gh/{project}", linkmodel.Shape("gh/{project}")},
		{added, 1, "gh/{repo}/issues/{num:int}", linkmodel.Shape("gh/{repo}/issues/{num:int}")},
		{pr.ID, 2, "gh/{repo}/pr/{num:int}", linkmodel.Shape("gh/{repo}/pr/{num:int}")},
	}
	if !slices.Equal(got, want) {
		t.Errorf("routes rows = %+v, want %+v", got, want)
	}
	if added == pr.ID || added == home.ID || added == repo.ID {
		t.Errorf("the new route reused id %d", added)
	}
	if n := count(t, s, `SELECT count(*) FROM routes WHERE id = ?`, home.ID); n != 0 {
		t.Errorf("the removed route %d is still stored", home.ID)
	}

	// Variables: one row per variable that has something to say, and none
	// left from the metadata the update replaced.
	wantVars := fmt.Sprintf("%d/project=Project| %d/num=|7", repo.ID, added)
	if gotVars := variableRows(t, s); gotVars != wantVars {
		t.Errorf("variables rows = %q, want %q", gotVars, wantVars)
	}

	// The revision: dated as the link, by the editor, not the owner.
	var at, action string
	var editorID int64
	if err := s.db.QueryRow(`SELECT at, action, editor_id FROM revisions WHERE link_id = ? ORDER BY id DESC LIMIT 1`, created.ID).
		Scan(&at, &action, &editorID); err != nil {
		t.Fatal(err)
	}
	if at != "2026-10-13T09:05:30Z" || action != "update" || editorID != editor {
		t.Errorf("revision row: at %q, action %q, editor %d; want 2026-10-13T09:05:30Z, update, %d", at, action, editorID, editor)
	}
	if n := count(t, s, `SELECT count(*) FROM revisions`); n != 2 {
		t.Errorf("revisions has %d rows after a create and an update, want 2", n)
	}
}

// variableRows lists the variables table as "route/name=description|example"
// entries, ordered by route and name.
func variableRows(t *testing.T, s *SQLite) string {
	t.Helper()
	rows, err := s.db.Query(`SELECT route_id, name, description, example FROM variables ORDER BY route_id, name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var routeID int64
		var name, description, example string
		if err := rows.Scan(&routeID, &name, &description, &example); err != nil {
			t.Fatal(err)
		}
		out = append(out, fmt.Sprintf("%d/%s=%s|%s", routeID, name, description, example))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return strings.Join(out, " ")
}

// TestVariableRowsHoldOnlyWhatThePatternDoesNot: a variable with neither a
// description nor an example has no row; reading rebuilds it from the
// pattern.
func TestVariableRowsHoldOnlyWhatThePatternDoesNot(t *testing.T) {
	s := openTemp(t)
	created := ghLink(t, s)
	pr, repo := created.Routes[0], created.Routes[2]

	want := fmt.Sprintf("%d/repo=Repository|ogre %d/repo=|ogre", pr.ID, repo.ID)
	if got := variableRows(t, s); got != want {
		t.Errorf("variables rows = %q, want %q (no row for {num}, which has no text)", got, want)
	}

	got, err := s.GetLink(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	wantVars := [][]linkmodel.VariableMeta{
		{meta("repo", "Repository", "ogre"), meta("num", "", "")},
		{},
		{meta("repo", "", "ogre")},
	}
	for i, w := range wantVars {
		if !slices.Equal(got.Routes[i].Variables, w) || got.Routes[i].Variables == nil {
			t.Errorf("route %d variables = %#v, want %#v", i, got.Routes[i].Variables, w)
		}
	}
}

// TestReadingToleratesOddVariableRows covers data this version would not
// write but may meet: a database from L1, whose routes have no variables rows
// at all, and a row for a name the pattern no longer has.
func TestReadingToleratesOddVariableRows(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	created := ghLink(t, s)
	pr := created.Routes[0]

	if _, err := s.db.Exec(`DELETE FROM variables`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO variables (route_id, name, description, example) VALUES (?, 'gone', 'stale', 'x'), (?, 'num', 'Number', '')`,
		pr.ID, pr.ID); err != nil {
		t.Fatal(err)
	}

	for _, read := range []struct {
		name string
		get  func() (linkmodel.Link, error)
	}{
		{"GetLink", func() (linkmodel.Link, error) { return s.GetLink(ctx, created.ID) }},
		{"ListLinks", func() (linkmodel.Link, error) {
			links, err := s.ListLinks(ctx)
			if err != nil || len(links) != 1 {
				return linkmodel.Link{}, fmt.Errorf("ListLinks = %d links, %v", len(links), err)
			}
			return links[0], nil
		}},
	} {
		got, err := read.get()
		if err != nil {
			t.Fatalf("%s: %v", read.name, err)
		}
		want := [][]linkmodel.VariableMeta{
			{meta("repo", "", ""), meta("num", "Number", "")},
			{},
			{meta("repo", "", "")},
		}
		for i, w := range want {
			if !slices.Equal(got.Routes[i].Variables, w) {
				t.Errorf("%s: route %d variables = %q, want %q", read.name, i, got.Routes[i].Variables, w)
			}
		}
	}

	// The next update of the route clears the stale row with the rest.
	if _, err := s.UpdateLink(ctx, created, created.Version, created.OwnerID); err != nil {
		t.Fatalf("UpdateLink: %v", err)
	}
	if n := count(t, s, `SELECT count(*) FROM variables WHERE name = 'gone'`); n != 0 {
		t.Errorf("the stale variables row survived an update of its route")
	}
}

// TestPatternTheModelRejects: the store keeps whatever pattern text it is
// given (checking it is not its job), but it attaches no variables to a text
// the link model cannot parse.
func TestPatternTheModelRejects(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	owner := localID(t, s)
	const broken = "odd/{unclosed"

	if got := patternVariables(broken); got != nil {
		t.Errorf("patternVariables(%q) = %q, want none", broken, got)
	}
	created, err := s.CreateLink(ctx, linkmodel.Link{Name: "odd", OwnerID: owner, Routes: []linkmodel.Route{
		{Pattern: broken, Destination: "https://example.com/"},
	}}, owner)
	if err != nil {
		t.Fatalf("CreateLink: %v", err)
	}
	got, err := s.GetLink(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetLink: %v", err)
	}
	if got.Routes[0].Pattern != broken || len(got.Routes[0].Variables) != 0 {
		t.Errorf("route read back as %+v, want the text as given and no variables", got.Routes[0])
	}

	_, err = s.CreateLink(ctx, linkmodel.Link{Name: "odder", OwnerID: owner, Routes: []linkmodel.Route{
		{Pattern: "odder/{unclosed", Destination: "https://example.com/", Variables: []linkmodel.VariableMeta{meta("unclosed", "x", "")}},
	}}, owner)
	if !errors.Is(err, ErrVariableMismatch) {
		t.Errorf("metadata on an unparseable pattern: error = %v, want ErrVariableMismatch", err)
	}
}

// TestRefusedChangesLeaveEveryRowAlone compares the tables themselves before
// and after each kind of refused change. Several of these fail late, after
// the link row and some routes were already rewritten inside the transaction;
// one fails at the very last statement, the revision.
func TestRefusedChangesLeaveEveryRowAlone(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	owner := localID(t, s)
	created := ghLink(t, s)
	other := sampleLink(t, s, "other")
	pr, home, repo := created.Routes[0], created.Routes[1], created.Routes[2]
	nobody := owner + 1000

	valid := func() linkmodel.Link {
		return linkmodel.Link{ID: created.ID, Name: "gh", Description: "after", Routes: []linkmodel.Route{
			{ID: repo.ID, Pattern: "gh/{project}", Destination: "https://example.com/{project}",
				Variables: []linkmodel.VariableMeta{meta("project", "Project", "p")}},
			{ID: pr.ID, Pattern: "gh/{repo}/pr/{num:int}", Destination: "https://example.com/changed"},
			{Pattern: "gh/new", Destination: "https://example.com/new"},
		}}
	}
	with := func(change func(l *linkmodel.Link)) linkmodel.Link {
		l := valid()
		change(&l)
		return l
	}

	tests := []struct {
		name string
		run  func() error
		want error // nil: any error
	}{
		{"update: literal pattern twice", func() error {
			_, err := s.UpdateLink(ctx, with(func(l *linkmodel.Link) {
				l.Routes = append(l.Routes, linkmodel.Route{Pattern: "gh/new", Destination: "https://example.com/again"})
			}), created.Version, owner)
			return err
		}, nil},
		{"update: same shape under another variable name", func() error {
			_, err := s.UpdateLink(ctx, with(func(l *linkmodel.Link) {
				l.Routes = append(l.Routes, linkmodel.Route{Pattern: "gh/{other}", Destination: "https://example.com/{other}"})
			}), created.Version, owner)
			return err
		}, nil},
		{"update: kept route takes a kept route's shape", func() error {
			_, err := s.UpdateLink(ctx, with(func(l *linkmodel.Link) {
				l.Routes = []linkmodel.Route{
					{ID: repo.ID, Pattern: "gh/{a}", Destination: "https://example.com/{a}"},
					{ID: home.ID, Pattern: "gh/{b}", Destination: "https://example.com/{b}"},
				}
			}), created.Version, owner)
			return err
		}, nil},
		{"update: editor does not exist", func() error {
			_, err := s.UpdateLink(ctx, valid(), created.Version, nobody)
			return err
		}, nil},
		{"update: stale version", func() error {
			_, err := s.UpdateLink(ctx, valid(), created.Version+1, owner)
			return err
		}, ErrVersionConflict},
		{"update: missing link", func() error {
			_, err := s.UpdateLink(ctx, with(func(l *linkmodel.Link) { l.ID = 424242 }), 1, owner)
			return err
		}, ErrNotFound},
		{"update: name taken", func() error {
			_, err := s.UpdateLink(ctx, with(func(l *linkmodel.Link) { l.Name = "other" }), created.Version, owner)
			return err
		}, ErrNameTaken},
		{"update: name not normalized", func() error {
			_, err := s.UpdateLink(ctx, with(func(l *linkmodel.Link) { l.Name = "GH" }), created.Version, owner)
			return err
		}, ErrNameNotNormalized},
		{"update: route of another link", func() error {
			_, err := s.UpdateLink(ctx, with(func(l *linkmodel.Link) { l.Routes[2].ID = other.Routes[0].ID }), created.Version, owner)
			return err
		}, ErrRouteNotInLink},
		{"update: metadata for a variable the pattern lacks", func() error {
			_, err := s.UpdateLink(ctx, with(func(l *linkmodel.Link) {
				l.Routes[2].Variables = []linkmodel.VariableMeta{meta("ghost", "x", "")}
			}), created.Version, owner)
			return err
		}, ErrVariableMismatch},
		{"delete: stale version", func() error { return s.DeleteLink(ctx, created.ID, created.Version+1, owner) }, ErrVersionConflict},
		{"delete: missing link", func() error { return s.DeleteLink(ctx, 424242, 0, owner) }, ErrNotFound},
		{"delete: editor does not exist", func() error { return s.DeleteLink(ctx, created.ID, created.Version, nobody) }, nil},
		{"create: editor does not exist", func() error {
			_, err := s.CreateLink(ctx, linkmodel.Link{Name: "fresh", OwnerID: owner, Routes: []linkmodel.Route{
				{Pattern: "fresh/{x}", Destination: "https://example.com/{x}", Variables: []linkmodel.VariableMeta{meta("x", "X", "1")}},
			}}, nobody)
			return err
		}, nil},
		{"create: same shape twice", func() error {
			_, err := s.CreateLink(ctx, linkmodel.Link{Name: "fresh", OwnerID: owner, Routes: []linkmodel.Route{
				{Pattern: "fresh/{x}", Destination: "https://example.com/{x}", Variables: []linkmodel.VariableMeta{meta("x", "X", "1")}},
				{Pattern: "fresh/{y}", Destination: "https://example.com/{y}"},
			}}, owner)
			return err
		}, nil},
	}

	before := dumpTables(t, s)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.run()
			if err == nil {
				t.Fatal("the change succeeded, want it refused")
			}
			if tt.want != nil && !errors.Is(err, tt.want) {
				t.Errorf("error = %v, want %v", err, tt.want)
			}
			t.Logf("error: %v", err)
			if after := dumpTables(t, s); after != before {
				t.Errorf("the refused change altered the tables.\nbefore:\n%s\nafter:\n%s", before, after)
			}
		})
	}

	// The same edit, valid, goes through at the version nobody used up.
	updated, err := s.UpdateLink(ctx, valid(), created.Version, owner)
	if err != nil {
		t.Fatalf("the valid update after all the refused ones: %v", err)
	}
	if updated.Version != created.Version+1 {
		t.Errorf("version = %d, want %d", updated.Version, created.Version+1)
	}
}

// TestDeleteRemovesTheRows: after a delete nothing of the link is left but
// its history.
func TestDeleteRemovesTheRows(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	owner := localID(t, s)
	editor := addUser(t, s, "ada@example.com")
	kept := sampleLink(t, s, "kept")
	doomed := ghLink(t, s)
	if n := count(t, s, `SELECT count(*) FROM variables`); n == 0 {
		t.Fatal("the link to delete has no variables rows; the test would prove nothing")
	}
	s.now = func() time.Time { return time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC) }

	if err := s.DeleteLink(ctx, doomed.ID, doomed.Version, editor); err != nil {
		t.Fatalf("DeleteLink: %v", err)
	}

	if n := count(t, s, `SELECT count(*) FROM links WHERE id = ?`, doomed.ID); n != 0 {
		t.Errorf("the links row is still there")
	}
	if n := count(t, s, `SELECT count(*) FROM routes WHERE link_id = ?`, doomed.ID); n != 0 {
		t.Errorf("%d routes outlived their link", n)
	}
	if n := count(t, s, `SELECT count(*) FROM variables`); n != 0 {
		t.Errorf("%d variables rows outlived their routes", n)
	}
	if n := count(t, s, `SELECT count(*) FROM routes WHERE link_id = ?`, kept.ID); n != 2 {
		t.Errorf("the other link has %d routes after the delete, want 2", n)
	}

	revs, err := s.Revisions(ctx, doomed.ID)
	if err != nil {
		t.Fatalf("Revisions: %v", err)
	}
	if len(revs) != 2 {
		t.Fatalf("the deleted link has %d revisions, want its create and its delete", len(revs))
	}
	last := revs[1]
	if last.Action != RevisionDelete || last.EditorID != editor || last.EditorID == owner ||
		!last.At.Equal(time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)) {
		t.Errorf("delete revision = %+v, want action delete by editor %d at the time of the delete", last, editor)
	}
	// The snapshot is the link as it was: its own times, not the delete's.
	was, err := DecodeSnapshot(last.Snapshot)
	if err != nil {
		t.Fatalf("DecodeSnapshot: %v", err)
	}
	wantSnapshot, err := EncodeSnapshot(doomed)
	if err != nil {
		t.Fatal(err)
	}
	if last.Snapshot != wantSnapshot {
		t.Errorf("delete snapshot =\n%s\nwant the link as it was:\n%s", last.Snapshot, wantSnapshot)
	}
	if was.Version != doomed.Version || !was.UpdatedAt.Equal(doomed.UpdatedAt) || len(was.Routes) != 3 {
		t.Errorf("delete snapshot decodes to %+v", was)
	}
}

// TestRevisionEditorIsNotTheOwner: a revision records who made the change,
// which is not always who owns the link.
func TestRevisionEditorIsNotTheOwner(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	owner := localID(t, s)
	ada := addUser(t, s, "ada@example.com")
	grace := addUser(t, s, "grace@example.com")

	created, err := s.CreateLink(ctx, linkmodel.Link{Name: "docs", OwnerID: owner, Routes: []linkmodel.Route{
		{Pattern: "docs", Destination: "https://example.com/docs"},
	}}, ada)
	if err != nil {
		t.Fatalf("CreateLink: %v", err)
	}
	updated, err := s.UpdateLink(ctx, linkmodel.Link{ID: created.ID, Name: "docs", Description: "by grace", OwnerID: grace, Routes: created.Routes},
		created.Version, grace)
	if err != nil {
		t.Fatalf("UpdateLink: %v", err)
	}
	if created.OwnerID != owner || updated.OwnerID != owner {
		t.Errorf("owner = %d then %d, want %d throughout: editing does not transfer a link", created.OwnerID, updated.OwnerID, owner)
	}
	if err := s.DeleteLink(ctx, created.ID, 0, owner); err != nil {
		t.Fatalf("DeleteLink: %v", err)
	}

	revs, err := s.Revisions(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range revs {
		got = append(got, fmt.Sprintf("%s by %d", r.Action, r.EditorID))
		l, err := DecodeSnapshot(r.Snapshot)
		if err != nil {
			t.Fatalf("DecodeSnapshot: %v", err)
		}
		if l.OwnerID != owner {
			t.Errorf("%s snapshot has owner %d, want %d", r.Action, l.OwnerID, owner)
		}
	}
	want := []string{
		fmt.Sprintf("create by %d", ada), fmt.Sprintf("update by %d", grace), fmt.Sprintf("delete by %d", owner),
	}
	if !slices.Equal(got, want) {
		t.Errorf("revisions = %q, want %q", got, want)
	}
}

// TestSnapshotFormat pins the stored document byte for byte. The format is
// read by later milestones from rows written now, so a change that makes this
// test fail is a change to stored data, not to a test.
func TestSnapshotFormat(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	s.now = func() time.Time { return time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC) }
	created := ghLink(t, s)
	if created.ID != 1 || created.OwnerID != 1 || created.Routes[0].ID != 1 {
		t.Fatalf("ids in a new database: link %d, owner %d, first route %d; the document below assumes 1, 1, 1",
			created.ID, created.OwnerID, created.Routes[0].ID)
	}

	s.now = func() time.Time { return time.Date(2026, 10, 13, 9, 5, 0, 0, time.UTC) }
	if _, err := s.UpdateLink(ctx, linkmodel.Link{ID: created.ID, Name: "hub", Routes: []linkmodel.Route{
		{ID: 3, Pattern: "hub/{repo}", Destination: "https://github.com/acme/{repo}"},
	}}, 1, 1); err != nil {
		t.Fatalf("UpdateLink: %v", err)
	}

	want := []string{
		`{"id":1,"name":"gh","description":"GitHub <shortcuts> & more","ownerId":1,"version":1,` +
			`"createdAt":"2026-10-13T09:00:00Z","updatedAt":"2026-10-13T09:00:00Z","routes":[` +
			`{"id":1,"position":0,"pattern":"gh/{repo}/pr/{num:int}","destination":"https://github.com/acme/{repo}/pull/{num}?a=1&b=2",` +
			`"variables":[{"name":"repo","description":"Repository","example":"ogre"},{"name":"num","description":"","example":""}]},` +
			`{"id":2,"position":1,"pattern":"gh","destination":"https://github.com/acme","variables":[]},` +
			`{"id":3,"position":2,"pattern":"gh/{repo}","destination":"https://github.com/acme/{repo}",` +
			`"variables":[{"name":"repo","description":"","example":"ogre"}]}]}`,
		`{"id":1,"name":"hub","description":"","ownerId":1,"version":2,` +
			`"createdAt":"2026-10-13T09:00:00Z","updatedAt":"2026-10-13T09:05:00Z","routes":[` +
			`{"id":3,"position":0,"pattern":"hub/{repo}","destination":"https://github.com/acme/{repo}",` +
			`"variables":[{"name":"repo","description":"","example":""}]}]}`,
	}
	revs, err := s.Revisions(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(revs) != len(want) {
		t.Fatalf("got %d revisions, want %d", len(revs), len(want))
	}
	for i, w := range want {
		if revs[i].Snapshot != w {
			t.Errorf("snapshot %d =\n%s\nwant\n%s", i, revs[i].Snapshot, w)
		}
	}
}

func TestEncodeSnapshot(t *testing.T) {
	// A link no store returned: nil slices and zero times must still give a
	// complete document, with arrays and not null.
	got, err := EncodeSnapshot(linkmodel.Link{Name: "bare", Routes: []linkmodel.Route{{Pattern: "bare"}}})
	if err != nil {
		t.Fatalf("EncodeSnapshot: %v", err)
	}
	want := `{"id":0,"name":"bare","description":"","ownerId":0,"version":0,` +
		`"createdAt":"0001-01-01T00:00:00Z","updatedAt":"0001-01-01T00:00:00Z","routes":[` +
		`{"id":0,"position":0,"pattern":"bare","destination":"","variables":[]}]}`
	if got != want {
		t.Errorf("EncodeSnapshot =\n%s\nwant\n%s", got, want)
	}

	got, err = EncodeSnapshot(linkmodel.Link{})
	if err != nil {
		t.Fatalf("EncodeSnapshot: %v", err)
	}
	if !strings.Contains(got, `"routes":[]`) || strings.Contains(got, "null") {
		t.Errorf("EncodeSnapshot of an empty link = %s, want \"routes\":[] and no null", got)
	}

	// A time in another zone is written in UTC.
	zone := time.FixedZone("UTC-5", -5*60*60)
	got, err = EncodeSnapshot(linkmodel.Link{CreatedAt: time.Date(2026, 10, 13, 4, 0, 0, 0, zone)})
	if err != nil {
		t.Fatalf("EncodeSnapshot: %v", err)
	}
	if !strings.Contains(got, `"createdAt":"2026-10-13T09:00:00Z"`) {
		t.Errorf("EncodeSnapshot = %s, want createdAt in UTC", got)
	}
}

func TestDecodeSnapshot(t *testing.T) {
	link := linkmodel.Link{
		ID: 7, Name: "gh", Description: "naïve <café> & 日本語\n\"quoted\"", OwnerID: 3, Version: 9,
		CreatedAt: time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 10, 14, 10, 30, 0, 0, time.UTC),
		Routes: []linkmodel.Route{
			{ID: 31, Position: 0, Pattern: "gh/{repo}", Destination: "https://github.com/acme/{repo}?x=<1>&y=2",
				Variables: []linkmodel.VariableMeta{meta("repo", "Dépôt 🚀", "ogre")}},
			{ID: 32, Position: 1, Pattern: "gh", Destination: "https://github.com/acme", Variables: []linkmodel.VariableMeta{}},
		},
	}
	doc, err := EncodeSnapshot(link)
	if err != nil {
		t.Fatalf("EncodeSnapshot: %v", err)
	}
	got, err := DecodeSnapshot(doc)
	if err != nil {
		t.Fatalf("DecodeSnapshot: %v", err)
	}
	again, err := EncodeSnapshot(got)
	if err != nil {
		t.Fatalf("EncodeSnapshot of the decoded link: %v", err)
	}
	if again != doc {
		t.Errorf("encode, decode, encode changed the document:\n%s\nthen\n%s", doc, again)
	}
	if got.ID != link.ID || got.Name != link.Name || got.Description != link.Description || got.OwnerID != link.OwnerID ||
		got.Version != link.Version || !got.CreatedAt.Equal(link.CreatedAt) || !got.UpdatedAt.Equal(link.UpdatedAt) {
		t.Errorf("DecodeSnapshot = %+v, want %+v", got, link)
	}
	for i, r := range link.Routes {
		g := got.Routes[i]
		if g.ID != r.ID || g.Position != r.Position || g.Pattern != r.Pattern || g.Destination != r.Destination ||
			!slices.Equal(g.Variables, r.Variables) || g.Variables == nil {
			t.Errorf("route %d = %+v, want %+v", i, g, r)
		}
	}

	t.Run("fields from a later version are ignored", func(t *testing.T) {
		later := strings.Replace(doc, `{"id":7,`, `{"format":2,"id":7,"coOwners":[4,5],`, 1)
		later = strings.Replace(later, `"pattern":"gh",`, `"pattern":"gh","hits":12,`, 1)
		got, err := DecodeSnapshot(later)
		if err != nil {
			t.Fatalf("DecodeSnapshot: %v", err)
		}
		if again, _ := EncodeSnapshot(got); again != doc {
			t.Errorf("decoded as\n%s\nwant\n%s", again, doc)
		}
	})

	for _, bad := range []struct{ name, doc string }{
		{"empty", ""},
		{"not JSON", "gh -> https://github.com"},
		{"cut short", doc[:len(doc)/2]},
		{"wrong type", `{"id":"seven"}`},
		{"no times", `{"id":7,"name":"gh","routes":[]}`},
		{"bad time", strings.Replace(doc, "2026-10-13T09:00:00Z", "yesterday", 1)},
	} {
		t.Run(bad.name, func(t *testing.T) {
			if l, err := DecodeSnapshot(bad.doc); err == nil {
				t.Errorf("DecodeSnapshot(%q) = %+v, want an error", bad.doc, l)
			}
		})
	}
}

func TestRevisionsRejectsACorruptTime(t *testing.T) {
	s := openTemp(t)
	link := sampleLink(t, s, "roadmap")
	if _, err := s.db.Exec(`UPDATE revisions SET at = 'yesterday'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Revisions(context.Background(), link.ID); err == nil {
		t.Error("Revisions accepted a time that is not RFC 3339")
	}
}

// TestChangesSurviveWithoutClose is TestSavedLinkSurvivesWithoutClose for the
// other two kinds of change: the database and its write-ahead log are copied
// while the store is open, as a crash would leave them, and the copy must
// show the update, the delete and the history of both.
func TestChangesSurviveWithoutClose(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "ogre.db")
	s := openAt(t, path)
	owner := localID(t, s)
	edited := ghLink(t, s)
	deleted := sampleLink(t, s, "doomed")

	updated, err := s.UpdateLink(ctx, linkmodel.Link{ID: edited.ID, Name: "hub", Description: "renamed", Routes: []linkmodel.Route{
		{ID: edited.Routes[2].ID, Pattern: "hub/{repo}", Destination: "https://github.com/acme/{repo}",
			Variables: []linkmodel.VariableMeta{meta("repo", "Repository", "ogre")}},
	}}, edited.Version, owner)
	if err != nil {
		t.Fatalf("UpdateLink: %v", err)
	}
	if err := s.DeleteLink(ctx, deleted.ID, deleted.Version, owner); err != nil {
		t.Fatalf("DeleteLink: %v", err)
	}

	crashed := filepath.Join(t.TempDir(), "ogre.db")
	copyFile(t, path, crashed)
	if _, err := os.Stat(path + "-wal"); err != nil {
		t.Fatalf("expected a write-ahead log next to the open database: %v", err)
	}
	copyFile(t, path+"-wal", crashed+"-wal")

	recovered := openAt(t, crashed)
	links, err := recovered.ListLinks(ctx)
	if err != nil {
		t.Fatalf("ListLinks on the recovered copy: %v", err)
	}
	if len(links) != 1 {
		t.Fatalf("the recovered copy holds %d links, want only the updated one", len(links))
	}
	got := links[0]
	if got.ID != updated.ID || got.Name != "hub" || got.Version != 2 || len(got.Routes) != 1 ||
		got.Routes[0].ID != edited.Routes[2].ID || !slices.Equal(got.Routes[0].Variables, updated.Routes[0].Variables) {
		t.Errorf("recovered link = %+v, want %+v", got, updated)
	}
	for id, want := range map[int64][]string{
		edited.ID:  {RevisionCreate, RevisionUpdate},
		deleted.ID: {RevisionCreate, RevisionDelete},
	} {
		revs, err := recovered.Revisions(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		var actions []string
		for _, r := range revs {
			actions = append(actions, r.Action)
		}
		if !slices.Equal(actions, want) {
			t.Errorf("recovered revisions of link %d = %q, want %q", id, actions, want)
		}
	}
}

// A stored pattern is read the way the resolver reads it, not by the rules
// for saving one. A route whose regular expression would be refused today
// (the limits were tighter, or a later Go release compiles it larger) still
// resolves, so it must also keep the descriptions of its variables: reading
// it by the save rules made them vanish without a word.
func TestStoredPatternKeepsItsVariablesWhenASaveRuleTightens(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	owner := localID(t, s)
	const pattern = "big/{v:regex((?:a?){510})}/{n:int}" // over linkmodel.MaxRegexProgram

	if _, problems := linkmodel.ParsePattern(pattern); len(problems) != 1 || problems[0].Code != linkmodel.CodePatternRegexTooComplex {
		t.Fatalf("ParsePattern(%q) = %+v; the test needs a pattern that can no longer be saved", pattern, problems)
	}
	if got := patternVariables(pattern); !slices.Equal(got, []string{"v", "n"}) {
		t.Fatalf("patternVariables(%q) = %q, want v and n", pattern, got)
	}
	created, err := s.CreateLink(ctx, linkmodel.Link{Name: "big", OwnerID: owner, Routes: []linkmodel.Route{
		{Pattern: pattern, Destination: "https://example.com/{n}?v={v}", Variables: []linkmodel.VariableMeta{meta("n", "A number", "7")}},
	}}, owner)
	if err != nil {
		t.Fatalf("CreateLink: %v", err)
	}
	got, err := s.GetLink(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetLink: %v", err)
	}
	want := []linkmodel.VariableMeta{meta("v", "", ""), meta("n", "A number", "7")}
	if !slices.Equal(got.Routes[0].Variables, want) {
		t.Errorf("variables read back as %+v, want %+v", got.Routes[0].Variables, want)
	}
	// And the route still resolves, with the same reading.
	if _, ok, _, err := linkmodel.Compile(got).Match([]string{"big", "aaa", "7"}); !ok || err != nil {
		t.Errorf("the stored route does not match: ok %v, err %v", ok, err)
	}
}
