package store

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/jobsdata"
)

// R6.12. Times are stored in UTC: a time given in another zone is written as the same instant in UTC (a
// trailing Z), never with a local offset. The console shows local time (apps/web/src/live/format.test.ts).
func TestJobTimesStoredUTC(t *testing.T) {
	s := jobsDB(t)
	ctx := context.Background()
	zone := time.FixedZone("UTC+2", 2*60*60)
	created := time.Date(2026, 11, 10, 10, 0, 0, 0, zone) // 08:00 UTC
	j := mkJob(7, "build", jobsdata.Done, jobsdata.Success, created, 5, 60)
	j.CreatedAt = created
	j.StartedAt = created.Add(5 * time.Second)
	j.CompletedAt = j.StartedAt.Add(time.Minute)
	must(t, s.UpsertJob(ctx, j))

	var createdAt, startedAt, completedAt string
	must(t, s.db.QueryRowContext(ctx, `SELECT created_at, started_at, completed_at FROM jobs WHERE id = 7`).
		Scan(&createdAt, &startedAt, &completedAt))
	for name, got := range map[string]string{"created_at": createdAt, "started_at": startedAt, "completed_at": completedAt} {
		if !strings.HasSuffix(got, "Z") || strings.Contains(got, "+02") {
			t.Errorf("%s stored as %q, want UTC with a trailing Z", name, got)
		}
	}
	if !strings.HasPrefix(createdAt, "2026-11-10T08:00:00") {
		t.Errorf("created_at %q, want the UTC instant 2026-11-10T08:00:00", createdAt)
	}
	d, ok, err := s.GetJob(ctx, 7)
	must(t, err)
	if !ok || !d.CreatedAt.Equal(created) || d.CreatedAt.Location() != time.UTC {
		t.Errorf("read back %v (%v), want the same instant in UTC", d.CreatedAt, d.CreatedAt.Location())
	}
}
