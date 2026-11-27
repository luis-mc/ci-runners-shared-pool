package assign

import (
	"context"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/github"
)

// Events receives the workflow_job webhooks (github.JobDetailEvents) and hands
// each one to the controller on one worker, so the webhook returns at once and
// the controller still sees the events one at a time.
type Events struct {
	C       *Controller
	OnError func(error)
	queue   chan github.JobEvent
}

// NewEvents makes the receiver; Run must be started for it to work.
func NewEvents(c *Controller, buffer int) *Events {
	if buffer < 1 {
		buffer = 256
	}
	return &Events{C: c, queue: make(chan github.JobEvent, buffer)}
}

// JobDetail implements github.JobDetailEvents. A full queue drops the event:
// the poller finds the job within a minute (runners R5.115).
func (e *Events) JobDetail(ev github.JobEvent) {
	select {
	case e.queue <- ev:
	default:
	}
}

// Run handles the queued events until ctx ends.
func (e *Events) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-e.queue:
			if err := e.Handle(ctx, ev); err != nil && e.OnError != nil {
				e.OnError(err)
			}
		}
	}
}

// Handle sends one event to the controller by its action.
func (e *Events) Handle(ctx context.Context, ev github.JobEvent) error {
	j := ev.Job
	switch ev.Action {
	case "queued":
		j.Status = "queued"
		return e.C.Queued(ctx, j)
	case "in_progress":
		j.Status = "in_progress"
		return e.C.InProgress(ctx, j)
	case "completed":
		j.Status = "completed"
		return e.C.Completed(ctx, j)
	}
	return nil
}
