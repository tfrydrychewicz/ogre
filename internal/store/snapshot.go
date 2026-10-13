package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/tfrydrychewicz/ogre/internal/linkmodel"
)

// linkSnapshot is the JSON document kept in revisions.snapshot: one whole
// link, frozen at one moment. This type and its two helpers below are the
// only definition of that format.
//
// The format is STABLE. Rows written today are read by the history screen
// and by revert (L5) for as long as the database lives, so a field may be
// added but none may be renamed, removed or change its meaning. A reader
// must ignore fields it does not know, and DecodeSnapshot does.
//
//	{
//	  "id": 12,
//	  "name": "gh",
//	  "description": "GitHub shortcuts",
//	  "ownerId": 1,
//	  "version": 3,
//	  "createdAt": "2026-10-13T09:00:00Z",
//	  "updatedAt": "2026-10-13T09:05:00Z",
//	  "routes": [
//	    {
//	      "id": 31,
//	      "position": 0,
//	      "pattern": "gh/{repo}/pr/{num:int}",
//	      "destination": "https://github.com/acme/{repo}/pull/{num}",
//	      "variables": [
//	        { "name": "repo", "description": "Repository", "example": "ogre" },
//	        { "name": "num", "description": "", "example": "" }
//	      ]
//	    }
//	  ]
//	}
//
// Rules of the format:
//
//   - Every field is always present. "routes" and "variables" are arrays,
//     never null, and are in route order and pattern order.
//   - "variables" lists every variable of the pattern, also those with no
//     description and no example, so a reader needs no pattern parser to
//     know a route's variables. A variable's type is not repeated: it is
//     part of the pattern text.
//   - Times are RFC 3339 in UTC, whole seconds, as in the tables.
//   - Text is stored as written: "<", ">" and "&" are not turned into
//     \u escapes, so a destination reads the same here as in the routes
//     table.
//
// It is the store's own record and not the export format of plan 3.5, which
// identifies an owner by e-mail and carries no IDs because it travels between
// Directories; a snapshot stays in the database that made it, where the IDs
// mean something.
type linkSnapshot struct {
	ID          int64           `json:"id"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	OwnerID     int64           `json:"ownerId"`
	Version     int             `json:"version"`
	CreatedAt   string          `json:"createdAt"`
	UpdatedAt   string          `json:"updatedAt"`
	Routes      []routeSnapshot `json:"routes"`
}

// routeSnapshot is one route inside a linkSnapshot.
type routeSnapshot struct {
	ID          int64              `json:"id"`
	Position    int                `json:"position"`
	Pattern     string             `json:"pattern"`
	Destination string             `json:"destination"`
	Variables   []variableSnapshot `json:"variables"`
}

// variableSnapshot is one variable's metadata inside a routeSnapshot.
type variableSnapshot struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Example     string `json:"example"`
}

// EncodeSnapshot renders l as the JSON document stored with a revision. The
// format is documented once, at linkSnapshot. The same link always encodes to
// the same bytes.
func EncodeSnapshot(l linkmodel.Link) (string, error) {
	doc := linkSnapshot{
		ID:          l.ID,
		Name:        l.Name,
		Description: l.Description,
		OwnerID:     l.OwnerID,
		Version:     l.Version,
		CreatedAt:   formatTime(l.CreatedAt),
		UpdatedAt:   formatTime(l.UpdatedAt),
		Routes:      make([]routeSnapshot, len(l.Routes)),
	}
	for i, r := range l.Routes {
		vars := make([]variableSnapshot, len(r.Variables))
		for j, v := range r.Variables {
			vars[j] = variableSnapshot{Name: v.Name, Description: v.Description, Example: v.Example}
		}
		doc.Routes[i] = routeSnapshot{
			ID: r.ID, Position: r.Position, Pattern: r.Pattern, Destination: r.Destination, Variables: vars,
		}
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	// json.Marshal would write "&" as &, and nearly every destination
	// with a query has one.
	enc.SetEscapeHTML(false)
	if err := enc.Encode(doc); err != nil {
		return "", fmt.Errorf("store: encode snapshot of link %q: %w", l.Name, err)
	}
	return string(bytes.TrimRight(buf.Bytes(), "\n")), nil
}

// DecodeSnapshot reads a document written by EncodeSnapshot back into a link.
// Decoding what EncodeSnapshot wrote gives back the same link, with Routes
// and every route's Variables non-nil. Fields this version does not know are
// ignored, so a snapshot written by a later version still reads.
func DecodeSnapshot(snapshot string) (linkmodel.Link, error) {
	var doc linkSnapshot
	if err := json.Unmarshal([]byte(snapshot), &doc); err != nil {
		return linkmodel.Link{}, fmt.Errorf("store: decode snapshot: %w", err)
	}
	created, err := parseTime("snapshot createdAt", doc.CreatedAt)
	if err != nil {
		return linkmodel.Link{}, err
	}
	updated, err := parseTime("snapshot updatedAt", doc.UpdatedAt)
	if err != nil {
		return linkmodel.Link{}, err
	}
	l := linkmodel.Link{
		ID:          doc.ID,
		Name:        doc.Name,
		Description: doc.Description,
		OwnerID:     doc.OwnerID,
		Version:     doc.Version,
		CreatedAt:   created,
		UpdatedAt:   updated,
		Routes:      make([]linkmodel.Route, len(doc.Routes)),
	}
	for i, r := range doc.Routes {
		vars := make([]linkmodel.VariableMeta, len(r.Variables))
		for j, v := range r.Variables {
			vars[j] = linkmodel.VariableMeta{Name: v.Name, Description: v.Description, Example: v.Example}
		}
		l.Routes[i] = linkmodel.Route{
			ID: r.ID, Position: r.Position, Pattern: r.Pattern, Destination: r.Destination, Variables: vars,
		}
	}
	return l, nil
}

// insertRevision records one change to a link inside the transaction that
// makes the change, so the history can never disagree with the data: a change
// that is rolled back leaves no revision, and a revision that cannot be
// written (an editor who does not exist) undoes the change.
func insertRevision(ctx context.Context, tx *sql.Tx, action string, editorID int64, at time.Time, l linkmodel.Link) error {
	snapshot, err := EncodeSnapshot(l)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO revisions (link_id, editor_id, at, action, snapshot) VALUES (?, ?, ?, ?, ?)`,
		l.ID, editorID, formatTime(at), action, snapshot); err != nil {
		return fmt.Errorf("record the %s revision (editor %d): %w", action, editorID, err)
	}
	return nil
}

var _ RevisionReader = (*SQLite)(nil)

// Revisions returns the history of one link, oldest first; see
// RevisionReader.
func (s *SQLite) Revisions(ctx context.Context, linkID int64) ([]Revision, error) {
	// Revision IDs are handed out in commit order by the single writer, so
	// ordering by ID is ordering by time, also within one second.
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, link_id, editor_id, at, action, snapshot
		 FROM revisions WHERE link_id = ? ORDER BY id`, linkID)
	if err != nil {
		return nil, fmt.Errorf("store: revisions of link %d: %w", linkID, err)
	}
	defer rows.Close()

	revisions := []Revision{}
	for rows.Next() {
		var r Revision
		var at string
		if err := rows.Scan(&r.ID, &r.LinkID, &r.EditorID, &at, &r.Action, &r.Snapshot); err != nil {
			return nil, fmt.Errorf("store: revisions of link %d: %w", linkID, err)
		}
		if r.At, err = parseTime("revisions.at", at); err != nil {
			return nil, err
		}
		revisions = append(revisions, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: revisions of link %d: %w", linkID, err)
	}
	return revisions, nil
}
