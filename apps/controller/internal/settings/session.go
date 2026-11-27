package settings

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/audit"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/store"
)

// The stale-session wait (runners R5.67, handover lesson 29): how long after a
// restart GitHub may still refuse a runner's session without it being a fault.
// One minutes value in the settings table; API and screen come later.

const keySessionWait = "session_wait_minutes"

// DefaultSessionWaitMinutes is the default window.
const DefaultSessionWaitMinutes = 6

// SessionWaitRange is the allowed window in minutes.
var SessionWaitRange = Range{1, 30}

// GetSessionWait returns the window; a missing or out of range value gives the default.
func (s *Service) GetSessionWait(ctx context.Context) (time.Duration, error) {
	def := DefaultSessionWaitMinutes * time.Minute
	raw, ok, err := s.raw(ctx, keySessionWait)
	if err != nil || !ok {
		return def, err
	}
	n, perr := strconv.Atoi(strings.TrimSpace(raw))
	if perr != nil || !SessionWaitRange.has(n) {
		return def, nil
	}
	return time.Duration(n) * time.Minute, nil
}

// UpdateSessionWait stores the window in minutes and writes one audit row.
func (s *Service) UpdateSessionWait(ctx context.Context, actor audit.Actor, minutes int) error {
	target := audit.Target{Type: "settings", ID: "session_wait"}
	if !SessionWaitRange.has(minutes) {
		problems := []string{"session_wait_minutes must be between " + strconv.Itoa(SessionWaitRange.Min) + " and " + strconv.Itoa(SessionWaitRange.Max)}
		_ = audit.New(s.db).RecordRefused(ctx, actor, "settings.session_wait.update", target, map[string]any{"summary": problems[0]})
		return &RangeError{Problems: problems}
	}
	before, err := s.GetSessionWait(ctx)
	if err != nil {
		return err
	}
	if int(before/time.Minute) == minutes {
		return nil
	}
	return s.db.WithTx(ctx, func(tx *store.Tx) error {
		if err := tx.SetSetting(keySessionWait, strconv.Itoa(minutes), actor.Login); err != nil {
			return err
		}
		return audit.RecordTx(tx, actor, "settings.session_wait.update", target,
			map[string]any{"session_wait_minutes": strconv.Itoa(int(before/time.Minute)) + " -> " + strconv.Itoa(minutes)})
	})
}
