package runners

import "context"

// Persister is the durable side of the runner records: the controller database
// (OPS59). Runner records are kept whether or not their machine is known yet.
type Persister interface {
	SaveRunner(ctx context.Context, r Record) error
	RemoveRunner(ctx context.Context, id string) error
	SavePlan(ctx context.Context, p Plan) error
	LoadPlan(ctx context.Context, id string) (Plan, bool, error)
	LoadRunners(ctx context.Context) ([]Record, error)
}

// DurableStore is the runner Store the controller runs on: MemStore logic with
// each change written to the database first-hand, and the records read back at
// start, so an update or restart keeps every runner (OPS59).
type DurableStore struct {
	*MemStore
	p Persister
}

var _ Store = (*DurableStore)(nil)

// OpenDurableStore loads the records from p.
func OpenDurableStore(ctx context.Context, p Persister) (*DurableStore, error) {
	rs, err := p.LoadRunners(ctx)
	if err != nil {
		return nil, err
	}
	m := NewMemStore()
	for _, r := range rs {
		m.runners[r.ID] = r
	}
	return &DurableStore{MemStore: m, p: p}, nil
}

func (d *DurableStore) save(ctx context.Context, id string) error {
	r, err := d.GetRunner(ctx, id)
	if err != nil {
		return err
	}
	return d.p.SaveRunner(ctx, r)
}

// InsertRunner implements Store.
func (d *DurableStore) InsertRunner(ctx context.Context, r Record) error {
	if err := d.MemStore.InsertRunner(ctx, r); err != nil {
		return err
	}
	return d.p.SaveRunner(ctx, r)
}

// SetRunnerState implements Store.
func (d *DurableStore) SetRunnerState(ctx context.Context, id string, to State, detail string) error {
	if err := d.MemStore.SetRunnerState(ctx, id, to, detail); err != nil {
		return err
	}
	return d.save(ctx, id)
}

// SetRunnerService implements Store.
func (d *DurableStore) SetRunnerService(ctx context.Context, id, svc, acct string) error {
	if err := d.MemStore.SetRunnerService(ctx, id, svc, acct); err != nil {
		return err
	}
	return d.save(ctx, id)
}

// SetRunnerRegistration implements Store.
func (d *DurableStore) SetRunnerRegistration(ctx context.Context, id string, ghID int64, version string) error {
	if err := d.MemStore.SetRunnerRegistration(ctx, id, ghID, version); err != nil {
		return err
	}
	return d.save(ctx, id)
}

// DeleteRunner implements Store.
func (d *DurableStore) DeleteRunner(ctx context.Context, id string) error {
	if err := d.MemStore.DeleteRunner(ctx, id); err != nil {
		return err
	}
	return d.p.RemoveRunner(ctx, id)
}

// SetRunnerPool implements Store.
func (d *DurableStore) SetRunnerPool(ctx context.Context, id, pool string) error {
	if err := d.MemStore.SetRunnerPool(ctx, id, pool); err != nil {
		return err
	}
	return d.save(ctx, id)
}

// SetRunnerHygiene implements Store.
func (d *DurableStore) SetRunnerHygiene(ctx context.Context, id string, h Hygiene) error {
	if err := d.MemStore.SetRunnerHygiene(ctx, id, h); err != nil {
		return err
	}
	return d.save(ctx, id)
}

// SavePlan implements Store.
func (d *DurableStore) SavePlan(ctx context.Context, p Plan) error {
	if err := d.MemStore.SavePlan(ctx, p); err != nil {
		return err
	}
	return d.p.SavePlan(ctx, p)
}

// GetPlan implements Store; a plan from before a restart is read from the database.
func (d *DurableStore) GetPlan(ctx context.Context, id string) (Plan, error) {
	if p, err := d.MemStore.GetPlan(ctx, id); err == nil {
		return p, nil
	}
	p, ok, err := d.p.LoadPlan(ctx, id)
	if err != nil {
		return Plan{}, err
	}
	if !ok {
		return Plan{}, ErrNotFound
	}
	return p, nil
}
