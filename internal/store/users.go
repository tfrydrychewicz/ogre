package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// ensureLocalUser inserts the built-in user of local mode unless it is there
// already. It runs on every Open so that a database always has an owner for
// new links, whichever Ogre version created it.
func (s *SQLite) ensureLocalUser(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO users (email, display_name, role, status, created_at)
		 VALUES (?, 'Local user', 'admin', 'active', ?)
		 ON CONFLICT (email) DO NOTHING`,
		LocalUserEmail, formatTime(s.timestamp()))
	if err != nil {
		return fmt.Errorf("create the local user: %w", err)
	}
	return nil
}

// LocalUser returns the built-in user of local mode.
func (s *SQLite) LocalUser(ctx context.Context) (User, error) {
	var u User
	err := s.db.QueryRowContext(ctx,
		`SELECT id, email, display_name, role, status FROM users WHERE email = ?`,
		LocalUserEmail).Scan(&u.ID, &u.Email, &u.DisplayName, &u.Role, &u.Status)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, fmt.Errorf("store: local user: %w", ErrNotFound)
	}
	if err != nil {
		return User{}, fmt.Errorf("store: local user: %w", err)
	}
	return u, nil
}
