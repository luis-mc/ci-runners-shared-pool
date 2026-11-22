package machinehealth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// KeyThresholds is the settings row of the Doctor thresholds.
const KeyThresholds = "health_thresholds"

// Thresholds are the limits the Doctor's machine health checks use. They are
// editable settings (doctor R5.66); the defaults are the owner's decision.
type Thresholds struct {
	// DiskDaysUnder: a volume that fills in fewer days than this is a finding.
	DiskDaysUnder int `json:"disk_days_under"`
	// DiskFreePctUnder: a volume with less free space than this percent is a finding.
	DiskFreePctUnder int `json:"disk_free_pct_under"`
	// NoHeartbeatSeconds: no heartbeat for this long is a finding.
	NoHeartbeatSeconds int `json:"no_heartbeat_seconds"`
	// ClockSkewSeconds: a clock that differs from the controller's by more than this is a finding.
	ClockSkewSeconds int `json:"clock_skew_seconds"`
	// OOMWindowMinutes: an OOM kill this recent is a finding.
	OOMWindowMinutes int `json:"oom_window_minutes"`
}

// DefaultThresholds are the owner's numbers: under 3 days or 10 % free, no
// heartbeat for 2 minutes, skew over 30 seconds, an OOM kill in the last hour.
var DefaultThresholds = Thresholds{DiskDaysUnder: 3, DiskFreePctUnder: 10, NoHeartbeatSeconds: 120, ClockSkewSeconds: 30, OOMWindowMinutes: 60}

// ErrBadThreshold is returned for a value out of range.
var ErrBadThreshold = errors.New("a health threshold is out of range")

// Validate checks every value against its range.
func (t Thresholds) Validate() error {
	for _, r := range []struct {
		name      string
		v, lo, hi int
	}{
		{"disk_days_under", t.DiskDaysUnder, 1, 365}, {"disk_free_pct_under", t.DiskFreePctUnder, 1, 90},
		{"no_heartbeat_seconds", t.NoHeartbeatSeconds, 30, 3600}, {"clock_skew_seconds", t.ClockSkewSeconds, 1, 3600},
		{"oom_window_minutes", t.OOMWindowMinutes, 1, 1440},
	} {
		if r.v < r.lo || r.v > r.hi {
			return fmt.Errorf("%w: %s must be %d to %d", ErrBadThreshold, r.name, r.lo, r.hi)
		}
	}
	return nil
}

// Thresholds reads the stored thresholds; a missing or bad row gives the defaults.
func (s *Service) Thresholds(ctx context.Context) (Thresholds, error) {
	raw, ok, err := s.Store.RawSetting(ctx, KeyThresholds)
	if err != nil {
		return DefaultThresholds, err
	}
	t := DefaultThresholds
	if ok {
		if json.Unmarshal([]byte(raw), &t) != nil || t.Validate() != nil {
			return DefaultThresholds, nil
		}
	}
	return t, nil
}

// SetThresholds validates and stores the thresholds.
func (s *Service) SetThresholds(ctx context.Context, t Thresholds, by string) error {
	if err := t.Validate(); err != nil {
		return err
	}
	b, _ := json.Marshal(t)
	return s.Store.PutRawSetting(ctx, KeyThresholds, string(b), by)
}
