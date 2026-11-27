package machines

import (
	"context"
	"sort"
	"strings"
	"time"
)

// FarPastUsual is how many times its usual duration a job may run before the
// drain flags it (R5.56).
const FarPastUsual = 2

// TypicalFunc gives the usual duration of a job, from job history. ok is false
// when there is no history for it.
type TypicalFunc func(ctx context.Context, repository, job string) (usual time.Duration, ok bool)

// StuckNotifier is an optional part of a Notifier: it is told once when the job
// that holds a drain runs far past its usual duration, so the owner is not left
// to find out by waiting (R5.56).
type StuckNotifier interface {
	DrainStuck(machineID, kind string, h Holder, running time.Duration)
}

// SetTypical sets where the usual duration of a job comes from.
func (s *Service) SetTypical(f TypicalFunc) {
	s.mu.Lock()
	s.typical = f
	s.mu.Unlock()
}

// JobsOn keeps the jobs that run on one of the runners. No runner names means
// the whole machine: every job stays. A change for runner set X counts only the
// jobs on X, never a busy runner outside X, not even one of the same repository
// (R5.57). Names compare without case.
func JobsOn(jobs []JobRef, runners []string) []JobRef {
	if len(runners) == 0 {
		return jobs
	}
	in := map[string]bool{}
	for _, r := range runners {
		in[strings.ToLower(r)] = true
	}
	out := make([]JobRef, 0, len(jobs))
	for _, j := range jobs {
		if in[strings.ToLower(j.RunnerName)] {
			out = append(out, j)
		}
	}
	return out
}

// HolderOf names the job that has run longest and how it compares with its
// usual duration (R5.55). A job with no start time ranks last.
func (s *Service) HolderOf(ctx context.Context, jobs []JobRef) *Holder {
	if len(jobs) == 0 {
		return nil
	}
	js := append([]JobRef(nil), jobs...)
	sort.SliceStable(js, func(i, j int) bool {
		a, b := js[i].StartedAt, js[j].StartedAt
		if a.IsZero() != b.IsZero() {
			return !a.IsZero()
		}
		return a.Before(b)
	})
	h := &Holder{Job: js[0]}
	s.mu.Lock()
	typical := s.typical
	s.mu.Unlock()
	if typical != nil {
		if u, ok := typical(ctx, h.Job.Repository, h.Job.Job); ok && u > 0 {
			h.Usual = u
			if !h.Job.StartedAt.IsZero() && s.now().Sub(h.Job.StartedAt) >= FarPastUsual*u {
				h.OverUsual = true
			}
		}
	}
	return h
}

// refreshHolder updates the holder of a change from what it waits for, and
// raises the stuck alert once. It reports whether the change needs saving.
func (s *Service) refreshHolder(ctx context.Context, c *Change) bool {
	h := s.HolderOf(ctx, c.Detail.WaitingFor)
	changed := !sameHolder(c.Detail.Holder, h)
	c.Detail.Holder = h
	if h != nil && h.OverUsual && !c.Detail.StuckAlerted {
		c.Detail.StuckAlerted = true
		changed = true
		s.mu.Lock()
		if s.stuckSent == nil {
			s.stuckSent = map[string]bool{}
		}
		sent := s.stuckSent[c.ID]
		s.stuckSent[c.ID] = true
		s.mu.Unlock()
		if sent {
			return changed
		}
		_ = s.o.Repo.Audit(ctx, "system", "machine.drain.stuck", c.MachineID, c.Kind+" "+h.Job.Repository+" "+h.Job.Job)
		if n, ok := s.o.Notify.(StuckNotifier); ok {
			n.DrainStuck(c.MachineID, c.Kind, *h, s.now().Sub(h.Job.StartedAt))
		}
	}
	return changed
}

func sameHolder(a, b *Holder) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Job.GitHubJobID == b.Job.GitHubJobID && a.Job.RunID == b.Job.RunID && a.Job.Job == b.Job.Job &&
		a.Job.RunnerName == b.Job.RunnerName && a.Usual == b.Usual && a.OverUsual == b.OverUsual
}

// Choices are the named choices the user has for a change that waits (R5.56):
// wait longer (the drain timeout runs again from now) or cancel the change.
// They are offered when the drain timeout has passed or the holder runs far
// past its usual duration. A choice that stops a job is never among them
// (R5.33). A change that is not waiting has none.
func Choices(c Change) []string {
	if c.State != ChangeWaiting && c.State != ChangeNeedsYou {
		return nil
	}
	if c.State == ChangeNeedsYou || !c.Detail.OverdueSince.IsZero() || (c.Detail.Holder != nil && c.Detail.Holder.OverUsual) {
		return []string{string(WaitLonger), string(CancelIt)}
	}
	return nil
}
