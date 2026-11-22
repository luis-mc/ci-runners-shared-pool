package update

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/update/lifecycle"
)

// Engine is the install path the lifecycle service drives. *Manager is the real one.
type Engine interface {
	LatestManifest(ctx context.Context) ([]byte, Release, error)
	PrepareVersion(ctx context.Context, version string) (string, error)
	Apply(ctx context.Context, kind Kind, confirmed bool, actor string) (Status, error)
}

var _ Engine = (*Manager)(nil)

// HistoryRow is one line of the update history (check, install, rollback or remove).
type HistoryRow struct {
	At      time.Time `json:"at"`
	Actor   string    `json:"actor"`
	Action  string    `json:"action"`
	Version string    `json:"version"`
	Result  string    `json:"result"`
}

// LifecycleStatus is the contract's status object (GET /api/v1/update/status).
type LifecycleStatus struct {
	State          string
	Current        string
	Available      string // the offered version, empty when none
	Required       bool
	Policy         string
	Window         *lifecycle.Window
	NextCheckAt    *time.Time
	Failures       *FailureView
	WaitingForJobs bool
}

// FailureView is the failed-install record of the offered version.
type FailureView struct {
	Version string
	Count   int
	RetryAt *time.Time
}

// ServiceOptions configure a Service.
type ServiceOptions struct {
	Engine  Engine
	Current string
	Key     ed25519.PublicKey
	// Dir is the update work folder; the policy and the install record live there.
	Dir     string
	Channel string
	// Target is this machine's key in the signed manifest's assets table, such as win-arm64. Empty
	// takes the manifest's default package.
	Target string
	// Paired says the machine has joined the pool: no check runs before.
	Paired func(ctx context.Context) bool
	// Idle says no job runs on this machine.
	Idle func(ctx context.Context) bool
	// Audit receives every row (check, install, refused, rollback, skip) with actor and result.
	Audit func(ctx context.Context, r lifecycle.Row)
	// History reads the persistent audit trail; nil uses the rows of this process.
	History func(ctx context.Context) ([]HistoryRow, error)
	Now     func() time.Time
	Logger  *slog.Logger
}

// Service runs the lifecycle state machine on the real clock for the controller.
type Service struct {
	o  ServiceOptions
	mu sync.Mutex
	u  *lifecycle.Updater
	// ctx is the context of the call in progress: the core is synchronous, the engine needs one.
	ctx context.Context
	rel Release
}

type persisted struct {
	Policy string            `json:"policy"`
	Window *lifecycle.Window `json:"window"`
}

type installRecord struct {
	Actor   string `json:"actor"`
	Remote  bool   `json:"remote"`
	Version string `json:"version"`
}

const (
	lifecycleFile = "lifecycle.json"
	installFile   = "install-actor.json"
)

// NewService builds the service, restores the policy and the outcome of an install that a
// restart ended, and writes that outcome to the audit trail.
func NewService(ctx context.Context, o ServiceOptions) *Service {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Channel == "" {
		o.Channel = "stable"
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	s := &Service{o: o, ctx: ctx}
	p := persisted{Policy: lifecycle.PolicyAutomatic}
	if b, err := os.ReadFile(filepath.Join(o.Dir, lifecycleFile)); err == nil { //nolint:gosec // work folder
		_ = json.Unmarshal(b, &p)
	}
	if p.Policy != lifecycle.PolicyAskFirst {
		p.Policy = lifecycle.PolicyAutomatic
	}
	now := float64(o.Now().Unix())
	paired := o.Paired == nil || o.Paired(ctx)
	idle := o.Idle == nil || o.Idle(ctx)
	seed := rand.Int63() //nolint:gosec // jitter, not a secret
	s.u = lifecycle.New(o.Current, paired, p.Policy, p.Window, o.Channel, idle, now, "", o.Key, seed, &serviceEnv{s: s}, rowSink{s})
	s.u.Target = o.Target
	s.restore()
	return s
}

// restore reads the result of the last install. A good one is audited as ok; a failed one puts the
// updater back to rolled_back with its retry time (1 h, then 4 h, then skipped).
func (s *Service) restore() {
	r, ok := ReadResult(s.o.Dir)
	if !ok || r.Kind != string(KindUpdate) {
		return
	}
	rec := s.readInstallRecord(r.To)
	if marker := filepath.Join(s.o.Dir, installFile); rec.Version == r.To {
		defer func() { _ = os.Remove(marker) }() // the outcome is audited once
	} else {
		return // already audited, or an update this service did not start
	}
	row := func(kind, result string) {
		s.u.Rows = append(s.u.Rows, lifecycle.Row{At: float64(r.At.Unix()), Kind: kind, Actor: rec.Actor, Result: result, Remote: rec.Remote, Version: r.To})
		s.emit(s.u.Rows[len(s.u.Rows)-1])
	}
	if r.OK && r.To == s.o.Current {
		row("install", "ok")
		s.u.Restore(r.To, true, 0, 0)
		return
	}
	if r.OK || r.To == s.o.Current {
		return
	}
	row("install", "rolled_back")
	row("rollback", "ok")
	f, _ := ReadFailures(s.o.Dir)
	n := 1
	if f.Version == r.To && f.Count > 0 {
		n = f.Count
	}
	if n >= 3 {
		row("skip", "ok")
	}
	s.u.Restore(r.To, false, n, float64(r.At.Unix()))
}

func (s *Service) readInstallRecord(version string) installRecord {
	var rec installRecord
	if b, err := os.ReadFile(filepath.Join(s.o.Dir, installFile)); err == nil { //nolint:gosec // work folder
		_ = json.Unmarshal(b, &rec)
	}
	return rec
}

func (s *Service) emit(r lifecycle.Row) {
	if s.o.Audit != nil {
		s.o.Audit(s.ctx, r)
	}
}

type rowSink struct{ s *Service }

func (r rowSink) Audit(row lifecycle.Row) { r.s.emit(row) }

// serviceEnv connects the core to the engine.
type serviceEnv struct{ s *Service }

func (e *serviceEnv) Get(string) ([]byte, error) {
	raw, rel, err := e.s.o.Engine.LatestManifest(e.s.ctx)
	if err != nil {
		return nil, err
	}
	e.s.rel = rel
	return raw, nil
}

func (e *serviceEnv) Package(m lifecycle.Manifest) (string, error) {
	return e.s.o.Engine.PrepareVersion(e.s.ctx, m.Version)
}

func (e *serviceEnv) Install(m lifecycle.Manifest, actor string, remote bool) (bool, error) {
	b, _ := json.Marshal(installRecord{Actor: actor, Remote: remote, Version: m.Version})
	_ = os.WriteFile(filepath.Join(e.s.o.Dir, installFile), b, 0o600)
	who := actor
	if actor == "updater" {
		who = "auto-update" // the engine waits at most its drain limit for an automatic install
	}
	if _, err := e.s.o.Engine.Apply(e.s.ctx, KindUpdate, true, who); err != nil {
		_ = os.Remove(filepath.Join(e.s.o.Dir, installFile))
		return true, err
	}
	return false, nil // the helper installs and restarts the service
}

// Run ticks the state machine every interval until ctx ends.
func (s *Service) Run(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.Tick(ctx)
		}
	}
}

// Tick reads pairing and idleness, then runs every timer that is due.
func (s *Service) Tick(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ctx = ctx
	s.refresh(ctx)
	s.u.Tick(float64(s.o.Now().Unix()))
}

func (s *Service) refresh(ctx context.Context) {
	if s.o.Paired != nil {
		if p := s.o.Paired(ctx); p != s.u.Paired {
			s.u.SetPaired(p)
		}
	}
	if s.o.Idle != nil {
		idle := s.o.Idle(ctx)
		if idle != s.u.Idle {
			s.u.Idle = idle
			s.u.Reeval()
		}
	}
	if now := float64(s.o.Now().Unix()); now > s.u.Now {
		s.u.Now = now
	}
}

// ErrNotPaired says no check runs before the machine has joined the pool.
var ErrNotPaired = errors.New("this machine has not joined the pool yet, so no update check runs")

// Check looks for an update now, as actor.
func (s *Service) Check(ctx context.Context, actor string) (LifecycleStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ctx = ctx
	s.refresh(ctx)
	if !s.u.CheckAs(actor) {
		return s.statusLocked(), ErrNotPaired
	}
	return s.statusLocked(), nil
}

// ErrNoOffer says there is nothing to install: no newer version is offered, or it is not this one.
var ErrNoOffer = errors.New("that version is not on offer")

// Install installs the offered version on a click (the signed-in owner's request, remote or not).
func (s *Service) Install(ctx context.Context, actor string, remote bool, version string) (LifecycleStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ctx = ctx
	s.refresh(ctx)
	if s.u.Offered == nil || (version != "" && s.u.Offered.Version != version) {
		return s.statusLocked(), ErrNoOffer
	}
	s.u.Install(actor, remote, true)
	return s.statusLocked(), nil
}

// SetPolicy saves the install policy and its window.
func (s *Service) SetPolicy(ctx context.Context, policy string, w *lifecycle.Window) (LifecycleStatus, error) {
	if policy != lifecycle.PolicyAutomatic && policy != lifecycle.PolicyAskFirst {
		return LifecycleStatus{}, fmt.Errorf("policy must be %s or %s", lifecycle.PolicyAutomatic, lifecycle.PolicyAskFirst)
	}
	if w != nil && (!validClock(w.Start) || !validClock(w.End)) {
		return LifecycleStatus{}, errors.New("a window needs start and end as HH:MM")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ctx = ctx
	b, _ := json.Marshal(persisted{Policy: policy, Window: w})
	if err := os.WriteFile(filepath.Join(s.o.Dir, lifecycleFile), b, 0o600); err != nil {
		return LifecycleStatus{}, err
	}
	s.u.Policy, s.u.Window = policy, w
	s.refresh(ctx)
	s.u.Reeval()
	return s.statusLocked(), nil
}

func validClock(v string) bool {
	_, err := time.Parse("15:04", v)
	return err == nil
}

// Status is the contract status object.
func (s *Service) Status(ctx context.Context) LifecycleStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ctx = ctx
	s.refresh(ctx)
	return s.statusLocked()
}

func (s *Service) statusLocked() LifecycleStatus {
	u := s.u
	st := LifecycleStatus{State: u.State, Current: u.Version, Policy: u.Policy, Window: u.Window,
		NextCheckAt: epoch(u.NextCheckAt), WaitingForJobs: u.WaitingForJobs()}
	if u.Offered != nil {
		st.Available, st.Required = u.Offered.Version, u.Offered.Required
		if u.Failures > 0 {
			st.Failures = &FailureView{Version: u.Offered.Version, Count: u.Failures, RetryAt: epoch(u.RetryAt)}
		}
	}
	return st
}

func epoch(f *float64) *time.Time {
	if f == nil {
		return nil
	}
	t := time.Unix(int64(*f), 0).UTC()
	return &t
}

// History returns the update history, newest first.
func (s *Service) History(ctx context.Context) ([]HistoryRow, error) {
	if s.o.History != nil {
		return s.o.History(ctx)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]HistoryRow, 0, len(s.u.Rows))
	for i := len(s.u.Rows) - 1; i >= 0; i-- {
		out = append(out, RowToHistory(s.u.Rows[i]))
	}
	return out, nil
}

// RowToHistory maps an audit row to the history's action names (check, install, rollback, remove).
func RowToHistory(r lifecycle.Row) HistoryRow {
	action, result := r.Kind, r.Result
	switch r.Kind {
	case "refused":
		action, result = "install", "refused"
	case "skip":
		action, result = "install", "skipped"
	}
	return HistoryRow{At: time.Unix(int64(r.At), 0).UTC(), Actor: r.Actor, Action: action, Version: r.Version, Result: result}
}
