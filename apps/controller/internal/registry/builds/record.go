package builds

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api/gen"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/events"
)

// Build states of the image_builds table.
const (
	StateQueued    = "queued"
	StateBuilding  = "building"
	StateDone      = "done"
	StateFailed    = "failed"
	StateCancelled = "cancelled"
)

// Meta says what is built and why (R5.69). Reason is empty when none is known;
// REG2 sets `not_in_registry` for a build after the controller said so (R5.71).
type Meta struct {
	MachineID string
	Profile   string
	Arch      string
	Key       string
	Reason    string
}

// Outcome is how a build ended.
type Outcome struct {
	State        string // done, failed or cancelled
	SizeBytes    int64
	FailureStep  string
	FailureClass string
	FailureWhy   string
	LogTail      string
}

// Sink receives the life of one build. The container backend calls it; the
// controller's Recorder keeps it. A nil Sink records nothing and never blocks
// a build, and a Sink error is only logged.
type Sink interface {
	Begin(ctx context.Context, m Meta) (Handle, error)
	Progress(ctx context.Context, id string, p Progress)
	End(ctx context.Context, id string, o Outcome)
}

// Handle names a started record and carries the typical seconds of the last
// builds of the same profile and arch, so the agent can say a time left in its
// own log lines. TypicalSeconds is 0 with no history.
type Handle struct {
	ID             string
	TypicalSeconds float64
}

type metaKey struct{}

// WithMeta tells the build why it runs and for which profile (REG2 passes
// Reason `not_in_registry`, R5.71). Machine, arch and key come from the backend.
func WithMeta(ctx context.Context, m Meta) context.Context {
	return context.WithValue(ctx, metaKey{}, m)
}

// MetaFrom reads what WithMeta set; the zero Meta when nothing was set.
func MetaFrom(ctx context.Context) Meta {
	m, _ := ctx.Value(metaKey{}).(Meta)
	return m
}

// Record is one build as the store keeps it (table image_builds).
type Record struct {
	ID             string
	MachineID      string
	Profile        string
	Arch           string
	Key            string
	Reason         string
	State          string
	Step           int
	StepsTotal     int
	StepName       string
	TypicalSeconds float64
	SizeBytes      int64
	FailureStep    string
	FailureClass   string
	FailureWhy     string
	LogTail        string
	PushState      string
	QueuedAt       time.Time
	StartedAt      time.Time // zero until the first step
	FinishedAt     time.Time // zero while it runs
}

// Store keeps the records. internal/store implements it over queries/image_builds.sql.
type Store interface {
	Insert(ctx context.Context, r Record) error
	// Get returns false when there is no such build.
	Get(ctx context.Context, id string) (Record, bool, error)
	SetProgress(ctx context.Context, id, state string, step, total int, name string, startedAt time.Time) error
	Finish(ctx context.Context, id string, o Outcome, at time.Time) error
	// Typical is the mean seconds of the last 5 done builds of the profile and arch; 0 with none.
	Typical(ctx context.Context, profile, arch string) (float64, error)
	// List gives the newest builds first, of one machine when machineID is set.
	List(ctx context.Context, machineID string, limit int) ([]Record, error)
}

// Recorder keeps each build as a record and tells the console (R5.69, R5.72).
type Recorder struct {
	Q      Store
	Events events.Publisher // nil: no events
	Now    func() time.Time // nil: the clock
	NewID  func() string    // nil: bld_ and random hex

	mu      sync.Mutex
	started map[string]time.Time
}

var _ Sink = (*Recorder)(nil)

func (r *Recorder) now() time.Time {
	if r.Now != nil {
		return r.Now().UTC()
	}
	return time.Now().UTC()
}

// Begin writes the record as queued, with the typical seconds of the last five
// builds of the profile and arch (zero with no history).
func (r *Recorder) Begin(ctx context.Context, m Meta) (Handle, error) {
	id := "bld_"
	if r.NewID != nil {
		id = r.NewID()
	} else {
		var b [8]byte
		if _, err := rand.Read(b[:]); err != nil {
			return Handle{}, err
		}
		id += hex.EncodeToString(b[:])
	}
	typical, err := r.Q.Typical(ctx, m.Profile, m.Arch)
	if err != nil || typical < 0 {
		typical = 0 // no history is not a reason to refuse a build
	}
	if err := r.Q.Insert(ctx, Record{
		ID: id, MachineID: m.MachineID, Profile: m.Profile, Arch: m.Arch, Key: m.Key, Reason: m.Reason, TypicalSeconds: typical,
		State: StateQueued, PushState: "none", QueuedAt: r.now(),
	}); err != nil {
		return Handle{}, fmt.Errorf("record the build: %w", err)
	}
	r.publish(ctx, id)
	return Handle{ID: id, TypicalSeconds: typical}, nil
}

// Progress moves the record to building and keeps the step. The first call
// sets started_at.
func (r *Recorder) Progress(ctx context.Context, id string, p Progress) {
	r.mu.Lock()
	if r.started == nil {
		r.started = map[string]time.Time{}
	}
	at, ok := r.started[id]
	if !ok {
		at = r.now()
		r.started[id] = at
	}
	r.mu.Unlock()
	if err := r.Q.SetProgress(ctx, id, StateBuilding, p.Step, p.Total, p.Name, at); err != nil {
		return
	}
	r.publishWith(ctx, id, &p)
}

// End writes the outcome with the finish time; the duration is finished_at
// minus started_at.
func (r *Recorder) End(ctx context.Context, id string, o Outcome) {
	if err := r.Q.Finish(ctx, id, o, r.now()); err != nil {
		return
	}
	r.mu.Lock()
	delete(r.started, id)
	r.mu.Unlock()
	r.publish(ctx, id)
}

func (r *Recorder) publish(ctx context.Context, id string) { r.publishWith(ctx, id, nil) }

// publishWith sends `image.build` with the record as the API shows it (R5.72).
func (r *Recorder) publishWith(ctx context.Context, id string, _ *Progress) {
	if r.Events == nil {
		return
	}
	row, ok, err := r.Q.Get(ctx, id)
	if err != nil || !ok {
		return
	}
	b, err := json.Marshal(ToAPI(row, "", r.now(), false))
	if err != nil {
		return
	}
	r.Events.Publish(events.ImageBuild, b)
}

// List gives the builds, the running ones first, then the newest (R5.70).
// machineID narrows to one machine.
func (r *Recorder) List(ctx context.Context, machineID string, now time.Time) ([]gen.ImageBuild, error) {
	rows, err := r.Q.List(ctx, machineID, 200)
	if err != nil {
		return nil, err
	}
	out := make([]gen.ImageBuild, 0, len(rows))
	for _, row := range rows {
		out = append(out, ToAPI(row, "", now, false))
	}
	sortRunningFirst(out)
	return out, nil
}

// Get is one build with its log tail (R5.69).
func (r *Recorder) Get(ctx context.Context, id string, now time.Time) (gen.ImageBuild, bool, error) {
	row, ok, err := r.Q.Get(ctx, id)
	if err != nil || !ok {
		return gen.ImageBuild{}, false, err
	}
	return ToAPI(row, "", now, true), true, nil
}
