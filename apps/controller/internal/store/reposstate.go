package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// ReposState is the repository state row (CRSP148); it implements
// repos.Persister.
type ReposState struct{ s *Store }

// ReposState returns the keeper of the repository state.
func (s *Store) ReposState() *ReposState { return &ReposState{s: s} }

// LoadReposState returns the saved state, or nothing when none was saved.
func (r *ReposState) LoadReposState(ctx context.Context) ([]byte, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	var body string
	err := r.s.db.QueryRowContext(ctx, `SELECT body FROM repos_state WHERE id = 1`).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read the repository state: %w", err)
	}
	return []byte(body), nil
}

// SaveReposState replaces the saved state.
func (r *ReposState) SaveReposState(ctx context.Context, body []byte) error {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	if _, err := r.s.db.ExecContext(ctx, `INSERT INTO repos_state (id, body) VALUES (1, ?)
ON CONFLICT (id) DO UPDATE SET body = excluded.body, saved_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')`, string(body)); err != nil {
		return fmt.Errorf("save the repository state: %w", err)
	}
	return nil
}
