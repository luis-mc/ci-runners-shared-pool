package depsetup

import (
	"context"
	"sort"
	"sync"
)

// MemRepo is an in-memory Repo for tests.
type MemRepo struct {
	mu   sync.Mutex
	jobs map[string]Job
}

// NewMemRepo returns an empty MemRepo.
func NewMemRepo() *MemRepo { return &MemRepo{jobs: map[string]Job{}} }

func clone(j Job) Job {
	j.ItemIDs = append([]string(nil), j.ItemIDs...)
	j.Added = append([]Added(nil), j.Added...)
	j.Log = append([]Event(nil), j.Log...)
	j.ManualSteps = append([]string(nil), j.ManualSteps...)
	if j.Last != nil {
		l := *j.Last
		j.Last = &l
	}
	return j
}

// Put implements Repo.
func (r *MemRepo) Put(_ context.Context, j Job) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.jobs[j.ID] = clone(j)
	return nil
}

// Get implements Repo.
func (r *MemRepo) Get(_ context.Context, machineID, id string) (Job, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	j, ok := r.jobs[id]
	if !ok || j.MachineID != machineID {
		return Job{}, false, nil
	}
	return clone(j), true, nil
}

// List implements Repo.
func (r *MemRepo) List(_ context.Context, machineID string, limit int) ([]Job, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []Job
	for _, j := range r.jobs {
		if j.MachineID == machineID {
			out = append(out, clone(j))
		}
	}
	sort.Slice(out, func(a, b int) bool {
		if !out[a].CreatedAt.Equal(out[b].CreatedAt) {
			return out[a].CreatedAt.After(out[b].CreatedAt)
		}
		return out[a].ID > out[b].ID
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// Unfinished implements Repo.
func (r *MemRepo) Unfinished(_ context.Context, machineID string) ([]Job, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []Job
	for _, j := range r.jobs {
		if j.MachineID == machineID && Running(j.State) {
			out = append(out, clone(j))
		}
	}
	return out, nil
}
