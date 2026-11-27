package demand

import (
	"context"
	"log/slog"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/github"
)

// Failover is a Demand source made of two: the scale set (Primary) and the
// REST poller (Fallback). The scale-set client is a preview API, so it reaches
// the rest of the code only through github.Demand (github-integration R5.40).
// When the primary cannot open, drops, or answers in a way it does not know,
// Failover runs the fallback and says so in the log; after a back-off it tries
// the primary again. Exactly one of the two runs at a time, so a job is never
// offered by both.
type Failover struct {
	Primary  github.Demand
	Fallback github.Demand
	Logger   *slog.Logger
	// Why gives the reason the primary ended, when it can (ScaleSetDemand.Err).
	Why func() error
	// Backoff starts at MinBackoff (default 30 s) and doubles up to MaxBackoff
	// (default 10 minutes).
	MinBackoff, MaxBackoff time.Duration
	// OnState is told "scaleset" or "polling" at every change, for the doctor.
	OnState func(source string, why string)
	// Sleep is a test seam.
	Sleep func(ctx context.Context, d time.Duration) error
}

var _ github.Demand = (*Failover)(nil)

func (f *Failover) log() *slog.Logger {
	if f.Logger != nil {
		return f.Logger
	}
	return slog.Default()
}

func (f *Failover) state(source, why string) {
	if f.OnState != nil {
		f.OnState(source, why)
	}
}

func (f *Failover) sleep(ctx context.Context, d time.Duration) error {
	if f.Sleep != nil {
		return f.Sleep(ctx, d)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Jobs implements github.Demand.
func (f *Failover) Jobs(ctx context.Context) (<-chan github.Job, error) {
	out := make(chan github.Job)
	go func() {
		defer close(out)
		lo, hi := f.MinBackoff, f.MaxBackoff
		if lo <= 0 {
			lo = 30 * time.Second
		}
		if hi < lo {
			hi = 10 * time.Minute
		}
		back := lo
		for ctx.Err() == nil {
			why := f.runPrimary(ctx, out)
			if ctx.Err() != nil {
				return
			}
			f.log().Warn("scale-set demand is down; polling instead", "why", why, "retry_in", back.String())
			f.state("polling", why)
			f.runFallback(ctx, out, back)
			if back *= 2; back > hi {
				back = hi
			}
		}
	}()
	return out, nil
}

// runPrimary forwards the scale set until it ends. It returns why it ended.
func (f *Failover) runPrimary(ctx context.Context, out chan<- github.Job) string {
	pctx, cancel := context.WithCancel(ctx)
	defer cancel()
	ch, err := f.Primary.Jobs(pctx)
	if err != nil {
		return err.Error()
	}
	f.state("scaleset", "")
	if !f.forward(ctx, ch, out) {
		return ""
	}
	if f.Why != nil {
		if err := f.Why(); err != nil {
			return err.Error()
		}
	}
	return "the session ended"
}

// runFallback forwards the poller for one back-off period, then stops it.
func (f *Failover) runFallback(ctx context.Context, out chan<- github.Job, d time.Duration) {
	fctx, cancel := context.WithCancel(ctx)
	defer cancel()
	ch, err := f.Fallback.Jobs(fctx)
	if err != nil {
		f.log().Error("start the polling fallback", "err", err)
		_ = f.sleep(ctx, d)
		return
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		f.forward(fctx, ch, out)
	}()
	_ = f.sleep(ctx, d)
	cancel()
	<-done
}

// forward copies jobs until the source closes (returns true) or ctx ends
// (returns false).
func (f *Failover) forward(ctx context.Context, in <-chan github.Job, out chan<- github.Job) bool {
	for {
		select {
		case <-ctx.Done():
			return false
		case j, ok := <-in:
			if !ok {
				return true
			}
			select {
			case out <- j:
			case <-ctx.Done():
				return false
			}
		}
	}
}
