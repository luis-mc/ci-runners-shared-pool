package machines

import (
	"context"
	"time"
)

// Persister is the durable side of the pool: the controller database (OPS59).
// Every call writes one record and returns once it is stored.
type Persister interface {
	SaveMachine(ctx context.Context, m Machine) error
	SaveSettings(ctx context.Context, machineID string, s Settings) error
	SaveKey(ctx context.Context, hash, machineID string, revoked bool) error
	SaveCode(ctx context.Context, c JoinCode) error
	SaveChange(ctx context.Context, c Change) error
	// Load reads the whole pool.
	Load(ctx context.Context) (Snapshot, error)
}

// DurableRepo is the Repository the controller runs on: the in-memory logic of
// MemRepo, with every change written to the database before the call returns,
// and the pool read back from the database at start. An update or a restart
// of the controller therefore keeps every machine, setting, key, code and
// change (OPS59). A call whose write fails returns the error.
type DurableRepo struct {
	*MemRepo
	p Persister
}

var _ Repository = (*DurableRepo)(nil)

// OpenDurableRepo loads the pool from p. A pool that is empty in the database
// is a fresh controller (or one waiting for its machines.json import).
func OpenDurableRepo(ctx context.Context, p Persister) (*DurableRepo, error) {
	s, err := p.Load(ctx)
	if err != nil {
		return nil, err
	}
	r := NewMemRepo()
	r.Restore(s)
	return &DurableRepo{MemRepo: r, p: p}, nil
}

// Import writes a snapshot (a machines.json from an older controller) to the
// database and into memory. It does nothing to a pool that has machines.
func (d *DurableRepo) Import(ctx context.Context, s Snapshot) (bool, error) {
	if len(d.Snapshot().Machines) > 0 || len(s.Machines) == 0 {
		return false, nil
	}
	for id, m := range s.Machines {
		if err := d.p.SaveMachine(ctx, m); err != nil {
			return false, err
		}
		if set, ok := s.Settings[id]; ok {
			if err := d.p.SaveSettings(ctx, id, set); err != nil {
				return false, err
			}
		}
	}
	for h, id := range s.Keys {
		if err := d.p.SaveKey(ctx, h, id, s.Revoked[h]); err != nil {
			return false, err
		}
	}
	for _, c := range s.Codes {
		if err := d.p.SaveCode(ctx, c); err != nil {
			return false, err
		}
	}
	for _, c := range s.Changes {
		if err := d.p.SaveChange(ctx, c); err != nil {
			return false, err
		}
	}
	d.Restore(s)
	return true, nil
}

func (d *DurableRepo) saveMachine(ctx context.Context, id string) error {
	m, err := d.GetMachine(ctx, id)
	if err != nil {
		return err
	}
	return d.p.SaveMachine(ctx, m)
}

// InsertMachine implements Repository.
func (d *DurableRepo) InsertMachine(ctx context.Context, m Machine, s Settings, keyHash string) error {
	if err := d.MemRepo.InsertMachine(ctx, m, s, keyHash); err != nil {
		return err
	}
	if err := d.p.SaveMachine(ctx, m); err != nil {
		return err
	}
	if err := d.p.SaveSettings(ctx, m.ID, s); err != nil {
		return err
	}
	return d.p.SaveKey(ctx, keyHash, m.ID, false)
}

// AddKey gives a machine one more key (a rejoin of the same machine, OPS59).
func (d *DurableRepo) AddKey(ctx context.Context, machineID, keyHash string) error {
	if err := d.MemRepo.AddKey(ctx, machineID, keyHash); err != nil {
		return err
	}
	return d.p.SaveKey(ctx, keyHash, machineID, false)
}

// UpdateMachine implements Repository.
func (d *DurableRepo) UpdateMachine(ctx context.Context, m Machine) error {
	if err := d.MemRepo.UpdateMachine(ctx, m); err != nil {
		return err
	}
	return d.p.SaveMachine(ctx, m)
}

// RevokeKeys implements Repository.
func (d *DurableRepo) RevokeKeys(ctx context.Context, machineID string) error {
	if err := d.MemRepo.RevokeKeys(ctx, machineID); err != nil {
		return err
	}
	for _, h := range d.keysOf(machineID) {
		if err := d.p.SaveKey(ctx, h, machineID, true); err != nil {
			return err
		}
	}
	return nil
}

// MarkRemoved implements Repository.
func (d *DurableRepo) MarkRemoved(ctx context.Context, machineID string, at time.Time) error {
	if err := d.MemRepo.MarkRemoved(ctx, machineID, at); err != nil {
		return err
	}
	return d.saveMachine(ctx, machineID)
}

// PutSettings implements Repository.
func (d *DurableRepo) PutSettings(ctx context.Context, id string, s Settings) error {
	if err := d.MemRepo.PutSettings(ctx, id, s); err != nil {
		return err
	}
	return d.p.SaveSettings(ctx, id, s)
}

// InsertJoinCode implements Repository.
func (d *DurableRepo) InsertJoinCode(ctx context.Context, c JoinCode) error {
	if err := d.MemRepo.InsertJoinCode(ctx, c); err != nil {
		return err
	}
	return d.p.SaveCode(ctx, c)
}

// RevokeOpenJoinCodes implements Repository.
func (d *DurableRepo) RevokeOpenJoinCodes(ctx context.Context, at time.Time) error {
	if err := d.MemRepo.RevokeOpenJoinCodes(ctx, at); err != nil {
		return err
	}
	for _, c := range d.Snapshot().Codes {
		if c.RevokedAt.Equal(at) {
			if err := d.p.SaveCode(ctx, c); err != nil {
				return err
			}
		}
	}
	return nil
}

// RevokeJoinCode implements Repository.
func (d *DurableRepo) RevokeJoinCode(ctx context.Context, id string, at time.Time) (bool, error) {
	ok, err := d.MemRepo.RevokeJoinCode(ctx, id, at)
	if err != nil || !ok {
		return ok, err
	}
	c, err := d.GetJoinCode(ctx, id)
	if err != nil {
		return false, err
	}
	return true, d.p.SaveCode(ctx, c)
}

// UseJoinCode implements Repository.
func (d *DurableRepo) UseJoinCode(ctx context.Context, hash, machineID string, now time.Time) (bool, error) {
	ok, err := d.MemRepo.UseJoinCode(ctx, hash, machineID, now)
	if err != nil || !ok {
		return ok, err
	}
	c, err := d.FindJoinCodeByHash(ctx, hash)
	if err != nil {
		return false, err
	}
	return true, d.p.SaveCode(ctx, c)
}

// InsertChange implements Repository.
func (d *DurableRepo) InsertChange(ctx context.Context, c Change) error {
	if err := d.MemRepo.InsertChange(ctx, c); err != nil {
		return err
	}
	return d.p.SaveChange(ctx, c)
}

// UpdateChange implements Repository.
func (d *DurableRepo) UpdateChange(ctx context.Context, c Change) error {
	if err := d.MemRepo.UpdateChange(ctx, c); err != nil {
		return err
	}
	return d.p.SaveChange(ctx, c)
}
