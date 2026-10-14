package perf

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"path/filepath"
	"time"

	"github.com/tfrydrychewicz/ogre/internal/linkmodel"
	"github.com/tfrydrychewicz/ogre/internal/store"

	// The driver the store uses, registered as "sqlite".
	_ "modernc.org/sqlite"
)

// LocalOwner returns the ID of the built-in user of the database at path,
// creating the database (schema and user) the way the server does if it does
// not exist yet. Generated links are owned by this user.
func LocalOwner(ctx context.Context, path string) (int64, error) {
	st, err := store.Open(ctx, path)
	if err != nil {
		return 0, err
	}
	defer st.Close()
	u, err := st.LocalUser(ctx)
	if err != nil {
		return 0, err
	}
	return u.ID, nil
}

// Seed writes links into the database at path, which LocalOwner has created
// and which holds no links yet.
//
// It writes the rows that store.SQLite.CreateLink writes (the link, its
// routes, the variables that carry a description or an example, and the
// "create" revision), with the same values, but all in one transaction and
// without syncing to disk. CreateLink commits and waits for the drive once
// per link, by design (NFR-7): about 3 ms each on a laptop, five minutes for
// 100,000 links, which would make the run too slow to repeat. TestSeedMatchesStore
// proves that the two paths leave the same data behind, so a schema change
// that this function misses fails a test rather than skewing a measurement.
//
// The links then reach the server the normal way: it opens the file and
// loads every link at startup, which is part of what the run measures.
func Seed(ctx context.Context, path string, links []linkmodel.Link) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	q := url.Values{}
	q.Add("_pragma", "foreign_keys(ON)")
	q.Add("_pragma", "synchronous(OFF)")
	db, err := sql.Open("sqlite", (&url.URL{Scheme: "file", Path: filepath.ToSlash(abs)}).String()+"?"+q.Encode())
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var existing int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM links`).Scan(&existing); err != nil {
		return fmt.Errorf("perf: %s is not an Ogre database: %w", path, err)
	}
	if existing != 0 {
		return fmt.Errorf("perf: %s already holds %d links; seed an empty database", path, existing)
	}

	insertLink, err := tx.PrepareContext(ctx,
		`INSERT INTO links (id, name, description, owner_id, version, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	insertRoute, err := tx.PrepareContext(ctx,
		`INSERT INTO routes (id, link_id, position, pattern, shape, destination) VALUES (?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	insertVariable, err := tx.PrepareContext(ctx,
		`INSERT INTO variables (route_id, name, description, example) VALUES (?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	insertRevision, err := tx.PrepareContext(ctx,
		`INSERT INTO revisions (link_id, editor_id, at, action, snapshot) VALUES (?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}

	for _, l := range links {
		if l.Name != linkmodel.NormalizeName(l.Name) {
			return fmt.Errorf("perf: link %q: %w", l.Name, store.ErrNameNotNormalized)
		}
		at := l.CreatedAt.UTC().Format(time.RFC3339)
		if _, err := insertLink.ExecContext(ctx, l.ID, l.Name, l.Description, l.OwnerID, l.Version, at,
			l.UpdatedAt.UTC().Format(time.RFC3339)); err != nil {
			return fmt.Errorf("perf: link %q: %w", l.Name, err)
		}
		for i, r := range l.Routes {
			if _, err := insertRoute.ExecContext(ctx, r.ID, l.ID, r.Position, r.Pattern, linkmodel.Shape(r.Pattern), r.Destination); err != nil {
				return fmt.Errorf("perf: link %q, route %d: %w", l.Name, i+1, err)
			}
			for _, v := range r.Variables {
				if v.Description == "" && v.Example == "" {
					continue
				}
				if _, err := insertVariable.ExecContext(ctx, r.ID, v.Name, v.Description, v.Example); err != nil {
					return fmt.Errorf("perf: link %q, route %d, variable %q: %w", l.Name, i+1, v.Name, err)
				}
			}
		}
		snapshot, err := store.EncodeSnapshot(l)
		if err != nil {
			return err
		}
		if _, err := insertRevision.ExecContext(ctx, l.ID, l.OwnerID, at, store.RevisionCreate, snapshot); err != nil {
			return fmt.Errorf("perf: link %q, revision: %w", l.Name, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	// Leave the file as the server would find it after a clean shutdown:
	// everything in the main file, the write-ahead log empty.
	_, err = db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`)
	return err
}
