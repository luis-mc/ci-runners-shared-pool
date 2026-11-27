package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/machines"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runners"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/store/sqlcgen"
)

// The machines, their settings, keys, join codes and changes, and the runner
// records and plans, kept as JSON documents (migration 00032, OPS59). Keys and
// codes are hashes only (machines R6.2).

// MachinePersister is the database side of machines.DurableRepo.
func (s *Store) MachinePersister() machines.Persister { return ctlMachines{q: s.q} }

// RunnerPersister is the database side of runners.DurableStore.
func (s *Store) RunnerPersister() runners.Persister { return ctlRunners{q: s.q} }

type ctlMachines struct{ q *sqlcgen.Queries }

var _ machines.Persister = ctlMachines{}

func doc(v any) (string, error) {
	b, err := json.Marshal(v)
	return string(b), err
}

func undoc[T any](s string) (T, error) {
	var v T
	err := json.Unmarshal([]byte(s), &v)
	return v, err
}

func (c ctlMachines) SaveMachine(ctx context.Context, m machines.Machine) error {
	d, err := doc(m)
	if err != nil {
		return err
	}
	return c.q.UpsertCtlMachine(ctx, sqlcgen.UpsertCtlMachineParams{ID: m.ID, Name: m.Name, Removed: b2i(!m.RemovedAt.IsZero()), Doc: d})
}

func (c ctlMachines) SaveSettings(ctx context.Context, id string, st machines.Settings) error {
	d, err := doc(st)
	if err != nil {
		return err
	}
	return c.q.UpsertCtlMachineSettings(ctx, sqlcgen.UpsertCtlMachineSettingsParams{MachineID: id, Doc: d})
}

func (c ctlMachines) SaveKey(ctx context.Context, hash, id string, revoked bool) error {
	return c.q.UpsertCtlMachineKey(ctx, sqlcgen.UpsertCtlMachineKeyParams{Hash: hash, MachineID: id, Revoked: b2i(revoked)})
}

func (c ctlMachines) SaveCode(ctx context.Context, jc machines.JoinCode) error {
	d, err := doc(jc)
	if err != nil {
		return err
	}
	return c.q.UpsertCtlJoinCode(ctx, sqlcgen.UpsertCtlJoinCodeParams{ID: jc.ID, CodeHash: jc.Hash, Doc: d})
}

func (c ctlMachines) SaveChange(ctx context.Context, ch machines.Change) error {
	d, err := doc(ch)
	if err != nil {
		return err
	}
	open := ch.State == machines.ChangeWaiting || ch.State == machines.ChangeRunning || ch.State == machines.ChangeNeedsYou
	return c.q.UpsertCtlMachineChange(ctx, sqlcgen.UpsertCtlMachineChangeParams{ID: ch.ID, MachineID: ch.MachineID, Open: b2i(open), Doc: d})
}

func (c ctlMachines) Load(ctx context.Context) (machines.Snapshot, error) {
	snap := machines.Snapshot{
		Machines: map[string]machines.Machine{}, Settings: map[string]machines.Settings{}, Keys: map[string]string{},
		Revoked: map[string]bool{}, Codes: map[string]machines.JoinCode{}, Changes: map[string]machines.Change{},
	}
	ms, err := c.q.ListCtlMachines(ctx)
	if err != nil {
		return snap, err
	}
	for _, r := range ms {
		m, err := undoc[machines.Machine](r.Doc)
		if err != nil {
			return snap, fmt.Errorf("machine %s: %w", r.ID, err)
		}
		snap.Machines[r.ID] = m
		d, err := c.q.GetCtlMachineSettings(ctx, r.ID)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return snap, err
		}
		if snap.Settings[r.ID], err = undoc[machines.Settings](d); err != nil {
			return snap, fmt.Errorf("settings of %s: %w", r.ID, err)
		}
	}
	keys, err := c.q.ListCtlKeys(ctx)
	if err != nil {
		return snap, err
	}
	for _, k := range keys {
		snap.Keys[k.Hash] = k.MachineID
		if k.Revoked != 0 {
			snap.Revoked[k.Hash] = true
		}
	}
	codes, err := c.q.ListCtlJoinCodes(ctx)
	if err != nil {
		return snap, err
	}
	for _, d := range codes {
		jc, err := undoc[machines.JoinCode](d)
		if err != nil {
			return snap, err
		}
		snap.Codes[jc.ID] = jc
	}
	chs, err := c.q.ListCtlMachineChanges(ctx)
	if err != nil {
		return snap, err
	}
	for _, d := range chs {
		ch, err := undoc[machines.Change](d)
		if err != nil {
			return snap, err
		}
		snap.Changes[ch.ID] = ch
	}
	return snap, nil
}

type ctlRunners struct{ q *sqlcgen.Queries }

var _ runners.Persister = ctlRunners{}

func (c ctlRunners) SaveRunner(ctx context.Context, r runners.Record) error {
	d, err := doc(r)
	if err != nil {
		return err
	}
	return c.q.UpsertCtlRunner(ctx, sqlcgen.UpsertCtlRunnerParams{ID: r.ID, MachineID: r.MachineID, Name: r.Name, Doc: d})
}

func (c ctlRunners) RemoveRunner(ctx context.Context, id string) error {
	return c.q.DeleteCtlRunner(ctx, id)
}

func (c ctlRunners) SavePlan(ctx context.Context, p runners.Plan) error {
	d, err := doc(p)
	if err != nil {
		return err
	}
	return c.q.UpsertCtlRunnerPlan(ctx, sqlcgen.UpsertCtlRunnerPlanParams{ID: p.ID, Doc: d})
}

func (c ctlRunners) LoadPlan(ctx context.Context, id string) (runners.Plan, bool, error) {
	d, err := c.q.GetCtlRunnerPlan(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return runners.Plan{}, false, nil
	}
	if err != nil {
		return runners.Plan{}, false, err
	}
	p, err := undoc[runners.Plan](d)
	return p, err == nil, err
}

func (c ctlRunners) LoadRunners(ctx context.Context) ([]runners.Record, error) {
	ds, err := c.q.ListCtlRunners(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]runners.Record, 0, len(ds))
	for _, d := range ds {
		r, err := undoc[runners.Record](d)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}
