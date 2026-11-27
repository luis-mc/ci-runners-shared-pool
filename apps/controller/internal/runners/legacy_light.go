package runners

import (
	"context"
	"fmt"
	"strings"
)

// Light runners were made by older versions: one per repository and OS, with
// the label crsp-light (decision D110, 19 Nov 2026: they do not fit the pool
// design, so they are gone). New runners never get the label. These helpers
// find what an older version left and remove it on upgrade.
const (
	// LegacyLightLabel is the label the removed light runners carried.
	LegacyLightLabel = "crsp-light"
	// LegacyLightNamePrefix started the name of every light runner.
	LegacyLightNamePrefix = "crsp-light-"
	// legacyQueueLight is the stored route queue of a light runner.
	legacyQueueLight Queue = "light"
	// LegacyLightMaxTries is how many times the reconcile tries to remove one
	// light runner before it waits for a person.
	LegacyLightMaxTries = 5
)

// IsLegacyLight reports whether a record is a light runner left by an older
// version, by its stored route, its label or its name.
func IsLegacyLight(r Record) bool {
	if !r.Managed {
		return false
	}
	if r.Route.Queue == legacyQueueLight || strings.HasPrefix(r.Name, LegacyLightNamePrefix) {
		return true
	}
	for _, l := range r.Labels {
		if strings.EqualFold(l, LegacyLightLabel) {
			return true
		}
	}
	return false
}

// LegacyLightResult is what one pass of RemoveLegacyLight did.
type LegacyLightResult struct {
	// Started are the runners whose removal began (drain, registration,
	// service, account, folder, record).
	Started []string
	// Waiting are the runners that have a job; they are removed after it.
	Waiting []string
	// GaveUp are the runners removed too often without success; each needs a person.
	GaveUp []string
}

func (s *Service) info(format string, args ...any) {
	if s.LogInfo != nil {
		s.LogInfo(fmt.Sprintf(format, args...))
	}
}

// RemoveLegacyLight drains and removes every light runner an older version
// made. It checks that the runner is idle first and never stops a running job:
// a busy runner is left for the next pass and removed after its job. A failed
// removal puts the runner back with the reason on its record (the doctor shows
// it) and is tried again by the next pass, at most LegacyLightMaxTries times.
// It does nothing when no light runner exists, and is safe to call again.
func (s *Service) RemoveLegacyLight(ctx context.Context, actor string) (LegacyLightResult, error) {
	s.lightMu.Lock()
	defer s.lightMu.Unlock()
	var res LegacyLightResult
	all, err := s.Store.ListRunners(ctx, Filter{})
	if err != nil {
		return res, err
	}
	for _, r := range all {
		if !IsLegacyLight(r) || r.State == Draining || r.State == Removing {
			continue
		}
		if s.triesOf(r.ID) >= LegacyLightMaxTries {
			res.GaveUp = append(res.GaveUp, r.Name)
			continue
		}
		if r.State != Failed {
			idle, job, err := s.idle(ctx, r)
			if err != nil {
				n := s.failedTry(r.ID)
				s.info("light runner %s on machine %s: the runner list cannot be read, so it is not removed yet (try %d of %d): %v",
					r.Name, r.MachineID, n, LegacyLightMaxTries, err)
				_ = s.Store.SetRunnerState(ctx, r.ID, r.State, "light runner removal: "+err.Error())
				continue
			}
			if !idle {
				res.Waiting = append(res.Waiting, r.Name)
				s.info("light runner %s on machine %s has job %s; it is removed after the job", r.Name, r.MachineID, job)
				continue
			}
		}
		_, finish, err := s.BeginRemove(ctx, r.ID, true, actor)
		if err != nil {
			n := s.failedTry(r.ID)
			s.info("light runner %s on machine %s: removal did not start (try %d of %d): %v", r.Name, r.MachineID, n, LegacyLightMaxTries, err)
			continue
		}
		res.Started = append(res.Started, r.Name)
		s.info("light runner %s on machine %s: removing (registration, service, account, folder, record)", r.Name, r.MachineID)
		rec := r
		s.background(func() {
			if err := finish(context.WithoutCancel(ctx)); err != nil {
				n := s.failedTry(rec.ID)
				s.info("light runner %s on machine %s: removal failed (try %d of %d): %v", rec.Name, rec.MachineID, n, LegacyLightMaxTries, err)
				return
			}
			s.info("light runner %s on machine %s: removed", rec.Name, rec.MachineID)
		})
	}
	return res, nil
}

func (s *Service) triesOf(id string) int {
	s.legacyMu.Lock()
	defer s.legacyMu.Unlock()
	return s.legacyTries[id]
}

// failedTry counts one more failed try for a runner and returns the count.
func (s *Service) failedTry(id string) int {
	s.legacyMu.Lock()
	defer s.legacyMu.Unlock()
	if s.legacyTries == nil {
		s.legacyTries = map[string]int{}
	}
	s.legacyTries[id]++
	return s.legacyTries[id]
}
