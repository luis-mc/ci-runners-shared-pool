package store

import (
	"context"
	"reflect"
	"testing"
)

// caches R5.51 to R5.54: the queue of a machine is its slot tickets and the queued jobs
// placed on it; nothing from another machine and no finished job counts.
func TestIntegrationQueuedFor(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	exec := func(q string, a ...any) {
		t.Helper()
		if _, err := s.db.ExecContext(ctx, q, a...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(`INSERT INTO machines (id, name, os, arch) VALUES ('m1','win-1','windows','x64'), ('m2','box-2','linux','x64')`)
	exec(`INSERT INTO installations (id, account_login, account_type) VALUES (1,'acme','Organization')`)
	exec(`INSERT INTO repositories (id, installation_id, full_name, owner, name) VALUES (10,1,'acme/api','acme','api'), (11,1,'acme/web','acme','web')`)
	exec(`INSERT INTO gh_jobs (id, repository_id, run_id, run_attempt, job_name, status, placed_machine_id) VALUES
		(1,10,1,1,'a','queued','m1'), (2,11,1,1,'b','queued','m1'), (3,10,2,1,'c','completed','m1'), (4,10,3,1,'d','queued','m2'), (5,10,4,1,'e','queued',NULL)`)
	exec(`INSERT INTO slot_tickets (id, machine_id, repository, run_id, run_attempt, job, queued_at_ns, random_suffix, expires_at, holder_machine)
		VALUES ('t1','m1','acme/api',1,1,'a',1,1,'2099-01-01T00:00:00Z','m1'), ('t2','m2','acme/other',1,1,'z',1,1,'2099-01-01T00:00:00Z','m2')`)
	n, repos, err := s.QueuedFor(ctx, "m1")
	if err != nil || n != 3 || !reflect.DeepEqual(repos, []string{"acme/api", "acme/web"}) {
		t.Fatalf("m1: %d %v %v", n, repos, err)
	}
	n, repos, err = s.QueuedFor(ctx, "m2")
	if err != nil || n != 2 || !reflect.DeepEqual(repos, []string{"acme/api", "acme/other"}) {
		t.Fatalf("m2: %d %v %v", n, repos, err)
	}
	if n, _, err := s.QueuedFor(ctx, "m_none"); err != nil || n != 0 {
		t.Fatalf("none: %d %v", n, err)
	}
}
