package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/routing"
	"github.com/luis-mc/ci-runners-shared-pool/internal/store/sqlcgen"
)

// Routing is the database side of routing.Store (migration 00036, OPS73).
func (s *Store) Routing() *RoutingStore { return &RoutingStore{s} }

// RoutingStore keeps the per-job variables, labels and runner labels.
type RoutingStore struct{ s *Store }

var _ routing.Store = (*RoutingStore)(nil)

// each runs a query and calls fn for every row; the rows are closed here.
func (r *RoutingStore) each(ctx context.Context, q string, arg string, fn func(rows *sql.Rows) error) error {
	rows, err := r.s.db.QueryContext(ctx, q, arg)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		if err := fn(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}

// Load implements routing.Store.
func (r *RoutingStore) Load(ctx context.Context, repository string) (routing.State, error) {
	var st routing.State
	if err := r.each(ctx, `SELECT job, var_name, label, class, os, value FROM routing_jobs WHERE repository = ? ORDER BY job`, repository, func(rows *sql.Rows) error {
		var j routing.JobState
		if err := rows.Scan(&j.Key, &j.Var, &j.Label, &j.Class, &j.OS, &j.Value); err != nil {
			return err
		}
		st.Jobs = append(st.Jobs, j)
		return nil
	}); err != nil {
		return st, err
	}
	if err := r.each(ctx, `SELECT label, state, since, jobs FROM routing_labels WHERE repository = ? ORDER BY label`, repository, func(rows *sql.Rows) error {
		var l routing.LabelState
		var since, jobs string
		if err := rows.Scan(&l.Label, &l.State, &since, &jobs); err != nil {
			return err
		}
		l.Since, _ = time.Parse(time.RFC3339Nano, since)
		_ = json.Unmarshal([]byte(jobs), &l.Jobs)
		st.Labels = append(st.Labels, l)
		return nil
	}); err != nil {
		return st, err
	}
	if err := r.each(ctx, `SELECT runner_id, github_id, label, scope_owner, scope_repo, scope_org FROM routing_runner_labels
		WHERE repository = ? ORDER BY runner_id, label`, repository, func(rows *sql.Rows) error {
		l := routing.RunnerLabel{Repository: repository}
		if err := rows.Scan(&l.RunnerID, &l.GitHubID, &l.Label, &l.ScopeOwner, &l.ScopeRepo, &l.ScopeOrg); err != nil {
			return err
		}
		st.Runners = append(st.Runners, l)
		return nil
	}); err != nil {
		return st, err
	}
	var pr routing.PRState
	var at string
	switch err := r.s.db.QueryRowContext(ctx, `SELECT number, url, opened_at FROM routing_prs WHERE repository = ?`, repository).Scan(&pr.Number, &pr.URL, &at); err {
	case nil:
		pr.OpenedAt, _ = time.Parse(time.RFC3339Nano, at)
		st.PR = &pr
	case sql.ErrNoRows:
	default:
		return st, err
	}
	return st, nil
}

// Save implements routing.Store: the repository's rows are replaced in one transaction.
func (r *RoutingStore) Save(ctx context.Context, repository string, st routing.State) error {
	return r.s.inTx(ctx, func(ctx context.Context, tx *sql.Tx, _ *sqlcgen.Queries) error {
		for _, t := range []string{"routing_jobs", "routing_labels", "routing_runner_labels", "routing_prs"} {
			if _, err := tx.ExecContext(ctx, `DELETE FROM `+t+` WHERE repository = ?`, repository); err != nil {
				return err
			}
		}
		for _, j := range st.Jobs {
			if _, err := tx.ExecContext(ctx, `INSERT INTO routing_jobs (repository, job, var_name, label, class, os, value) VALUES (?,?,?,?,?,?,?)`,
				repository, j.Key, j.Var, j.Label, j.Class, j.OS, j.Value); err != nil {
				return err
			}
		}
		for _, l := range st.Labels {
			jobs, _ := json.Marshal(l.Jobs)
			if _, err := tx.ExecContext(ctx, `INSERT INTO routing_labels (repository, label, state, since, jobs) VALUES (?,?,?,?,?)`,
				repository, l.Label, l.State, l.Since.UTC().Format(time.RFC3339Nano), string(jobs)); err != nil {
				return err
			}
		}
		for _, l := range st.Runners {
			if _, err := tx.ExecContext(ctx, `INSERT INTO routing_runner_labels (repository, runner_id, github_id, label, scope_owner, scope_repo, scope_org)
				VALUES (?,?,?,?,?,?,?)`, repository, l.RunnerID, l.GitHubID, l.Label, l.ScopeOwner, l.ScopeRepo, l.ScopeOrg); err != nil {
				return err
			}
		}
		if st.PR != nil {
			if _, err := tx.ExecContext(ctx, `INSERT INTO routing_prs (repository, number, url, opened_at) VALUES (?,?,?,?)`,
				repository, st.PR.Number, st.PR.URL, st.PR.OpenedAt.UTC().Format(time.RFC3339Nano)); err != nil {
				return err
			}
		}
		return nil
	})
}
