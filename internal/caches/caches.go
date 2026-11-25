// Package caches is the controller side of the per-runner caches of milestone 1
// (caches R5.36, R5.39; web-console R5.25): each machine's agent measures the
// tool cache and the Rust build cache of every runner and reports; the
// controller lists them with size against cap, and wipes one on request.
//
// A wipe never cuts a running job: the agent keeps new jobs of that runner out,
// waits until the runner is idle, checks again right before it deletes, and
// reports every step (caches R5.32, runners R6.4). The wipe is audited.
package caches

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/audit"
	agentv1 "github.com/luis-mc/ci-runners-shared-pool/internal/gen/agent/v1"
	"github.com/luis-mc/ci-runners-shared-pool/internal/machines"
	"github.com/luis-mc/ci-runners-shared-pool/internal/mirror"
)

// Kinds of cache this milestone knows (the API enum values).
const (
	KindTool = "tool_cache"
	KindRust = "rust_cache"
	// KindDependency is the whole dependency folder of one persistent runner:
	// one line per persistent runner with its size (caches R5.56, runners R5.90).
	KindDependency = "dependency_cache"
	// KindActions is the Actions cache receiver of the holder (R5.50 step 3).
	KindActions = "actions_cache"
)

// State of a cache.
const (
	StateReady  = "ready"
	StateWiping = "wiping"
)

// Errors the API maps to the error envelope.
var (
	ErrNotFound = errors.New("no such cache")
	// ErrOffline means the machine's agent is not connected, so it can neither
	// measure nor wipe.
	ErrOffline = errors.New("the machine is offline")
)

// RefusalError is a refused wipe with a reason fit to show.
type RefusalError struct{ Reason string }

func (e *RefusalError) Error() string { return e.Reason }

// ConfirmItem is one line of the question card: what goes or what it waits for.
type ConfirmItem struct{ Kind, Text string }

// NeedsConfirmation says the wipe needs a yes (UX-62). Items name the cache with
// its size and each running job the drain waits for.
type NeedsConfirmation struct{ Items []ConfirmItem }

func (e *NeedsConfirmation) Error() string { return "this wipe needs a yes" }

// Cache is one cache folder of one runner.
type Cache struct {
	ID          string
	Kind        string
	MachineID   string
	RunnerName  string
	Size        int64
	Cap         int64 // 0 when the cache has no cap
	Entries     int
	LastWipedAt time.Time
	State       string
	WaitingFor  []string
	Error       string
	// The rows of a cache holder (mirrors.go).
	Name        string
	Status      string
	LastRefresh time.Time
	Hits        int64
	Misses      int64
	BytesServed int64
}

// Machines is what the service needs from the machines area.
type Machines interface {
	List(ctx context.Context) ([]machines.View, error)
	Get(ctx context.Context, id string) (machines.View, error)
	Send(machineID string, msg *agentv1.SessionResponse) error
	Connected(machineID string) bool
}

var _ Machines = (*machines.Service)(nil)

// Options configure a Service.
type Options struct {
	Machines Machines
	Audit    audit.Recorder
	Now      func() time.Time
	NewID    func() string
	// Mirrors serves the rows of the cache holder; nil gives none (R5.71).
	Mirrors Mirrors
	// HolderID is the machine id those rows carry; "controller" by default.
	HolderID string
}

type entry struct {
	Cache
	runnerService string
}

type wipe struct {
	cacheID, commandID string
	actor              audit.Actor
	before             int64
	started            time.Time
}

// Service keeps the newest report of each machine and the wipes in progress.
// The reports are what the agents measured; the service never invents a size.
type Service struct {
	o Options

	mu      sync.Mutex
	reports map[string][]entry // machine id -> its last report
	wiped   map[string]time.Time
	wipes   map[string]*wipe // command id -> wipe
	seq     int
	mirror  mirrorState
}

// New returns a Service.
func New(o Options) *Service {
	if o.Now == nil {
		o.Now = func() time.Time { return time.Now().UTC() }
	}
	return &Service{o: o, reports: map[string][]entry{}, wiped: map[string]time.Time{}, wipes: map[string]*wipe{},
		mirror: mirrorState{wipes: map[string]*mirrorWipe{}, done: map[string]time.Time{}, last: map[string]string{}}}
}

func slug(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return b.String()
}

// CacheID is the stable id of a runner's cache of one kind on one machine.
func CacheID(machineID, runnerName, kind string) string {
	return "cch_" + slug(machineID) + "_" + slug(runnerName) + "_" + kind
}

// OnHello asks a machine that just connected to measure its caches at once, so
// the list is current before the agent's own timer fires.
func (s *Service) OnHello(_ context.Context, machineID string, _ *agentv1.Hello) {
	_ = s.o.Machines.Send(machineID, &agentv1.SessionResponse{Message: &agentv1.SessionResponse_MeasureCaches{MeasureCaches: &agentv1.MeasureCaches{}}})
}

// OnMessage takes the cache messages of the agent link. It returns true when it
// handled the message.
func (s *Service) OnMessage(ctx context.Context, machineID string, msg *agentv1.SessionRequest, _ func(*agentv1.SessionResponse)) bool {
	switch m := msg.GetMessage().(type) {
	case *agentv1.SessionRequest_CacheReport:
		s.HandleReport(machineID, m.CacheReport)
		return true
	case *agentv1.SessionRequest_CacheWipeStatus:
		s.HandleWipeStatus(ctx, machineID, m.CacheWipeStatus)
		return true
	}
	return false
}

// HandleReport replaces the machine's report with the one the agent measured.
func (s *Service) HandleReport(machineID string, r *agentv1.CacheReport) {
	s.mu.Lock()
	defer s.mu.Unlock()
	old := map[string]entry{}
	for _, e := range s.reports[machineID] {
		old[e.ID] = e
	}
	out := make([]entry, 0, len(r.GetCaches()))
	for _, c := range r.GetCaches() {
		kind := c.GetKind()
		if kind != KindTool && kind != KindRust && kind != KindDependency {
			continue
		}
		id := CacheID(machineID, c.GetRunnerName(), kind)
		e := entry{runnerService: c.GetServiceName(), Cache: Cache{
			ID: id, Kind: kind, MachineID: machineID, RunnerName: c.GetRunnerName(), Size: c.GetSizeBytes(), Cap: c.GetCapBytes(),
			Entries: int(c.GetEntryCount()), LastWipedAt: s.wiped[id], State: StateReady,
		}}
		// A measure that arrives while a wipe runs does not end it.
		if o, ok := old[id]; ok && o.State == StateWiping {
			e.State, e.WaitingFor = o.State, o.WaitingFor
		}
		out = append(out, e)
	}
	s.reports[machineID] = out
}

// List returns the caches of connected or known machines, newest report. A
// removed machine's caches are dropped. machineID and kind narrow the list when
// not empty.
func (s *Service) List(ctx context.Context, machineID, kind string) ([]Cache, error) {
	out, err := s.listReports(ctx, machineID, kind)
	if err != nil {
		return nil, err
	}
	mr, err := s.mirrorRows(ctx, machineID, kind)
	if err != nil {
		return nil, err
	}
	return append(out, mr...), nil
}

func (s *Service) listReports(ctx context.Context, machineID, kind string) ([]Cache, error) {
	ms, err := s.o.Machines.List(ctx)
	if err != nil {
		return nil, err
	}
	known := map[string]bool{}
	for _, m := range ms {
		known[m.Machine.ID] = true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Cache
	for id, es := range s.reports {
		if !known[id] {
			delete(s.reports, id)
			continue
		}
		if machineID != "" && id != machineID {
			continue
		}
		for _, e := range es {
			if kind == "" || e.Kind == kind {
				out = append(out, e.Cache)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.MachineID != b.MachineID {
			return a.MachineID < b.MachineID
		}
		if a.RunnerName != b.RunnerName {
			return a.RunnerName < b.RunnerName
		}
		return a.Kind < b.Kind
	})
	return out, nil
}

func (s *Service) find(id string) (entry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, es := range s.reports {
		for _, e := range es {
			if e.ID == id {
				return e, true
			}
		}
	}
	return entry{}, false
}

// KindName is how a question card calls a cache.
func KindName(kind string) string {
	switch kind {
	case KindTool:
		return "tool cache"
	case KindRust:
		return "Rust build cache"
	case KindDependency:
		return "dependency cache"
	}
	return kind
}

// BeginWipe checks that the cache exists and the machine is online, then asks
// for a yes naming the cache, its size and the jobs the drain waits for
// (UX-62). With confirm it sends the wipe to the agent and returns the cache as
// wiping. Nothing is deleted here: the agent deletes when the runner is idle.
func (s *Service) BeginWipe(ctx context.Context, actor audit.Actor, id string, confirm bool) (Cache, error) {
	if strings.HasPrefix(id, mirror.IDPrefix) {
		return s.beginMirrorWipe(ctx, actor, id, confirm)
	}
	e, ok := s.find(id)
	if !ok {
		return Cache{}, ErrNotFound
	}
	v, err := s.o.Machines.Get(ctx, e.MachineID)
	if err != nil {
		return Cache{}, ErrNotFound
	}
	if !s.o.Machines.Connected(e.MachineID) {
		return Cache{}, &RefusalError{Reason: fmt.Sprintf("%s is offline; the wipe needs its agent", v.Machine.Name)}
	}
	if e.State == StateWiping {
		return Cache{}, &RefusalError{Reason: fmt.Sprintf("the %s of runner %s is already being wiped", KindName(e.Kind), e.RunnerName)}
	}
	jobs := runnerJobs(v.RunningJobs, e.RunnerName)
	if !confirm {
		items := []ConfirmItem{{Kind: "cache", Text: fmt.Sprintf("the %s of runner %s on %s, %s", KindName(e.Kind), e.RunnerName, v.Machine.Name, FormatBytes(e.Size))}}
		for _, j := range jobs {
			items = append(items, ConfirmItem{Kind: "job", Text: "waits for " + j})
		}
		return Cache{}, &NeedsConfirmation{Items: items}
	}
	timeout := v.Settings.DrainTimeoutMinutes
	if timeout <= 0 {
		timeout = 60
	}
	s.mu.Lock()
	s.seq++
	cmd := "wipe-" + e.ID + fmt.Sprintf("-%d", s.seq)
	s.wipes[cmd] = &wipe{cacheID: id, actor: actor, commandID: cmd, before: e.Size, started: s.o.Now()}
	s.setLocked(id, func(c *entry) { c.State, c.WaitingFor, c.Error = StateWiping, jobs, "" })
	s.mu.Unlock()
	err = s.o.Machines.Send(e.MachineID, &agentv1.SessionResponse{Message: &agentv1.SessionResponse_WipeCache{WipeCache: &agentv1.WipeCache{
		CommandId: cmd, RunnerName: e.RunnerName, Kind: e.Kind, DrainTimeoutSeconds: int32(timeout * 60), //nolint:gosec // bounded
	}}})
	if err != nil {
		s.mu.Lock()
		delete(s.wipes, cmd)
		s.setLocked(id, func(c *entry) { c.State, c.WaitingFor = StateReady, nil })
		s.mu.Unlock()
		return Cache{}, &RefusalError{Reason: fmt.Sprintf("%s is offline; the wipe needs its agent", v.Machine.Name)}
	}
	_ = s.audit(ctx, actor, "cache.wipe.start", e.MachineID, id, map[string]any{
		"summary": fmt.Sprintf("wipe the %s of runner %s on %s", KindName(e.Kind), e.RunnerName, v.Machine.Name),
		"kind":    e.Kind, "runner": e.RunnerName, "machine": v.Machine.Name, "size_bytes": e.Size, "waits_for": jobs,
	})
	got, _ := s.find(id)
	return got.Cache, nil
}

func runnerJobs(jobs []machines.JobRef, runner string) []string {
	var out []string
	for _, j := range jobs {
		if j.RunnerName == runner {
			out = append(out, fmt.Sprintf("%s, run %d, job %s", j.Repository, j.RunID, j.Job))
		}
	}
	return out
}

func (s *Service) setLocked(id string, f func(*entry)) {
	for m, es := range s.reports {
		for i := range es {
			if es[i].ID == id {
				f(&es[i])
				s.reports[m] = es
				return
			}
		}
	}
}

// audit writes one entry; it adds the machine to the details. A failed write is
// returned so the caller can say so; the wipe itself does not wait on it.
func (s *Service) audit(ctx context.Context, actor audit.Actor, action, machineID, cacheID string, d map[string]any) error {
	if s.o.Audit == nil {
		return nil
	}
	d["machine_id"] = machineID
	return s.o.Audit.Record(ctx, actor, action, audit.Target{Type: "cache", ID: cacheID}, d)
}

// HandleWipeStatus takes the agent's report of a wipe: waiting (with the jobs),
// wiping, done or failed. Done and failed are audited with the bytes freed
// (caches R5.34). After a wipe the agent measures again.
func (s *Service) HandleWipeStatus(ctx context.Context, machineID string, st *agentv1.CacheWipeStatus) {
	s.mu.Lock()
	w, ok := s.wipes[st.GetCommandId()]
	if !ok {
		s.mu.Unlock()
		return
	}
	var names []string
	for _, j := range st.GetWaitingFor() {
		names = append(names, fmt.Sprintf("%s, run %d, job %s", j.GetRepository(), j.GetRunId(), j.GetJob()))
	}
	id := w.cacheID
	switch st.GetState() {
	case "waiting":
		s.setLocked(id, func(c *entry) { c.State, c.WaitingFor = StateWiping, names })
		s.mu.Unlock()
		return
	case "wiping":
		s.setLocked(id, func(c *entry) { c.State, c.WaitingFor = StateWiping, nil })
		s.mu.Unlock()
		return
	}
	delete(s.wipes, st.GetCommandId())
	done := st.GetState() == "done"
	now := s.o.Now()
	if done {
		s.wiped[id] = now
	}
	s.setLocked(id, func(c *entry) {
		c.State, c.WaitingFor = StateReady, nil
		if done {
			c.Size, c.Entries, c.LastWipedAt, c.Error = 0, 0, now, ""
		} else {
			c.Error = st.GetError()
		}
	})
	e, _ := s.entryLocked(id)
	s.mu.Unlock()
	action, details := "cache.wipe.done", map[string]any{
		"summary": fmt.Sprintf("wiped the %s of runner %s, %s freed", KindName(e.Kind), e.RunnerName, FormatBytes(st.GetFreedBytes())),
		"kind":    e.Kind, "runner": e.RunnerName, "size_bytes": w.before, "freed_bytes": st.GetFreedBytes(),
	}
	if !done {
		action = "cache.wipe.failed"
		details["summary"] = fmt.Sprintf("the wipe of the %s of runner %s failed", KindName(e.Kind), e.RunnerName)
		details["error"] = st.GetError()
	}
	_ = s.audit(ctx, w.actor, action, machineID, id, details)
	// Measure again so the list shows what is really there.
	_ = s.o.Machines.Send(machineID, &agentv1.SessionResponse{Message: &agentv1.SessionResponse_MeasureCaches{MeasureCaches: &agentv1.MeasureCaches{}}})
}

func (s *Service) entryLocked(id string) (entry, bool) {
	for _, es := range s.reports {
		for _, e := range es {
			if e.ID == id {
				return e, true
			}
		}
	}
	return entry{}, false
}

// FormatBytes writes a size the way the question card and the audit log do:
// binary units with one decimal (the console uses its own formatter for the
// screen, UX-18).
func FormatBytes(n int64) string {
	const k = 1024
	if n < k {
		return fmt.Sprintf("%d B", n)
	}
	units := []string{"KiB", "MiB", "GiB", "TiB"}
	f := float64(n)
	i := -1
	for f >= k && i < len(units)-1 {
		f /= k
		i++
	}
	return fmt.Sprintf("%.1f %s", f, units[i])
}
