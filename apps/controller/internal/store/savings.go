package store

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// PoolJobRow is one finished job that ran on a pool machine, with what the savings report needs.
type PoolJobRow struct {
	Repository string
	Public     bool
	OS         string
	Machine    string
	Run        time.Duration
}

// finishedPoolJobsSQL reads one completed-at window of done jobs on the (state, completed_at) index (audit R3-1).
const finishedPoolJobsSQL = `SELECT j.repository, COALESCE(r.visibility, 'private') = 'public',
 COALESCE(m.os, ''), CASE WHEN j.machine_name != '' THEN j.machine_name ELSE COALESCE(m.name, j.machine_id) END,
 j.started_at, j.completed_at
 FROM jobs j
 LEFT JOIN repositories r ON r.id = j.repository_id
 LEFT JOIN machines m ON m.id = j.machine_id
 WHERE j.state = 'done' AND j.machine_id != '' AND j.started_at != '' AND j.completed_at != ''
 AND j.completed_at >= ? AND j.completed_at < ?`

// FinishedPoolJobs lists the jobs that completed in [from, to) on a machine of the pool: they have a machine
// and a real start and end. The system is the machine's, the repository is public when GitHub says so
// (observability R5.15, R5.18).
func (s *Store) FinishedPoolJobs(ctx context.Context, from, to time.Time) ([]PoolJobRow, error) {
	rows, err := s.db.QueryContext(ctx, finishedPoolJobsSQL, fmtTime(from), fmtTime(to))
	if err != nil {
		return nil, fmt.Errorf("finished pool jobs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []PoolJobRow
	for rows.Next() {
		var p PoolJobRow
		var started, completed string
		if err := rows.Scan(&p.Repository, &p.Public, &p.OS, &p.Machine, &started, &completed); err != nil {
			return nil, err
		}
		a, b := parseTime(started), parseTime(completed)
		if a.IsZero() || b.IsZero() || !b.After(a) {
			continue
		}
		p.Run = b.Sub(a)
		p.OS = strings.ToLower(p.OS)
		out = append(out, p)
	}
	return out, rows.Err()
}
