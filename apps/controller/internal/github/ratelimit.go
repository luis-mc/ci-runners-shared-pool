package github

import (
	"context"
	"net/http"
	"strconv"
	"time"
)

// budget tracks one installation's REST limit (R6.6).
type budget struct {
	limit     int
	remaining int
	reset     time.Time
}

type ctxKey int

const backgroundKey ctxKey = 1

// Background marks a context as background work (polling, reconcile, variable
// and runs-on checks). Background calls stop when half of the installation's
// hourly limit is used (R6.6); user-driven calls are never held back.
func Background(ctx context.Context) context.Context {
	return context.WithValue(ctx, backgroundKey, true)
}

func isBackground(ctx context.Context) bool {
	v, _ := ctx.Value(backgroundKey).(bool)
	return v
}

func (c *REST) noteBudget(installation int64, h http.Header) {
	if installation == 0 {
		return
	}
	lim, err1 := strconv.Atoi(h.Get("X-RateLimit-Limit"))
	rem, err2 := strconv.Atoi(h.Get("X-RateLimit-Remaining"))
	reset, err3 := strconv.ParseInt(h.Get("X-RateLimit-Reset"), 10, 64)
	if err1 != nil || err2 != nil || err3 != nil {
		return
	}
	c.mu.Lock()
	c.budgets[installation] = &budget{limit: lim, remaining: rem, reset: time.Unix(reset, 0)}
	c.mu.Unlock()
}

// checkBudget refuses a background call once more than half of the limit is
// used, until the reset time. It returns a RateLimitError the console shows.
func (c *REST) checkBudget(ctx context.Context, installation int64) error {
	if installation == 0 || !isBackground(ctx) {
		return nil
	}
	c.mu.Lock()
	b := c.budgets[installation]
	c.mu.Unlock()
	if b == nil || !c.cfg.Now().Before(b.reset) {
		return nil
	}
	if b.remaining < b.limit/2 {
		return &RateLimitError{ResetAt: b.reset}
	}
	return nil
}
