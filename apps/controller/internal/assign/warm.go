package assign

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runners"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/scheduler"
)

// The warm runner (runners R5.113, scheduling R5.66): one ready runner per busy
// label set, busy meaning at least one job of the set in the last hour. It
// counts against the machine's slots and budget, is used by the next job of its
// set and replaced after, and is released first when a real job needs the room.

// ensureWarm keeps one ready runner for each busy set and drops the ones that no
// longer earn their room. A warm runner is made only when no job waits for room
// anywhere, so it can never keep a real job waiting; the room check then runs
// with the warm runners already held, never releasing another to make one.
func (c *Controller) ensureWarm(ctx context.Context, vs []*machineView, waiting []Lease, now time.Time) error {
	sets, err := c.Store.ListSets(ctx)
	if err != nil {
		return err
	}
	warm, err := c.Store.ListWarm(ctx)
	if err != nil {
		return err
	}
	busy := map[string]Set{}
	for _, s := range sets {
		if now.Sub(s.LastBusy) <= BusyWindow {
			busy[s.Key] = s
		}
	}
	byKey := map[string]Warm{}
	for _, w := range warm {
		// A warm runner of a set that is no longer busy, or one that is stale, goes.
		if _, ok := busy[w.LabelKey]; !ok || now.Sub(w.StartedAt) > WarmMaxAge {
			if err := c.releaseWarm(ctx, w.ID); err != nil && err != runners.ErrRunnerBusy {
				return err
			}
			continue
		}
		byKey[w.LabelKey] = w
	}
	for _, l := range waiting {
		if l.State == Waiting {
			return nil // a real job waits: no new warm runner this round
		}
	}
	keys := make([]string, 0, len(busy))
	for k := range busy {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if _, have := byKey[k]; have {
			continue
		}
		s := busy[k]
		repo, ok, err := c.Env.Repo(ctx, s.Repository)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		for _, v := range vs {
			if !v.m.Serves(s.OS, s.Labels) {
				continue
			}
			probe := Lease{JobID: 0, Repository: s.Repository, Labels: s.Labels, OS: s.OS, Req: s.Req}
			d := scheduler.CheckRoom(v.roomInput(probe, now, nil, nil))
			if !d.OK || len(d.Release) > 0 {
				continue
			}
			name := c.newName("crsp-warm-", int64(len(keys)))
			rec, err := c.Runners.StartJobRunner(ctx, runners.JobRunner{Name: name, MachineID: v.m.ID, Arch: v.m.Arch, OS: v.m.OS,
				Labels: s.Labels, Scope: repo.Scope, ExtraPackages: repo.ExtraPackages}, Actor)
			if err != nil {
				continue // the next round tries again; a warm runner is never urgent
			}
			w := Warm{ID: name, MachineID: v.m.ID, LabelKey: k, Repository: s.Repository, OS: s.OS, Labels: s.Labels,
				RunnerName: name, RunnerRecord: rec.ID, GitHubRunner: rec.GitHubRunnerID, Req: s.Req, StartedAt: now}
			if err := c.Store.PutWarm(ctx, w); err != nil {
				return fmt.Errorf("keep the warm runner: %w", err)
			}
			v.held = append(v.held, scheduler.RoomHeld{Req: s.Req, Warm: true, ID: w.ID})
			break
		}
	}
	return nil
}
