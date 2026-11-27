package demand

import (
	"context"
	"reflect"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/github"
)

// Runners R5.92: the count of an ephemeral pool request is the ceiling of
// ephemeral runners at once on the machine. The job slots still bound it; a
// job above the ceiling waits until a runner ends.
func TestCeilingBoundsEphemeralRunners(t *testing.T) {
	for _, c := range []struct {
		name        string
		slots, ceil int
		noCeiling   bool
		wantStarted int
		wantWaiting int
	}{
		{"ceiling below the slots", 4, 2, false, 2, 1},
		{"slots below the ceiling", 1, 5, false, 1, 2},
		{"no ephemeral runner set up", 4, 0, true, 0, 3},
	} {
		t.Run(c.name, func(t *testing.T) {
			l, fr, _ := newLoop(c.slots)
			fr.ceiling, fr.noCeiling = c.ceil, c.noCeiling
			ctx := context.Background()
			for i := int64(1); i <= 3; i++ {
				l.Observe(ctx, queued(i, linuxLabels...))
			}
			l.Dispatch(ctx)
			if len(fr.started) != c.wantStarted || l.Waiting() != c.wantWaiting {
				t.Fatalf("started %d waiting %d, want %d and %d", len(fr.started), l.Waiting(), c.wantStarted, c.wantWaiting)
			}
		})
	}
}

// A job that ends frees its place under the ceiling for the job that waits.
func TestCeilingFreedByAFinishedJob(t *testing.T) {
	l, fr, _ := newLoop(4)
	fr.ceiling = 1
	ctx := context.Background()
	l.Observe(ctx, queued(1, linuxLabels...))
	l.Observe(ctx, queued(2, linuxLabels...))
	l.Dispatch(ctx)
	if len(fr.started) != 1 || l.Waiting() != 1 {
		t.Fatalf("started %d waiting %d", len(fr.started), l.Waiting())
	}
	fr.running["m1"]--
	l.Observe(ctx, github.Job{ID: 1, Repository: "alice/demo", Status: "completed", RunnerName: "crsp-job-1-1"})
	l.Dispatch(ctx)
	if len(fr.started) != 2 || fr.started[1].Name != "crsp-job-2-1" {
		t.Fatalf("started %+v", fr.started)
	}
}

// A job that asks for the ephemeral label is matched; the runner carries it.
func TestMatchAcceptsTheEphemeralLabel(t *testing.T) {
	got, why := Match([]string{"self-hosted", "linux", "ephemeral"}, "x64", "")
	if why != "" || !reflect.DeepEqual(got, []string{"ephemeral", "linux", "self-hosted", "x64"}) {
		t.Fatalf("labels %v why %q", got, why)
	}
}
