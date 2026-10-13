package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	"github.com/tfrydrychewicz/ogre/internal/linkmodel"
)

// parkedShapePrefix starts the placeholder that UpdateLink writes into
// routes.shape while it rearranges a link's routes (see replaceRoutes). A
// real shape begins with the link's name, and a name cannot contain "/", so
// a placeholder can never equal a shape.
const parkedShapePrefix = "/"

// nextLinkID is the SQL expression for the ID of a new link: one more than
// the highest ID any link has ever had.
//
// Left to itself SQLite gives a new row the highest ID in the table plus one,
// so the ID of the newest link, once that link is deleted, would go to the
// next link created. A link's ID must never come back, for two reasons. Its
// revisions stay behind under that ID, and a new link would inherit them as
// its own history. And an edit is addressed by ID and version: a save from a
// page left open on the deleted link, at version 1, would silently overwrite
// the new link, also at version 1.
//
// The revisions table remembers the IDs that are gone: every link created
// since revisions are written has at least one, and revisions are never
// deleted. (Links older than that were never deleted without one either: the
// delete writes it.) Whatever removes revisions in the future must keep this
// in mind. Both lookups are answered from an index.
const nextLinkID = `(SELECT 1 + max(
	(SELECT coalesce(max(id), 0) FROM links),
	(SELECT coalesce(max(link_id), 0) FROM revisions)))`

// CreateLink stores l, its routes and the "create" revision in one
// transaction; see Store.
func (s *SQLite) CreateLink(ctx context.Context, l linkmodel.Link, editorID int64) (linkmodel.Link, error) {
	// The UNIQUE constraint compares the stored text, so it only keeps names
	// unique ignoring case if every stored name is in one spelling.
	if l.Name != linkmodel.NormalizeName(l.Name) {
		return linkmodel.Link{}, fmt.Errorf("store: create link %q: %w", l.Name, ErrNameNotNormalized)
	}
	routes, err := routesToStore(l.Routes)
	if err != nil {
		return linkmodel.Link{}, fmt.Errorf("store: create link %q: %w", l.Name, err)
	}
	now := s.timestamp()
	out := linkmodel.Link{
		Name: l.Name, Description: l.Description, OwnerID: l.OwnerID,
		Version: 1, CreatedAt: now, UpdatedAt: now, Routes: routes,
	}

	err = s.write(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`INSERT INTO links (id, name, description, owner_id, version, created_at, updated_at)
			 VALUES (`+nextLinkID+`, ?, ?, ?, 1, ?, ?)`,
			out.Name, out.Description, out.OwnerID, formatTime(now), formatTime(now))
		if err != nil {
			// links has two unique values, the id and the name. The id is
			// computed above every id in use, inside a transaction that
			// already holds the write lock, so it cannot clash; a unique
			// violation raised by this insert can only mean the name is
			// taken. Deciding by statement and result code avoids reading
			// SQLite's message text.
			if isUniqueViolation(err) {
				return ErrNameTaken
			}
			return err
		}
		if out.ID, err = res.LastInsertId(); err != nil {
			return err
		}
		for i := range out.Routes {
			out.Routes[i].ID = 0 // whatever the caller put there is not an ID of ours
			if err := insertRoute(ctx, tx, out.ID, &out.Routes[i]); err != nil {
				return fmt.Errorf("route %d: %w", i+1, err)
			}
		}
		return insertRevision(ctx, tx, RevisionCreate, editorID, now, out)
	})
	if err != nil {
		return linkmodel.Link{}, fmt.Errorf("store: create link %q: %w", l.Name, err)
	}
	return out, nil
}

// GetLink returns one link with its routes; see Store.
func (s *SQLite) GetLink(ctx context.Context, id int64) (linkmodel.Link, error) {
	var l linkmodel.Link
	err := s.read(ctx, func(tx *sql.Tx) (err error) {
		l, err = loadLink(ctx, tx, id)
		return err
	})
	if err != nil {
		return linkmodel.Link{}, fmt.Errorf("store: get link %d: %w", id, err)
	}
	return l, nil
}

// ListLinks returns every link with its routes; see Store.
func (s *SQLite) ListLinks(ctx context.Context) ([]linkmodel.Link, error) {
	var links []linkmodel.Link
	err := s.read(ctx, func(tx *sql.Tx) (err error) {
		if links, err = queryLinks(ctx, tx, nil); err != nil {
			return err
		}
		return attachRoutes(ctx, tx, links, nil)
	})
	if err != nil {
		return nil, fmt.Errorf("store: list links: %w", err)
	}
	return links, nil
}

// UpdateLink replaces a link's name, description and routes and writes the
// "update" revision, all in one transaction; see Store.
func (s *SQLite) UpdateLink(ctx context.Context, l linkmodel.Link, expectedVersion int, editorID int64) (linkmodel.Link, error) {
	if l.Name != linkmodel.NormalizeName(l.Name) {
		return linkmodel.Link{}, fmt.Errorf("store: update link %d: name %q: %w", l.ID, l.Name, ErrNameNotNormalized)
	}
	routes, err := routesToStore(l.Routes)
	if err != nil {
		return linkmodel.Link{}, fmt.Errorf("store: update link %d: %w", l.ID, err)
	}
	now := s.timestamp()
	out := linkmodel.Link{ID: l.ID, Name: l.Name, Description: l.Description, UpdatedAt: now, Routes: routes}

	err = s.write(ctx, func(tx *sql.Tx) error {
		// The version test is part of the statement that takes the new
		// version, so two editors who both read version 3 cannot both write
		// version 4, whatever the database does about isolation: the second
		// statement finds no row at version 3.
		res, err := tx.ExecContext(ctx,
			`UPDATE links SET name = ?, description = ?, version = version + 1, updated_at = ?
			 WHERE id = ? AND version = ?`,
			out.Name, out.Description, formatTime(now), out.ID, expectedVersion)
		if err != nil {
			// As in CreateLink: this statement leaves the id alone, so the
			// only unique value it can collide on is another link's name.
			if isUniqueViolation(err) {
				return ErrNameTaken
			}
			return err
		}
		changed, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if changed == 0 {
			// No row matched: either there is no such link, or it is at
			// another version. The caller needs to know which.
			stored, err := linkVersion(ctx, tx, out.ID)
			if err != nil {
				return err
			}
			return fmt.Errorf("it is at version %d, not %d: %w", stored, expectedVersion, ErrVersionConflict)
		}

		// What an update does not change is read back, not trusted from l.
		var created string
		if err := tx.QueryRowContext(ctx,
			`SELECT owner_id, version, created_at FROM links WHERE id = ?`, out.ID,
		).Scan(&out.OwnerID, &out.Version, &created); err != nil {
			return err
		}
		if out.CreatedAt, err = parseTime("links.created_at", created); err != nil {
			return err
		}

		if err := replaceRoutes(ctx, tx, out.ID, out.Routes); err != nil {
			return err
		}
		return insertRevision(ctx, tx, RevisionUpdate, editorID, now, out)
	})
	if err != nil {
		return linkmodel.Link{}, fmt.Errorf("store: update link %d: %w", l.ID, err)
	}
	return out, nil
}

// DeleteLink removes a link and writes the "delete" revision in one
// transaction; see Store.
func (s *SQLite) DeleteLink(ctx context.Context, id int64, expectedVersion int, editorID int64) error {
	now := s.timestamp()
	err := s.write(ctx, func(tx *sql.Tx) error {
		// The snapshot of a delete is the link as it was, so it is read
		// first. Reading before writing is safe here because a write
		// transaction holds the write lock from its first statement (see
		// dsn); the version test is repeated in the DELETE all the same, so
		// the check does not rest on that alone.
		was, err := loadLink(ctx, tx, id)
		if err != nil {
			return err
		}
		if expectedVersion != 0 && was.Version != expectedVersion {
			return fmt.Errorf("it is at version %d, not %d: %w", was.Version, expectedVersion, ErrVersionConflict)
		}
		if err := insertRevision(ctx, tx, RevisionDelete, editorID, now, was); err != nil {
			return err
		}
		// Routes go with the link, and variables with their routes, by
		// ON DELETE CASCADE. Revisions have no such reference on purpose.
		res, err := tx.ExecContext(ctx, `DELETE FROM links WHERE id = ? AND version = ?`, id, was.Version)
		if err != nil {
			return err
		}
		removed, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if removed != 1 {
			return fmt.Errorf("it changed while being deleted: %w", ErrVersionConflict)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("store: delete link %d: %w", id, err)
	}
	return nil
}

// write runs fn in one write transaction: committed when fn returns nil,
// rolled back, with nothing of it kept, when fn fails or panics.
func (s *SQLite) write(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	// After a successful Commit this is a no-op that reports ErrTxDone.
	defer func() { _ = tx.Rollback() }()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// read runs fn in one read transaction, so that links, routes and variables
// come from the same moment even if another process writes in between the
// queries.
func (s *SQLite) read(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	return fn(tx)
}

// isUniqueViolation reports whether err is SQLite refusing a row because of a
// UNIQUE constraint. The driver enables extended result codes, so the code
// distinguishes it from foreign-key, NOT NULL and CHECK failures.
func isUniqueViolation(err error) bool {
	var se *sqlite.Error
	return errors.As(err, &se) && se.Code() == sqlite3.SQLITE_CONSTRAINT_UNIQUE
}

// linkVersion returns the stored version of a link, or ErrNotFound.
func linkVersion(ctx context.Context, tx *sql.Tx, id int64) (int, error) {
	var version int
	err := tx.QueryRowContext(ctx, `SELECT version FROM links WHERE id = ?`, id).Scan(&version)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	return version, err
}

// patternVariables returns the names of the variables of a pattern, in
// pattern order.
//
// Which variables a pattern has is the link model's rule, so it is asked and
// not derived here; the store never looks inside a pattern. A text that is
// not a valid pattern has no variables as far as the store is concerned: it
// keeps such a route (checking patterns is not its job) but will not attach
// metadata to it.
//
// The pattern is read the way every stored pattern is read
// (linkmodel.StoredPattern), not by the rules for saving one: a route that
// still resolves must not lose its descriptions because a save rule was
// tightened after it was written.
func patternVariables(pattern string) []string {
	parsed, ok := linkmodel.StoredPattern(pattern)
	if !ok {
		return nil
	}
	return parsed.Variables()
}

// variablesFor returns one entry per variable of pattern, in pattern order,
// taking each description and example from meta, which is keyed by variable
// name.
func variablesFor(pattern string, meta map[string]linkmodel.VariableMeta) []linkmodel.VariableMeta {
	return variablesIn(patternVariables(pattern), meta)
}

// variablesIn returns one entry per name, in that order, taking each
// description and example from meta. It is the one place that builds
// Route.Variables, for links being stored and for links being read, which is
// what makes the two agree. The result is never nil.
func variablesIn(names []string, meta map[string]linkmodel.VariableMeta) []linkmodel.VariableMeta {
	vars := make([]linkmodel.VariableMeta, len(names))
	for i, name := range names {
		vars[i] = linkmodel.VariableMeta{Name: name, Description: meta[name].Description, Example: meta[name].Example}
	}
	return vars
}

// routesToStore returns the routes as they will be stored and returned: a
// fresh slice (the caller's is left alone) with positions in slice order and
// each route's Variables brought to the form Store promises. IDs are kept as
// given; CreateLink and UpdateLink decide what they mean.
//
// The caller's Variables may be incomplete or in any order. An entry for a
// name the pattern does not declare, or a second entry for one name, is an
// ErrVariableMismatch.
func routesToStore(in []linkmodel.Route) ([]linkmodel.Route, error) {
	out := make([]linkmodel.Route, len(in))
	for i, r := range in {
		names := patternVariables(r.Pattern)
		declared := make(map[string]bool, len(names))
		for _, name := range names {
			declared[name] = true
		}
		meta := make(map[string]linkmodel.VariableMeta, len(r.Variables))
		for _, v := range r.Variables {
			if !declared[v.Name] {
				return nil, fmt.Errorf("route %d (%s): the pattern has no variable %q: %w", i+1, r.Pattern, v.Name, ErrVariableMismatch)
			}
			if _, twice := meta[v.Name]; twice {
				return nil, fmt.Errorf("route %d (%s): variable %q is described twice: %w", i+1, r.Pattern, v.Name, ErrVariableMismatch)
			}
			meta[v.Name] = v
		}
		out[i] = linkmodel.Route{
			ID: r.ID, Position: i, Pattern: r.Pattern, Destination: r.Destination,
			Variables: variablesIn(names, meta),
		}
	}
	return out, nil
}

// insertRoute stores r as a new route of the link and sets r.ID.
func insertRoute(ctx context.Context, tx *sql.Tx, linkID int64, r *linkmodel.Route) error {
	// The shape column lets the database refuse two routes of one link that
	// would match the same requests. What a pattern's shape is, is the link
	// model's rule, so it is asked and not derived here.
	res, err := tx.ExecContext(ctx,
		`INSERT INTO routes (link_id, position, pattern, shape, destination) VALUES (?, ?, ?, ?, ?)`,
		linkID, r.Position, r.Pattern, linkmodel.Shape(r.Pattern), r.Destination)
	if err != nil {
		return err
	}
	if r.ID, err = res.LastInsertId(); err != nil {
		return err
	}
	return insertVariables(ctx, tx, *r)
}

// insertVariables stores the metadata of r's variables. The route must have
// no variables rows yet.
//
// A variable with neither a description nor an example gets no row: the
// table holds only what the pattern text does not carry, and for such a
// variable that is nothing. Reading fills it in from the pattern.
func insertVariables(ctx context.Context, tx *sql.Tx, r linkmodel.Route) error {
	for _, v := range r.Variables {
		if v.Description == "" && v.Example == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO variables (route_id, name, description, example) VALUES (?, ?, ?, ?)`,
			r.ID, v.Name, v.Description, v.Example); err != nil {
			return fmt.Errorf("variable %q: %w", v.Name, err)
		}
	}
	return nil
}

// replaceRoutes makes the stored routes of a link equal to routes, keeping
// the rows, and so the IDs, of the routes that are named by ID. It sets the
// ID of each new route in routes.
func replaceRoutes(ctx context.Context, tx *sql.Tx, linkID int64, routes []linkmodel.Route) error {
	stored, err := routeIDs(ctx, tx, linkID)
	if err != nil {
		return err
	}
	kept := make(map[int64]bool, len(routes))
	for i, r := range routes {
		switch {
		case r.ID == 0:
			continue
		case !stored[r.ID]:
			return fmt.Errorf("route %d (%s) has id %d: %w", i+1, r.Pattern, r.ID, ErrRouteNotInLink)
		case kept[r.ID]:
			return fmt.Errorf("route %d (%s) repeats id %d: %w", i+1, r.Pattern, r.ID, ErrRouteNotInLink)
		}
		kept[r.ID] = true
	}

	// SQLite checks UNIQUE (link_id, shape) row by row, not at commit. Two
	// kept routes that swap patterns would therefore collide on the first of
	// the two updates although the end result is fine, and so would a new
	// route that takes the pattern of one being removed. So every stored
	// route first moves to a placeholder shape of its own, out of the way,
	// and the loop below gives each kept route its real one. Two routes that
	// end up with the same shape still collide there, as they must.
	if _, err := tx.ExecContext(ctx,
		`UPDATE routes SET shape = ? || id WHERE link_id = ?`, parkedShapePrefix, linkID); err != nil {
		return err
	}
	// Metadata is replaced as a whole, like the routes it belongs to.
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM variables WHERE route_id IN (SELECT id FROM routes WHERE link_id = ?)`, linkID); err != nil {
		return err
	}

	for i := range routes {
		r := &routes[i]
		if r.ID == 0 {
			if err := insertRoute(ctx, tx, linkID, r); err != nil {
				return fmt.Errorf("route %d: %w", i+1, err)
			}
			continue
		}
		// "AND link_id" repeats what the check above established: this
		// statement must never be able to reach another link's route.
		if _, err := tx.ExecContext(ctx,
			`UPDATE routes SET position = ?, pattern = ?, shape = ?, destination = ?
			 WHERE id = ? AND link_id = ?`,
			r.Position, r.Pattern, linkmodel.Shape(r.Pattern), r.Destination, r.ID, linkID); err != nil {
			return fmt.Errorf("route %d: %w", i+1, err)
		}
		if err := insertVariables(ctx, tx, *r); err != nil {
			return fmt.Errorf("route %d: %w", i+1, err)
		}
	}

	// The routes that were not named go last, after the new ones exist.
	// SQLite numbers a new row one above the highest ID in the table, so a
	// route inserted after the removal of the table's newest route would
	// get that route's ID back, and "a new route gets a new ID" would be
	// false within a single update. (Across updates the routes table gives
	// no such guarantee; see the note on route IDs at Store.UpdateLink.)
	for id := range stored {
		if kept[id] {
			continue
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM routes WHERE id = ?`, id); err != nil {
			return err
		}
	}
	return nil
}

// routeIDs returns the IDs of the stored routes of a link.
func routeIDs(ctx context.Context, tx *sql.Tx, linkID int64) (map[int64]bool, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id FROM routes WHERE link_id = ?`, linkID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	ids := map[int64]bool{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids[id] = true
	}
	return ids, rows.Err()
}

// loadLink reads one link with its routes, or returns ErrNotFound.
func loadLink(ctx context.Context, tx *sql.Tx, id int64) (linkmodel.Link, error) {
	links, err := queryLinks(ctx, tx, &id)
	if err != nil {
		return linkmodel.Link{}, err
	}
	if len(links) == 0 {
		return linkmodel.Link{}, ErrNotFound
	}
	if err := attachRoutes(ctx, tx, links, &id); err != nil {
		return linkmodel.Link{}, err
	}
	return links[0], nil
}

// queryLinks reads the links table ordered by name, without routes: every
// link, or only the one with ID *only. The result is never nil.
func queryLinks(ctx context.Context, tx *sql.Tx, only *int64) ([]linkmodel.Link, error) {
	query := `SELECT id, name, description, owner_id, version, created_at, updated_at FROM links`
	var args []any
	if only != nil {
		query += ` WHERE id = ?`
		args = append(args, *only)
	}
	rows, err := tx.QueryContext(ctx, query+` ORDER BY name`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	links := []linkmodel.Link{}
	for rows.Next() {
		l := linkmodel.Link{Routes: []linkmodel.Route{}}
		var created, updated string
		if err := rows.Scan(&l.ID, &l.Name, &l.Description, &l.OwnerID, &l.Version, &created, &updated); err != nil {
			return nil, err
		}
		if l.CreatedAt, err = parseTime("links.created_at", created); err != nil {
			return nil, err
		}
		if l.UpdatedAt, err = parseTime("links.updated_at", updated); err != nil {
			return nil, err
		}
		links = append(links, l)
	}
	return links, rows.Err()
}

// attachRoutes fills in the routes of links, each list ordered by position
// and each route with its variables. With only set, links holds that one link
// and only its routes are read.
func attachRoutes(ctx context.Context, tx *sql.Tx, links []linkmodel.Link, only *int64) error {
	meta, err := queryVariables(ctx, tx, only)
	if err != nil {
		return err
	}
	byID := make(map[int64]int, len(links))
	for i, l := range links {
		byID[l.ID] = i
	}

	query := `SELECT id, link_id, position, pattern, destination FROM routes`
	var args []any
	if only != nil {
		query += ` WHERE link_id = ?`
		args = append(args, *only)
	}
	rows, err := tx.QueryContext(ctx, query+` ORDER BY link_id, position, id`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var r linkmodel.Route
		var linkID int64
		if err := rows.Scan(&r.ID, &linkID, &r.Position, &r.Pattern, &r.Destination); err != nil {
			return err
		}
		i, ok := byID[linkID]
		if !ok {
			// Foreign keys make this impossible; say so loudly if it happens.
			return fmt.Errorf("route %d belongs to link %d, which does not exist", r.ID, linkID)
		}
		r.Variables = variablesFor(r.Pattern, meta[r.ID])
		links[i].Routes = append(links[i].Routes, r)
	}
	return rows.Err()
}

// queryVariables reads the variables table into route ID -> variable name ->
// metadata: for every route, or only for the routes of the link *only.
func queryVariables(ctx context.Context, tx *sql.Tx, only *int64) (map[int64]map[string]linkmodel.VariableMeta, error) {
	query := `SELECT v.route_id, v.name, v.description, v.example FROM variables v`
	var args []any
	if only != nil {
		query += ` JOIN routes r ON r.id = v.route_id WHERE r.link_id = ?`
		args = append(args, *only)
	}
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	meta := map[int64]map[string]linkmodel.VariableMeta{}
	for rows.Next() {
		var routeID int64
		var v linkmodel.VariableMeta
		if err := rows.Scan(&routeID, &v.Name, &v.Description, &v.Example); err != nil {
			return nil, err
		}
		if meta[routeID] == nil {
			meta[routeID] = map[string]linkmodel.VariableMeta{}
		}
		meta[routeID][v.Name] = v
	}
	return meta, rows.Err()
}
