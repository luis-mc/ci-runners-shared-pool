package store

import (
	"context"
	"fmt"
	"sort"
)

// QueuedFor counts the jobs that wait for a machine's runners and names
// the repositories they belong to: the slot tickets of the machine and the
// queued jobs placed on it (caches R5.51 to R5.54). A job not yet placed waits for any
// machine, so it is not counted here.
func (s *Store) QueuedFor(ctx context.Context, machineID string) (int, []string, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	rows, err := s.db.QueryContext(ctx, `
SELECT repository FROM slot_tickets WHERE machine_id = ?
UNION ALL
SELECT r.full_name FROM gh_jobs j JOIN repositories r ON r.id = j.repository_id
WHERE j.status = 'queued' AND j.placed_machine_id = ?`, machineID, machineID)
	if err != nil {
		return 0, nil, fmt.Errorf("read the queue of machine %s: %w", machineID, err)
	}
	defer func() { _ = rows.Close() }()
	n := 0
	seen := map[string]bool{}
	var repos []string
	for rows.Next() {
		var repo string
		if err := rows.Scan(&repo); err != nil {
			return 0, nil, fmt.Errorf("read the queue of machine %s: %w", machineID, err)
		}
		n++
		if !seen[repo] {
			seen[repo] = true
			repos = append(repos, repo)
		}
	}
	if err := rows.Err(); err != nil {
		return 0, nil, fmt.Errorf("read the queue of machine %s: %w", machineID, err)
	}
	sort.Strings(repos)
	return n, repos, nil
}
