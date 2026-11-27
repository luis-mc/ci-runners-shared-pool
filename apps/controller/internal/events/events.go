// Package events is the live event hub seam (observability R5.1 to R5.6, R6.1
// to R6.4; web-console R5.6, R5.7).
//
// Producers (scheduler, machines, runners, doctor, plans) publish after their
// transaction commits, so a client never sees a change that was rolled back.
// The hub numbers events from one counter, keeps the last 2,000 events or 10
// minutes for replay, and closes a subscriber that falls 500 events behind;
// a slow subscriber never blocks a publisher (R6.3). The SSE handler of
// GET /api/v1/events (operation streamEvents) reads a Subscription. Event
// payloads are the LiveEvent schema of packages/contract/openapi.yaml.
package events

import (
	"context"
	"errors"
	"time"
)

// Type is the event type, the SSE `event:` field (observability 7, live stream).
type Type string

// Event types of the M1 live view.
const (
	Snapshot       Type = "snapshot"
	JobQueued      Type = "job.queued"
	JobWaiting     Type = "job.waiting"
	JobStarted     Type = "job.started"
	JobFinished    Type = "job.finished"
	MachineLoad    Type = "machine.load"
	MachineState   Type = "machine.state"
	RunnerState    Type = "runner.state"
	SavedToday     Type = "saved.today"
	DoctorProgress Type = "doctor.progress"
	PlanProgress   Type = "plan.progress"
	ChangeProgress Type = "change.progress"
	// ImageBuild is a build record that changed (registry R5.72, IMG1).
	ImageBuild Type = "image.build"
)

// ErrTooManyStreams is returned by Subscribe past the stream cap (default 50,
// ceiling 200; R6.3).
var ErrTooManyStreams = errors.New("too many open event streams")

// Event is one published change. ID is assigned by the hub and increases.
type Event struct {
	ID   uint64
	Type Type
	At   time.Time
	// Data is the JSON of the LiveEvent field that matches Type. It has been
	// through the redactor (R6.5).
	Data []byte
	// ReplayGap marks a snapshot sent because the missed events were no longer
	// kept (R5.3).
	ReplayGap bool
}

// Publisher is what producers depend on.
type Publisher interface {
	// Publish hands an event to the hub. It never blocks on a subscriber. At
	// most one machine.load per machine per 5 seconds is kept (R6.2).
	Publish(t Type, data []byte)
}

// Subscription is one open stream.
type Subscription interface {
	// Events delivers events in order. It is closed when the subscriber falls
	// too far behind, the hub stops, or Close is called.
	Events() <-chan Event
	// Close ends the subscription.
	Close()
}

// Hub fans events out to subscribers and replays missed ones.
type Hub interface {
	Publisher
	// Subscribe opens a stream. With lastEventID 0 the first event is a full
	// snapshot. Otherwise the missed events follow in order, or a snapshot
	// marked ReplayGap when they are no longer kept (R5.3). The stream sends
	// nothing before the caller has checked the session (R6.4).
	Subscribe(ctx context.Context, lastEventID uint64) (Subscription, error)
}

// Snapshotter builds the full snapshot the hub sends first; the observability
// feature implements it over the store.
type Snapshotter interface {
	Snapshot(ctx context.Context) ([]byte, error)
}
