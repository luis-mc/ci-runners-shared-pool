// Package machinehealth is the controller side of the machine health report
// (machines R5.70 to R5.77, OPS83): it keeps the last HealthReport of each
// machine and 24 hours of disk samples, works out the days until a disk is
// full, flags what is out of date, and serves the job stats per machine and
// per runner from the store.
package machinehealth

import (
	"context"
	"fmt"
	"sync"
	"time"

	"google.golang.org/protobuf/encoding/protojson"

	agentv1 "github.com/luis-mc/ci-runners-shared-pool/internal/gen/agent/v1"
	"github.com/luis-mc/ci-runners-shared-pool/internal/store"
)

// Store is the part of the store the service uses; *store.Store implements it.
type Store interface {
	PutMachineHealth(ctx context.Context, r store.MachineHealthRow) error
	GetMachineHealth(ctx context.Context, machineID string) (store.MachineHealthRow, bool, error)
	ListMachineHealth(ctx context.Context) ([]store.MachineHealthRow, error)
	DeleteMachineHealth(ctx context.Context, machineID string) error
	AddDiskSamples(ctx context.Context, machineID string, samples []store.DiskSample) error
	DiskSamples(ctx context.Context, machineID string, since time.Time) ([]store.DiskSample, error)
	PruneDiskSamples(ctx context.Context, before time.Time) (int64, error)
	JobStats(ctx context.Context, sc store.JobStatsScope, from, to time.Time) (store.JobStats, error)
	MachineRunnersWithJobs(ctx context.Context, machineID string, from, to time.Time) ([]store.RunnerRef, error)
	RawSetting(ctx context.Context, key string) (string, bool, error)
	PutRawSetting(ctx context.Context, key, jsonValue, by string) error
}

var _ Store = (*store.Store)(nil)

// SampleRetention is how long the disk samples are kept.
const SampleRetention = 24 * time.Hour

// StaleAfter is how old a report may be before the view says it is stale.
const StaleAfter = 3 * time.Minute

// Service is the machine health service.
type Service struct {
	Store Store
	Now   func() time.Time
	// ControllerVersion is what agents are compared with.
	ControllerVersion string
	// LatestRunner returns the latest known runner app version; nil or "" means none is known.
	LatestRunner func() string
	// Jobs says how many jobs run on a machine and whether it takes new jobs;
	// nil means nothing runs and the machine takes jobs.
	Jobs func(ctx context.Context, machineID string) (running int, taking bool)
	// Slots is the job slots of a machine, for its busy percent; nil or 0 means 1.
	Slots func(ctx context.Context, machineID string) int

	mu        sync.Mutex
	lastPrune time.Time
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Ingest stores one HealthReport: the report itself, the OOM state and a disk sample per known volume.
func (s *Service) Ingest(ctx context.Context, machineID string, r *agentv1.HealthReport) error {
	if r == nil {
		return nil
	}
	now := s.now()
	raw, err := protojson.MarshalOptions{UseProtoNames: true}.Marshal(r)
	if err != nil {
		return fmt.Errorf("health report: %w", err)
	}
	prev, had, err := s.Store.GetMachineHealth(ctx, machineID)
	if err != nil {
		return err
	}
	row := store.MachineHealthRow{MachineID: machineID, ReceivedAt: now, Report: string(raw), OOMTotal: prev.OOMTotal, LastOOMAt: prev.LastOOMAt}
	if o := r.GetOom(); o.GetKnown() {
		if had && o.GetTotalKills() > prev.OOMTotal {
			row.LastOOMAt = now
		}
		if k := o.GetLastKillUnixMs(); k > 0 && time.UnixMilli(k).After(row.LastOOMAt) {
			row.LastOOMAt = time.UnixMilli(k)
		}
		row.OOMTotal = o.GetTotalKills()
	}
	if err := s.Store.PutMachineHealth(ctx, row); err != nil {
		return err
	}
	var samples []store.DiskSample
	at := time.UnixMilli(r.GetSampledAtUnixMs())
	if r.GetSampledAtUnixMs() == 0 {
		at = now
	}
	for _, v := range r.GetVolumes() {
		if v.GetKnown() {
			samples = append(samples, store.DiskSample{Role: v.GetRole(), Path: v.GetPath(), At: at, FreeBytes: v.GetFreeBytes(), Total: v.GetTotalBytes()})
		}
	}
	if len(samples) > 0 {
		if err := s.Store.AddDiskSamples(ctx, machineID, samples); err != nil {
			return err
		}
	}
	return s.prune(ctx, now)
}

// prune drops disk samples older than 24 hours, at most once an hour.
func (s *Service) prune(ctx context.Context, now time.Time) error {
	s.mu.Lock()
	due := now.Sub(s.lastPrune) >= time.Hour
	if due {
		s.lastPrune = now
	}
	s.mu.Unlock()
	if !due {
		return nil
	}
	_, err := s.Store.PruneDiskSamples(ctx, now.Add(-SampleRetention))
	return err
}

// Remove drops what is kept of a machine that left the pool.
func (s *Service) Remove(ctx context.Context, machineID string) error {
	return s.Store.DeleteMachineHealth(ctx, machineID)
}

// OnMessage is the agent link hook: it takes HealthReport messages and says it did.
func (s *Service) OnMessage(ctx context.Context, machineID string, m *agentv1.SessionRequest) bool {
	hr, ok := m.GetMessage().(*agentv1.SessionRequest_HealthReport)
	if !ok {
		return false
	}
	_ = s.Ingest(ctx, machineID, hr.HealthReport)
	return true
}

// Disk is one volume with its trend.
type Disk struct {
	Role, Path    string
	Known         bool
	UnknownReason string
	Total, Free   int64
	FreePct       float64
	DaysUntilFull *float64
	DaysNote      string
}

// View is the health of one machine as the API and the Doctor read it.
type View struct {
	MachineID  string
	ReceivedAt time.Time
	// Reported is false when no report has arrived yet; the rest is empty then.
	Reported bool
	Stale    bool
	Report   *agentv1.HealthReport
	Disks    []Disk

	OOMLastAt time.Time
	// OOMRecent is true when the last OOM kill is inside the OOM window.
	OOMRecent bool

	AgentOutOfDate                  bool
	RunnerOutOfDate                 bool
	ControllerVersion, LatestRunner string

	// RestartMayHappenNow: a restart is pending and the OS may do it now.
	RestartMayHappenNow bool
	// RestartMayHitJobs: it may happen now and the machine runs or takes jobs.
	RestartMayHitJobs bool
	RunningJobs       int
	Taking            bool
}

// View builds the view of one machine.
func (s *Service) View(ctx context.Context, machineID string) (View, error) {
	row, ok, err := s.Store.GetMachineHealth(ctx, machineID)
	if err != nil {
		return View{}, err
	}
	if !ok {
		return View{MachineID: machineID}, nil
	}
	return s.build(ctx, row)
}

// Views builds the view of every machine that reported.
func (s *Service) Views(ctx context.Context) ([]View, error) {
	rows, err := s.Store.ListMachineHealth(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]View, 0, len(rows))
	for _, r := range rows {
		v, err := s.build(ctx, r)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

func (s *Service) build(ctx context.Context, row store.MachineHealthRow) (View, error) {
	now := s.now()
	rep := &agentv1.HealthReport{}
	if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal([]byte(row.Report), rep); err != nil {
		return View{}, fmt.Errorf("health report of %s: %w", row.MachineID, err)
	}
	th, err := s.Thresholds(ctx)
	if err != nil {
		return View{}, err
	}
	v := View{MachineID: row.MachineID, ReceivedAt: row.ReceivedAt, Reported: true, Stale: now.Sub(row.ReceivedAt) > StaleAfter, Report: rep,
		OOMLastAt: row.LastOOMAt, ControllerVersion: s.ControllerVersion, Taking: true}
	v.OOMRecent = !row.LastOOMAt.IsZero() && now.Sub(row.LastOOMAt) < time.Duration(th.OOMWindowMinutes)*time.Minute

	samples, err := s.Store.DiskSamples(ctx, row.MachineID, now.Add(-SampleRetention))
	if err != nil {
		return View{}, err
	}
	by := map[[2]string][]store.DiskSample{}
	for _, d := range samples {
		k := [2]string{d.Role, d.Path}
		by[k] = append(by[k], d)
	}
	for _, vol := range rep.GetVolumes() {
		d := Disk{Role: vol.GetRole(), Path: vol.GetPath(), Known: vol.GetKnown(), UnknownReason: vol.GetUnknownReason(), Total: vol.GetTotalBytes(), Free: vol.GetFreeBytes()}
		if d.Known && d.Total > 0 {
			d.FreePct = 100 * float64(d.Free) / float64(d.Total)
			t := DaysUntilFull(by[[2]string{d.Role, d.Path}])
			d.DaysUntilFull, d.DaysNote = t.Days, t.Note
		}
		v.Disks = append(v.Disks, d)
	}

	vers := rep.GetVersions()
	v.AgentOutOfDate = OutOfDate(vers.GetAgentVersion(), s.ControllerVersion)
	if s.LatestRunner != nil {
		v.LatestRunner = s.LatestRunner()
		v.RunnerOutOfDate = OutOfDate(vers.GetRunnerAppVersion(), v.LatestRunner)
	}

	if s.Jobs != nil {
		v.RunningJobs, v.Taking = s.Jobs(ctx, row.MachineID)
	}
	u := rep.GetUpdates()
	v.RestartMayHappenNow = u.GetKnown() && u.GetRestartPending() && u.GetRestartAllowedNow()
	v.RestartMayHitJobs = v.RestartMayHappenNow && (v.RunningJobs > 0 || v.Taking)
	return v, nil
}
