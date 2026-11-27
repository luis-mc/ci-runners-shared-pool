package main

import (
	"context"
	"errors"
	"os/exec"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/audit"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/machines"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/update"
)

// errRemoteRemove is the answer for a machine other than this one: its own console removes it.
var errRemoteRemove = errors.New("Open crsp on that machine to remove it there.") //nolint:staticcheck,revive // shown to the person as it is

// removeService is POST /api/v1/remove (lifecycle LC-22 to LC-24). It writes the audit row of
// the request and starts `crsp uninstall` for this machine, which does the work of OPS69
// (internal/uninstall) and writes its own rows, to the audit file and the system log, that
// outlive the data. The command is detached: the remove stops this very service.
type removeService struct {
	log     *audit.Log
	svc     *machines.Service
	exe     func() (string, error)
	start   func(exe string, args []string) error
	onError func(error)
}

var _ api.RemoveService = (*removeService)(nil)

func newRemoveService(log *audit.Log, svc *machines.Service, onError func(error)) *removeService {
	return &removeService{log: log, svc: svc, exe: update.ExecutablePath, start: startDetached, onError: onError}
}

// isLocal says whether machineID is this machine: empty, or the controller's own ID.
func (r *removeService) isLocal(ctx context.Context, machineID string) bool {
	if machineID == "" {
		return true
	}
	if r.svc == nil {
		return false
	}
	vs, err := r.svc.List(ctx)
	if err != nil {
		return false
	}
	for _, v := range vs {
		if v.Machine.ID == machineID {
			return v.Machine.IsController
		}
	}
	return false
}

// Remove starts the remove of this machine. deleteData true is Delete my data too.
func (r *removeService) Remove(ctx context.Context, actor audit.Actor, machineID string, deleteData bool) error {
	if !r.isLocal(ctx, machineID) {
		return errRemoteRemove
	}
	exe, err := r.exe()
	if err != nil {
		return err
	}
	choice, flag := "keep", "--keep-data"
	if deleteData {
		choice, flag = "delete", "--delete-data"
	}
	if r.log != nil {
		details := map[string]any{"summary": "remove started, " + choice + " data", "result": "started", "delete_data": deleteData}
		if err := r.log.Record(ctx, actor, update.AuditAction("remove", "started"), audit.Target{Type: update.AuditTarget, ID: version}, details); err != nil && r.onError != nil {
			r.onError(err)
		}
	}
	who := actor.Login
	if who == "" {
		who = "console"
	}
	if err := r.start(exe, []string{"uninstall", flag, "--yes", "--actor", who}); err != nil {
		if r.log != nil {
			details := map[string]any{"summary": "remove could not start", "result": "failed", "delete_data": deleteData}
			_ = r.log.Record(ctx, actor, update.AuditAction("remove", "failed"), audit.Target{Type: update.AuditTarget, ID: version}, details)
		}
		return err
	}
	return nil
}

func detach(cmd *exec.Cmd) error {
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
