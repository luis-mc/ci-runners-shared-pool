package logs

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"
)

// SetupSteps are the runner setup steps the agent logs (R5.20), in the order a
// runner goes through them.
var SetupSteps = []string{"prepare image", "register", "start", "stop", "clean up", "deregister"}

// SetupComponent is the component of a runner setup line.
const SetupComponent = "runner-setup"

// SetupStep logs one runner setup step: Info when err is nil, Error with the
// error text otherwise. The line carries the runner id, so the controller can
// show it in the runner's detail and find it by search (R5.21).
func SetupStep(l *slog.Logger, runnerID, step string, err error) {
	if err != nil {
		l.Error("runner setup: "+step+" failed", "component", SetupComponent, "runner", runnerID, "step", step, "err", err.Error())
		return
	}
	l.Info("runner setup: "+step, "component", SetupComponent, "runner", runnerID, "step", step)
}

// AgentHandler turns every record it gets into a Line for sink. Put it after
// the redactor in the handler chain (Options.Extra), so a line is stored
// already redacted (R6.5). The attributes machine, runner, job and component
// fill the line's fields; the rest go to Attrs as JSON.
type AgentHandler struct {
	sink  func(Line)
	level slog.Leveler
	attrs []slog.Attr
}

// NewAgentHandler returns the handler; level nil means Info.
func NewAgentHandler(sink func(Line), level slog.Leveler) *AgentHandler {
	if level == nil {
		level = slog.LevelInfo
	}
	return &AgentHandler{sink: sink, level: level}
}

// Enabled implements slog.Handler.
func (h *AgentHandler) Enabled(_ context.Context, l slog.Level) bool { return l >= h.level.Level() }

// Handle implements slog.Handler.
func (h *AgentHandler) Handle(_ context.Context, r slog.Record) error {
	l := Line{At: r.Time, Level: r.Level, Message: r.Message}
	if l.At.IsZero() {
		l.At = time.Now()
	}
	rest := map[string]any{}
	put := func(a slog.Attr) {
		v := a.Value.Resolve().String()
		switch a.Key {
		case "machine":
			l.Machine = v
		case "runner":
			l.Runner = v
		case "job":
			l.Job = v
		case "component":
			l.Component = v
		case "version", "mode":
		default:
			rest[a.Key] = a.Value.Resolve().Any()
		}
	}
	for _, a := range h.attrs {
		put(a)
	}
	r.Attrs(func(a slog.Attr) bool { put(a); return true })
	if len(rest) > 0 {
		if b, err := json.Marshal(rest); err == nil {
			l.Attrs = string(b)
		}
	}
	h.sink(l)
	return nil
}

// WithAttrs implements slog.Handler.
func (h *AgentHandler) WithAttrs(a []slog.Attr) slog.Handler {
	c := *h
	c.attrs = append(append([]slog.Attr(nil), h.attrs...), a...)
	return &c
}

// WithGroup implements slog.Handler; groups are ignored, the keys stay flat.
func (h *AgentHandler) WithGroup(string) slog.Handler { return h }

// Forwarder keeps the agent's lines on disk (Buffer) and sends them to the
// controller in order whenever a link is attached (R5.20, R5.23). Lines go to
// disk first, so an outage or a crash loses nothing but what the cap drops.
type Forwarder struct {
	Buf *Buffer

	mu   sync.Mutex
	send func(ctx context.Context, lines []Line, dropped int64) error
	// OnError sees a failed disk write; the line is lost, the agent goes on (R6.9 applies to the agent too).
	OnError func(error)
}

// Add stores a line. It never returns an error to the caller.
func (f *Forwarder) Add(l Line) {
	if err := f.Buf.Append(l); err != nil && f.OnError != nil {
		f.OnError(err)
	}
}

// Attach sets the function that sends one batch over the agent link and
// returns the function that detaches it when the link closes.
func (f *Forwarder) Attach(send func(ctx context.Context, lines []Line, dropped int64) error) (detach func()) {
	f.mu.Lock()
	f.send = send
	f.mu.Unlock()
	return func() {
		f.mu.Lock()
		f.send = nil
		f.mu.Unlock()
	}
}

// Flush sends what is buffered. With no link attached it does nothing. The
// buffer is emptied only after the send returned no error.
func (f *Forwarder) Flush(ctx context.Context) error {
	f.mu.Lock()
	send := f.send
	f.mu.Unlock()
	if send == nil {
		return nil
	}
	lines, dropped, ack, err := f.Buf.Take(time.Now())
	if err != nil || len(lines) == 0 {
		return err
	}
	if err := send(ctx, lines, dropped); err != nil {
		return err
	}
	return ack()
}

// Run flushes every interval until ctx ends, with a last flush on the way out.
func (f *Forwarder) Run(ctx context.Context, every time.Duration) {
	if every <= 0 {
		every = 2 * time.Second
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			fctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_ = f.Flush(fctx)
			cancel()
			return
		case <-t.C:
			_ = f.Flush(ctx)
		}
	}
}
