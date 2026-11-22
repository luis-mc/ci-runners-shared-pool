package main

import (
	"context"
	"sort"

	"github.com/luis-mc/ci-runners-shared-pool/internal/api"
	"github.com/luis-mc/ci-runners-shared-pool/internal/audit"
	"github.com/luis-mc/ci-runners-shared-pool/internal/machines"
	"github.com/luis-mc/ci-runners-shared-pool/internal/update"
	"github.com/luis-mc/ci-runners-shared-pool/internal/update/lifecycle"
)

// lifecycleAudit writes every check, install, rollback and refusal of the updater to the audit
// log with its actor and result. A failed write is only logged by the caller: the update goes on.
func lifecycleAudit(log *audit.Log, onErr func(error)) func(context.Context, lifecycle.Row) {
	return func(ctx context.Context, r lifecycle.Row) {
		actor := audit.Actor{Login: r.Actor, Source: audit.SourceWeb}
		if r.Actor == "updater" || r.Actor == "" {
			actor = audit.System
		}
		details := map[string]any{"summary": r.Kind + " " + r.Result, "result": r.Result, "detail": r.Detail, "remote": r.Remote, "version": r.Version}
		target := audit.Target{Type: update.AuditTarget, ID: r.Version}
		var err error
		if r.Kind == "refused" {
			err = log.RecordRefused(ctx, actor, update.RowAction(r), target, details)
		} else {
			err = log.Record(ctx, actor, update.RowAction(r), target, details)
		}
		if err != nil && onErr != nil {
			onErr(err)
		}
	}
}

// lifecycleHistory reads the update rows back, newest first.
func lifecycleHistory(log *audit.Log) func(context.Context) ([]update.HistoryRow, error) {
	return func(ctx context.Context) ([]update.HistoryRow, error) {
		page, err := log.List(ctx, audit.Filter{TargetType: update.AuditTarget, Limit: audit.MaxPage})
		if err != nil {
			return nil, err
		}
		out := make([]update.HistoryRow, 0, len(page.Items))
		for _, e := range page.Items {
			if h, ok := update.HistoryFromAudit(e.At, e.Actor, e.Action, e.TargetID); ok {
				out = append(out, h)
			}
		}
		sort.SliceStable(out, func(i, j int) bool { return out[i].At.After(out[j].At) })
		return out, nil
	}
}

// fleetView lists this machine and the connected agents with their version and update state.
type fleetView struct {
	svc   *machines.Service
	state func(context.Context) string
}

var _ api.FleetService = fleetView{}

func (f fleetView) Fleet(ctx context.Context) ([]api.FleetMachine, error) {
	vs, err := f.svc.List(ctx)
	if err != nil {
		return nil, err
	}
	agents, _ := f.svc.Agents(ctx)
	updating := map[string]bool{}
	for _, a := range agents {
		updating[a.ID] = a.Update.Active()
	}
	out := make([]api.FleetMachine, 0, len(vs))
	for _, v := range vs {
		row := api.FleetMachine{ID: v.Machine.ID, Name: v.Machine.Name, Version: v.Machine.AgentVersion, State: "up_to_date", Local: v.Machine.IsController}
		switch {
		case v.Machine.IsController:
			row.Version, row.State = version, f.state(ctx)
		case updating[v.Machine.ID]:
			row.State = "installing"
		case update.Newer(row.Version, version):
			row.State = "available"
		}
		out = append(out, row)
	}
	return out, nil
}

func (f fleetView) AskUpdate(machineID, version string) error {
	return f.svc.AskUpdate(machineID, version)
}
