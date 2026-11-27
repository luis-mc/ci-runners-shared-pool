package runners

import (
	"context"
	"fmt"
)

// Update statuses a console can show (runners R5.38).
const (
	UpdateUpdated = "updated"
	UpdateFailed  = "failed"
	UpdateWaiting = "waiting for the job to end"
	UpdateCurrent = "current"
)

// UpdateRequest asks an agent to replace the runner files of one persistent
// runner. The agent downloads the pinned version, checks its SHA-256 (R5.37,
// one re-download on a wrong hash), stops the service, replaces the files,
// keeps the registration and the folder's state, and starts the service.
type UpdateRequest struct {
	RunnerID    string
	Name        string
	ServiceName string
	Version     string
	SHA256      string
}

// RunnerUpdater is the agent call the update needs. It is separate from Agent
// so a machine whose agent cannot update yet answers with a reason.
type RunnerUpdater interface {
	UpdateRunner(ctx context.Context, machineID string, req UpdateRequest) error
}

// UpdateResult is the truthful result of one runner's update.
type UpdateResult struct {
	RunnerID string
	Name     string
	Status   string
	From, To string
	Reason   string
}

// UpdateSettingOn reads the owner setting for automatic runner updates; it is
// the product's automatic-update setting (operations R5.15), default on.
func (s *Service) updateSettingOn(ctx context.Context) bool {
	if s.AutoUpdate == nil {
		return true
	}
	return s.AutoUpdate(ctx)
}

// UpdateRunner drains one persistent runner, updates it to the pin of its
// machine and starts it again. The idle check runs right before the swap
// (R6.4); a running job is never cut. A drain that runs out leaves the runner
// as it was and says it is waiting for the job to end.
func (s *Service) UpdateRunner(ctx context.Context, id, actor string) (UpdateResult, error) {
	r, err := s.Store.GetRunner(ctx, id)
	if err != nil {
		return UpdateResult{}, err
	}
	res := UpdateResult{RunnerID: r.ID, Name: r.Name, From: r.RunnerVersion}
	if !r.Managed || !r.KeepsState() {
		return res, refuse("runner %s is not a persistent runner made by CRSP; an ephemeral runner uses the new version from its next job", r.Name)
	}
	if s.Updater == nil {
		return res, refuse("the agent of this machine cannot update runners yet")
	}
	m, err := s.Machines.GetMachine(ctx, r.MachineID)
	if err != nil {
		return res, err
	}
	pin, err := PinFor(m.OS, m.Arch)
	if err != nil {
		return res, refuse("%v", err)
	}
	res.To = pin.Version
	if r.RunnerVersion == pin.Version {
		res.Status = UpdateCurrent
		return res, nil
	}
	if r.State != Idle && r.State != Busy {
		return res, refuse("runner %s is %s; it is updated when it is idle", r.Name, r.State)
	}
	prev := r.State
	if err := s.Store.SetRunnerState(ctx, id, Draining, "update to "+pin.Version); err != nil {
		return res, err
	}
	d := s.drainFor(r)
	err = d.Act(ctx, func(ctx context.Context) error {
		_ = s.Store.SetRunnerState(ctx, id, Updating, "idle; updating to "+pin.Version)
		return s.Updater.UpdateRunner(ctx, r.MachineID, UpdateRequest{
			RunnerID: id, Name: r.Name, ServiceName: r.ServiceName, Version: pin.Version, SHA256: pin.SHA256})
	})
	switch {
	case err == nil:
		_ = s.Store.SetRunnerRegistration(ctx, id, r.GitHubRunnerID, pin.Version)
		_ = s.Store.SetRunnerState(ctx, id, Idle, "updated to "+pin.Version)
		res.Status = UpdateUpdated
		s.audit(ctx, actor, "runner.update", r.MachineID, r.Name, r.RunnerVersion+" -> "+pin.Version)
	case IsDrainTimeout(err):
		_ = s.Store.SetRunnerState(ctx, id, restoreState(prev), err.Error())
		res.Status, res.Reason = UpdateWaiting, err.Error()
	default:
		// The agent may have stopped the service; the state says what is true.
		_ = s.Store.SetRunnerState(ctx, id, restoreState(prev), "update failed: "+err.Error())
		res.Status, res.Reason = UpdateFailed, err.Error()
		s.audit(ctx, actor, "runner.update.failed", r.MachineID, r.Name, err.Error())
	}
	return res, nil
}

// UpdateAll updates every persistent runner that runs an older version than the
// pin. With auto set (the check that runs by itself) nothing happens when the
// owner turned automatic updates off; an update the owner asks for runs always.
func (s *Service) UpdateAll(ctx context.Context, actor string, auto bool) ([]UpdateResult, error) {
	if auto && !s.updateSettingOn(ctx) {
		return nil, nil
	}
	managed := true
	list, err := s.Store.ListRunners(ctx, Filter{Managed: &managed})
	if err != nil {
		return nil, err
	}
	var out []UpdateResult
	for _, r := range list {
		if !r.KeepsState() || IsPoolRunner(r) { // a pool runner has no runner files; its registrations do
			continue
		}
		res, err := s.UpdateRunner(ctx, r.ID, actor)
		if err != nil {
			res.RunnerID, res.Name, res.Status, res.Reason = r.ID, r.Name, UpdateFailed, fmt.Sprint(err)
		}
		if res.Status == UpdateCurrent {
			continue
		}
		out = append(out, res)
	}
	return out, nil
}
