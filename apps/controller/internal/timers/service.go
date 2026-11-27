package timers

import (
	"context"
	"sync"
)

// Service is what the API calls: the settings with their check, and the
// actions of the grace by GitHub's job and run ids (scheduling R5.101, R5.104).
type Service struct {
	E        *Engine
	Settings SettingsStore

	mu  sync.Mutex
	cur Settings
	set bool
}

// Current returns the settings in force. The first call reads the store; Save
// refreshes it. It is what the engine reads.
func (s *Service) Current() Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.set {
		s.cur = s.Settings.Get(context.Background())
		s.set = true
	}
	return s.cur
}

// TimersEnforced says whether the timers stop jobs and cancel runs; false is
// observe mode. It is what the doctor reads.
func (s *Service) TimersEnforced(context.Context) (bool, error) { return s.Current().Enforce, nil }

// Get returns the settings in force.
func (s *Service) Get(context.Context) Settings { return s.Current() }

// Check validates a change without saving it.
func (s *Service) Check(_ context.Context, in Input) (Settings, []Problem) {
	return Check(s.Current(), in)
}

// Save validates and saves a change. A refused change returns its problems and
// saves nothing.
func (s *Service) Save(ctx context.Context, in Input, by string) (Settings, []Problem, error) {
	next, problems := Check(s.Current(), in)
	if len(problems) > 0 {
		return next, problems, nil
	}
	if err := s.Settings.Save(ctx, next, by); err != nil {
		return next, nil, err
	}
	s.mu.Lock()
	s.cur, s.set = next, true
	s.mu.Unlock()
	return next, nil, nil
}

// Job returns the timer of a job.
func (s *Service) Job(ctx context.Context, id int64) (JobView, bool) { return s.E.JobByID(ctx, id) }

// JobsByID returns the timers of many jobs held in memory in one read (the running jobs of a list).
func (s *Service) JobsByID(ids []int64) map[int64]JobView { return s.E.JobsByID(ids) }

// Run returns the timer of a run.
func (s *Service) Run(ctx context.Context, runID int64, attempt int) (RunView, bool) {
	return s.E.RunByID(ctx, runID, attempt)
}

// StopNow is the Stop now button of a job.
func (s *Service) StopNow(ctx context.Context, id int64, actor string) (JobView, error) {
	v, ok := s.E.JobByID(ctx, id)
	if !ok {
		return JobView{}, ErrUnknown
	}
	if err := s.E.StopNow(ctx, v.Key, actor); err != nil {
		return JobView{}, err
	}
	v, _ = s.E.JobByID(ctx, id)
	return v, nil
}

// Extend is the Extend 10 min button of a job.
func (s *Service) Extend(ctx context.Context, id int64, actor string) (JobView, error) {
	v, ok := s.E.JobByID(ctx, id)
	if !ok {
		return JobView{}, ErrUnknown
	}
	if err := s.E.Extend(ctx, v.Key, actor); err != nil {
		return JobView{}, err
	}
	v, _ = s.E.JobByID(ctx, id)
	return v, nil
}

// CancelNow is the Cancel now button of a run.
func (s *Service) CancelNow(ctx context.Context, runID int64, attempt int, actor string) (RunView, error) {
	v, ok := s.E.RunByID(ctx, runID, attempt)
	if !ok {
		return RunView{}, ErrUnknown
	}
	if err := s.E.CancelNow(ctx, v.Key, actor); err != nil {
		return RunView{}, err
	}
	v, _ = s.E.RunByID(ctx, runID, v.Key.Attempt)
	return v, nil
}

// ExtendRun is the Extend 10 min button of a run.
func (s *Service) ExtendRun(ctx context.Context, runID int64, attempt int, actor string) (RunView, error) {
	v, ok := s.E.RunByID(ctx, runID, attempt)
	if !ok {
		return RunView{}, ErrUnknown
	}
	if err := s.E.ExtendRun(ctx, v.Key, actor); err != nil {
		return RunView{}, err
	}
	v, _ = s.E.RunByID(ctx, runID, v.Key.Attempt)
	return v, nil
}
