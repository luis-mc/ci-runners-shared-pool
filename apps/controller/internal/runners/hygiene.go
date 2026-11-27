package runners

import (
	"context"
	"fmt"
	"strings"
)

// Persistent runner hygiene (runners R5.90, R5.91). The pool always removes a
// job's temporary files; the agent reports how that went (ReportClean). A
// failed clean-up leaves the runner unclean and it takes no new job until a
// later clean-up succeeds. The dependency caches stay only while the runner's
// setting KeepDependencies is on.

// hygieneActor is the audit actor of a report from a machine's agent.
const hygieneActor = "agent"

func hygieneKey(machineID, name string) string { return machineID + "|" + name }

// findByName finds the record of a runner by its machine and name. The name is
// the key only, never parsed.
func (s *Service) findByName(ctx context.Context, machineID, name string) (Record, error) {
	all, err := s.Store.ListRunners(ctx, Filter{MachineID: machineID})
	if err != nil {
		return Record{}, err
	}
	for _, r := range all {
		if r.MachineID == machineID && r.Name == name {
			return r, nil
		}
	}
	return Record{}, ErrNotFound
}

// ReportClean is what a machine's agent says after a clean-up of a persistent
// runner: the size of the runner's own dependency cache and the reason the
// clean-up failed ("" when it succeeded, which makes the runner clean again).
// It also ends a pending Clear now. A runner that turns unclean or clean again
// is written to the audit trail. ErrNotFound for a runner that is not a
// persistent runner of the machine.
func (s *Service) ReportClean(ctx context.Context, machineID, runnerName string, cacheBytes int64, uncleanReason string) error {
	r, err := s.findByName(ctx, machineID, runnerName)
	if err != nil {
		return err
	}
	if !r.KeepsState() {
		return ErrNotFound
	}
	uncleanReason = strings.TrimSpace(uncleanReason)
	if cacheBytes < 0 {
		cacheBytes = 0
	}
	h := Hygiene{KeepDependencies: r.KeepDependencies, DependencyCacheBytes: cacheBytes, UncleanReason: uncleanReason}
	if err := s.Store.SetRunnerHygiene(ctx, r.ID, h); err != nil {
		return err
	}
	s.setClearPending(machineID, runnerName, false)
	switch {
	case r.UncleanReason == "" && uncleanReason != "":
		s.audit(ctx, hygieneActor, "runner.unclean", machineID, r.ID, uncleanReason)
	case r.UncleanReason != "" && uncleanReason == "":
		s.audit(ctx, hygieneActor, "runner.clean", machineID, r.ID, "the clean-up succeeded")
	}
	return s.foldIntoPool(ctx, r)
}

// foldIntoPool keeps the pool runner that owns a hidden registration in step
// with its registrations: the sum of their cache sizes and the first reason
// one is unclean. A record that is not a registration changes nothing.
func (s *Service) foldIntoPool(ctx context.Context, reg Record) error {
	if reg.PoolRunnerID == "" {
		return nil
	}
	parent, err := s.Store.GetRunner(ctx, reg.PoolRunnerID)
	if err != nil {
		return nil // the pool runner is gone; nothing to fold into
	}
	all, err := s.Store.ListRunners(ctx, Filter{MachineID: reg.MachineID})
	if err != nil {
		return err
	}
	h := Hygiene{KeepDependencies: parent.KeepDependencies}
	for _, k := range all {
		if k.PoolRunnerID != parent.ID {
			continue
		}
		h.DependencyCacheBytes += k.DependencyCacheBytes
		if h.UncleanReason == "" {
			h.UncleanReason = k.UncleanReason
		}
	}
	return s.Store.SetRunnerHygiene(ctx, parent.ID, h)
}

// Unclean returns the reason the persistent runner of this machine and name is
// unclean, or "" when it is clean or unknown. The job gate reads it: an unclean
// runner takes no new job (runners R5.91).
func (s *Service) Unclean(ctx context.Context, machineID, runnerName string) string {
	r, err := s.findByName(ctx, machineID, runnerName)
	if err != nil {
		return ""
	}
	return r.UncleanReason
}

// KeepDependencies says whether the persistent runner keeps its downloaded
// dependencies between jobs. An unknown runner keeps them (the default).
func (s *Service) KeepDependencies(ctx context.Context, machineID, runnerName string) bool {
	r, err := s.findByName(ctx, machineID, runnerName)
	if err != nil || !r.KeepsState() {
		return true
	}
	return r.KeepDependencies
}

// members is the records that run for a persistent runner: its hidden
// registrations when it is a pool runner, and itself.
func (s *Service) members(ctx context.Context, r Record) ([]Record, error) {
	out := []Record{r}
	if !IsPoolRunner(r) {
		return out, nil
	}
	all, err := s.Store.ListRunners(ctx, Filter{MachineID: r.MachineID})
	if err != nil {
		return nil, err
	}
	for _, k := range all {
		if k.PoolRunnerID == r.ID {
			out = append(out, k)
		}
	}
	return out, nil
}

// RequestClearCache asks for the dependency cache of a persistent runner to be
// cleared. An idle runner clears it as soon as its agent reads the flag; a
// runner with a job clears it after the job (the agent only reads the flag
// between jobs). The flag lasts until ReportClean (runners R5.90).
func (s *Service) RequestClearCache(ctx context.Context, runnerID, actor string) (View, error) {
	r, err := s.find(ctx, runnerID)
	if err != nil {
		return View{}, err
	}
	if !r.KeepsState() {
		return View{}, refuse("runner %s is ephemeral: a fresh container for each job keeps no dependency cache", r.Name)
	}
	ms, err := s.members(ctx, r)
	if err != nil {
		return View{}, err
	}
	for _, m := range ms {
		s.setClearPending(m.MachineID, m.Name, true)
	}
	s.audit(ctx, actor, "runner.clear_cache", r.MachineID, r.ID, r.Name)
	return s.GetView(ctx, r.ID)
}

// ClearCachePending is true while a Clear now is asked and the agent has not
// reported the clean-up yet. The agent reads it between jobs.
func (s *Service) ClearCachePending(machineID, runnerName string) bool {
	s.hygMu.Lock()
	defer s.hygMu.Unlock()
	return s.clearPending[hygieneKey(machineID, runnerName)]
}

func (s *Service) setClearPending(machineID, name string, on bool) {
	s.hygMu.Lock()
	defer s.hygMu.Unlock()
	if s.clearPending == nil {
		s.clearPending = map[string]bool{}
	}
	if on {
		s.clearPending[hygieneKey(machineID, name)] = true
		return
	}
	delete(s.clearPending, hygieneKey(machineID, name))
}

// UpdateSettings changes the dependency setting of a persistent runner
// (runners R5.90). Turning it off makes the next clean-up drop the dependency
// caches too. An ephemeral runner has no such setting and is refused.
func (s *Service) UpdateSettings(ctx context.Context, runnerID string, keepDependencies *bool, actor string) (View, error) {
	r, err := s.find(ctx, runnerID)
	if err != nil {
		return View{}, err
	}
	if !r.KeepsState() {
		return View{}, refuse("runner %s is ephemeral: it is a fresh container for each job and keeps nothing, so it has no settings to change", r.Name)
	}
	if keepDependencies != nil && *keepDependencies != r.KeepDependencies {
		ms, err := s.members(ctx, r)
		if err != nil {
			return View{}, err
		}
		for _, m := range ms {
			h := Hygiene{KeepDependencies: *keepDependencies, DependencyCacheBytes: m.DependencyCacheBytes, UncleanReason: m.UncleanReason}
			if err := s.Store.SetRunnerHygiene(ctx, m.ID, h); err != nil {
				return View{}, err
			}
		}
		s.audit(ctx, actor, "runner.keep_dependencies", r.MachineID, r.ID, fmt.Sprintf("%s: keep downloaded dependencies %t", r.Name, *keepDependencies))
	}
	return s.GetView(ctx, r.ID)
}
