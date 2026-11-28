package store

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// PoolJobGroup is a count of finished pool jobs that share a repository, system, machine and whole-minute
// run time. The database groups them, so a report over thousands of jobs reads a few rows.
type PoolJobGroup struct {
	Repository string
	Public     bool
	OS         string
	Machine    string
	// Minutes is each job's run time rounded up to the next whole minute (observability R5.15).
	Minutes int
	Jobs    int
}

// PoolJobGroups counts the jobs that completed in [from, to) on a machine of the pool: they have a machine and
// a real start and end. The system is the machine's, the repository is public when GitHub says so (R5.15,
// R5.18). The grouping and the round-up happen in SQL; the index `jobs_done_at` serves the window.
func (s *Store) PoolJobGroups(ctx context.Context, from, to time.Time) ([]PoolJobGroup, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT repository, public, os, machine, mins, COUNT(*) FROM (
 SELECT j.repository AS repository, COALESCE(r.visibility, 'private') = 'public' AS public,
  LOWER(COALESCE(m.os, '')) AS os,
  CASE WHEN j.machine_name != '' THEN j.machine_name ELSE COALESCE(m.name, j.machine_id) END AS machine,
  (CAST(ROUND((julianday(j.completed_at) - julianday(j.started_at)) * 86400000) AS INTEGER) + 59999) / 60000 AS mins
 FROM jobs j
 LEFT JOIN repositories r ON r.id = j.repository_id
 LEFT JOIN machines m ON m.id = j.machine_id
 WHERE j.state = 'done' AND j.completed_at >= ? AND j.completed_at < ?
 AND j.machine_id != '' AND j.started_at != '' AND j.completed_at != '')
 WHERE mins > 0
 GROUP BY repository, public, os, machine, mins`, fmtTime(from), fmtTime(to))
	if err != nil {
		return nil, fmt.Errorf("pool job groups: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []PoolJobGroup
	for rows.Next() {
		var g PoolJobGroup
		if err := rows.Scan(&g.Repository, &g.Public, &g.OS, &g.Machine, &g.Minutes, &g.Jobs); err != nil {
			return nil, err
		}
		g.OS = strings.ToLower(g.OS)
		out = append(out, g)
	}
	return out, rows.Err()
}
