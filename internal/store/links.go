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

// CreateLink stores l and its routes in one transaction; see Store.
func (s *SQLite) CreateLink(ctx context.Context, l linkmodel.Link) (_ linkmodel.Link, err error) {
	// The UNIQUE constraint compares the stored text, so it only keeps names
	// unique ignoring case if every stored name is in one spelling.
	if l.Name != linkmodel.NormalizeName(l.Name) {
		return linkmodel.Link{}, fmt.Errorf("store: create link %q: %w", l.Name, ErrNameNotNormalized)
	}
	now := s.timestamp()
	stamp := formatTime(now)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return linkmodel.Link{}, fmt.Errorf("store: create link: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	res, err := tx.ExecContext(ctx,
		`INSERT INTO links (name, description, owner_id, version, created_at, updated_at)
		 VALUES (?, ?, ?, 1, ?, ?)`,
		l.Name, l.Description, l.OwnerID, stamp, stamp)
	if err != nil {
		// links has a single UNIQUE constraint, the name, and this statement
		// supplies no id that could clash; so a unique violation raised by
		// this insert can only mean the name is taken. Deciding by statement
		// and result code avoids reading SQLite's message text.
		if isUniqueViolation(err) {
			return linkmodel.Link{}, ErrNameTaken
		}
		return linkmodel.Link{}, fmt.Errorf("store: create link %q: %w", l.Name, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return linkmodel.Link{}, fmt.Errorf("store: create link %q: %w", l.Name, err)
	}

	out := l
	out.ID = id
	out.Version = 1
	out.CreatedAt = now
	out.UpdatedAt = now
	// A fresh slice: the caller's routes are not modified.
	out.Routes = make([]linkmodel.Route, len(l.Routes))
	for i, r := range l.Routes {
		// The shape column lets the database refuse two routes of one link
		// that would match the same requests. What a pattern's shape is, is
		// the link model's rule, so it is asked and not derived here.
		res, err = tx.ExecContext(ctx,
			`INSERT INTO routes (link_id, position, pattern, shape, destination)
			 VALUES (?, ?, ?, ?, ?)`,
			id, i, r.Pattern, linkmodel.Shape(r.Pattern), r.Destination)
		if err != nil {
			return linkmodel.Link{}, fmt.Errorf("store: create link %q: route %d: %w", l.Name, i+1, err)
		}
		var routeID int64
		if routeID, err = res.LastInsertId(); err != nil {
			return linkmodel.Link{}, fmt.Errorf("store: create link %q: route %d: %w", l.Name, i+1, err)
		}
		out.Routes[i] = linkmodel.Route{ID: routeID, Position: i, Pattern: r.Pattern, Destination: r.Destination}
	}

	if err = tx.Commit(); err != nil {
		return linkmodel.Link{}, fmt.Errorf("store: create link %q: %w", l.Name, err)
	}
	return out, nil
}

// isUniqueViolation reports whether err is SQLite refusing a row because of a
// UNIQUE constraint. The driver enables extended result codes, so the code
// distinguishes it from foreign-key, NOT NULL and CHECK failures.
func isUniqueViolation(err error) bool {
	var se *sqlite.Error
	return errors.As(err, &se) && se.Code() == sqlite3.SQLITE_CONSTRAINT_UNIQUE
}

// ListLinks returns every link with its routes; see Store.
func (s *SQLite) ListLinks(ctx context.Context) ([]linkmodel.Link, error) {
	// One read transaction, so links and routes come from the same moment
	// even if another process writes in between the two queries.
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("store: list links: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	links, err := queryLinks(ctx, tx)
	if err != nil {
		return nil, fmt.Errorf("store: list links: %w", err)
	}
	if err := attachRoutes(ctx, tx, links); err != nil {
		return nil, fmt.Errorf("store: list links: %w", err)
	}
	return links, nil
}

// queryLinks reads the links table ordered by name, without routes.
func queryLinks(ctx context.Context, tx *sql.Tx) ([]linkmodel.Link, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT id, name, description, owner_id, version, created_at, updated_at
		 FROM links ORDER BY name`)
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

// attachRoutes fills in the routes of links, each list ordered by position.
func attachRoutes(ctx context.Context, tx *sql.Tx, links []linkmodel.Link) error {
	byID := make(map[int64]int, len(links))
	for i, l := range links {
		byID[l.ID] = i
	}
	rows, err := tx.QueryContext(ctx,
		`SELECT id, link_id, position, pattern, destination
		 FROM routes ORDER BY link_id, position, id`)
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
		links[i].Routes = append(links[i].Routes, r)
	}
	return rows.Err()
}
