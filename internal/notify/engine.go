package notify

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/store"
)

// Defaults and ranges of the pool thresholds (R5.1, R5.8, R5.11, R5.12).
const (
	DefaultOfflineGrace = 2 * time.Minute
	DefaultJobWaiting   = 15 * time.Minute
	DefaultDiskPercent  = 10
	DefaultFlapWindow   = 10 * time.Minute
	DefaultHourlyCap    = 20
	CeilingHourlyCap    = 200
	// BurstCount notices of one type inside BurstWindow are sent as one grouped notice (R5.13).
	BurstCount  = 5
	BurstWindow = 60 * time.Second
	// DiskResolveMargin is how many points above the threshold free space must be for disk.low to resolve (R5.1).
	DiskResolveMargin = 2
)

// Thresholds are the pool-wide numbers of R5.1, R5.8, R5.11 and R5.12.
type Thresholds struct {
	OfflineGraceMinutes int `json:"offline_grace_minutes"`
	JobWaitingMinutes   int `json:"job_waiting_minutes"`
	DiskPercent         int `json:"disk_percent"`
	FlapWindowMinutes   int `json:"flap_window_minutes"`
	HourlyCap           int `json:"hourly_cap"`
}

// DefaultThresholds are the values of the spec.
func DefaultThresholds() Thresholds {
	return Thresholds{OfflineGraceMinutes: 2, JobWaitingMinutes: 15, DiskPercent: DefaultDiskPercent, FlapWindowMinutes: 10, HourlyCap: DefaultHourlyCap}
}

// Validate checks every value against its range (R5.1: grace 1 to 60 min, job 1 to 1440 min, disk 1 to 50 %; R5.12: cap 1 to 200).
func (t Thresholds) Validate() error {
	var p problems
	for _, r := range []struct {
		name      string
		v, lo, hi int
		unit      string
	}{
		{"The offline grace time", t.OfflineGraceMinutes, 1, 60, "minutes"},
		{"The job waiting time", t.JobWaitingMinutes, 1, 1440, "minutes"},
		{"The disk threshold", t.DiskPercent, 1, 50, "percent"},
		{"The flap window", t.FlapWindowMinutes, 1, 1440, "minutes"},
		{"The hourly cap", t.HourlyCap, 1, CeilingHourlyCap, "notices"},
	} {
		if r.v < r.lo || r.v > r.hi {
			p.add(fmt.Sprintf("%s must be %d to %d %s.", r.name, r.lo, r.hi, r.unit))
		}
	}
	return p.err()
}

// Fact is one thing a source says is true now (section 3, step 1).
type Fact struct {
	Type        string
	SubjectKind string // machine, job, runner, repository, endpoint, pool ...
	SubjectID   string
	SubjectName string
	// Severity is set for doctor findings (R5.4).
	Severity string
	// Summary is the plain-English line of the firing notice: "build-02 stopped answering at 14:30 UTC."
	Summary string
	Link    string
	// Since and Grace: the key fires only when Since+Grace has passed (R5.5). Zero means now.
	Since time.Time
	Grace time.Duration
}

func (f Fact) key() string { return f.Type + "|" + f.SubjectKind + "|" + f.SubjectID }

// Scope says which keys an Observe call owns: the keys of one type, of the given subject kinds
// (all kinds when Kinds is empty, minus ExceptKinds). Keys inside the scope that no fact names are resolved.
type Scope struct {
	Type        string
	Kinds       []string
	ExceptKinds []string
}

func (s Scope) has(kind string) bool {
	for _, k := range s.ExceptKinds {
		if k == kind {
			return false
		}
	}
	if len(s.Kinds) == 0 {
		return true
	}
	for _, k := range s.Kinds {
		if k == kind {
			return true
		}
	}
	return false
}

// Thresholds returns the pool thresholds: the stored ones, or the defaults.
func (s *Service) Thresholds(ctx context.Context) Thresholds {
	t := DefaultThresholds()
	if s.cfg.Thresholds != nil {
		t = s.cfg.Thresholds(ctx)
	}
	if t.Validate() != nil {
		return DefaultThresholds()
	}
	return t
}

// Observe tells the engine the complete set of facts of one scope. A fact whose key is new or resolved
// fires; a key in scope that is firing and has no fact resolves. Only a change of state makes a notice
// (R5.2, R5.3). The state change, the notice and its deliveries are written in one transaction (R6.4).
func (s *Service) Observe(ctx context.Context, sc Scope, facts []Fact) error {
	s.engMu.Lock()
	defer s.engMu.Unlock()
	now := s.cfg.Now().UTC()
	present := map[string]bool{}
	var errs []error
	for _, f := range facts {
		f.Type = sc.Type
		present[f.key()] = true
		if !f.Since.IsZero() && now.Before(f.Since.Add(f.Grace)) {
			// Inside the grace time (R5.5): not yet an event. If it already fires it stays so.
			if ev, ok, err := s.eventState(ctx, f.key()); err == nil && (!ok || ev.State != StateFiring) {
				continue
			}
		}
		if err := s.change(ctx, f, true, now); err != nil {
			errs = append(errs, err)
		}
	}
	rows, err := s.cfg.DB.ListNotifyEventsByType(ctx, sc.Type)
	if err != nil {
		return err
	}
	for _, ev := range rows {
		if ev.State != StateFiring || present[ev.Key] || !sc.has(ev.SubjectKind) {
			continue
		}
		f := Fact{Type: ev.Type, SubjectKind: ev.SubjectKind, SubjectID: ev.SubjectID, SubjectName: ev.SubjectName, Severity: ev.Severity, Link: ev.Link}
		if err := s.change(ctx, f, false, now); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Report sets one key firing or resolved. Use it for a source that knows a single subject, such as a webhook endpoint (R5.24).
func (s *Service) Report(ctx context.Context, f Fact, firing bool) error {
	s.engMu.Lock()
	defer s.engMu.Unlock()
	return s.change(ctx, f, firing, s.cfg.Now().UTC())
}

func (s *Service) eventState(ctx context.Context, key string) (store.NotifyEvent, bool, error) {
	var ev store.NotifyEvent
	var ok bool
	err := s.cfg.DB.WithTx(ctx, func(tx *store.Tx) error {
		var err error
		ev, ok, err = tx.GetNotifyEvent(key)
		return err
	})
	return ev, ok, err
}

// change applies one fact to its key. It makes a notice only when the state changes (R5.3), and none when the
// key fires again inside the flap window after it resolved (R5.11).
func (s *Service) change(ctx context.Context, f Fact, firing bool, now time.Time) error {
	chans, err := s.cfg.DB.ListNotificationChannels(ctx)
	if err != nil {
		return err
	}
	th := s.Thresholds(ctx)
	flap := time.Duration(th.FlapWindowMinutes) * time.Minute
	key := f.key()
	return s.cfg.DB.WithTx(ctx, func(tx *store.Tx) error {
		ev, had, err := tx.GetNotifyEvent(key)
		if err != nil {
			return err
		}
		state := StateResolved
		if firing {
			state = StateFiring
		}
		if had && ev.State == state {
			return nil // a repeat of the same state makes nothing (R5.3)
		}
		if !had && !firing {
			return nil
		}
		stamp := store.FormatTime(now)
		tone := toneFor(f.Type, f.Severity)
		next := store.NotifyEvent{
			Key: key, Type: f.Type, SubjectKind: f.SubjectKind, SubjectID: f.SubjectID, SubjectName: f.SubjectName,
			State: state, Tone: tone, Severity: f.Severity, Summary: f.Summary, Link: f.Link,
			FirstFiredAt: stamp, LastChangeAt: stamp,
		}
		if had {
			next.FirstFiredAt, next.Flaps, next.OpenNoticeID = ev.FirstFiredAt, ev.Flaps, ev.OpenNoticeID
			if f.Summary == "" {
				next.Summary = ev.Summary
			}
			if f.SubjectName == "" {
				next.SubjectName = ev.SubjectName
			}
			if f.Link == "" {
				next.Link = ev.Link
			}
		}
		if firing && had && now.Sub(store.ParseTime(ev.LastChangeAt)) < flap {
			// Flapping: the notice stays open and counts the flap (R5.11).
			next.Flaps++
			if next.OpenNoticeID != "" {
				if err := tx.SetNoticeFlaps(next.OpenNoticeID, next.Flaps); err != nil {
					return err
				}
			}
			return tx.PutNotifyEvent(next)
		}
		n := store.Notice{
			ID: randomID("ntc_"), EventKey: key, Type: f.Type, SubjectKind: f.SubjectKind, SubjectID: f.SubjectID,
			SubjectName: next.SubjectName, State: state, Tone: tone, Severity: f.Severity, Link: next.Link,
			Summary: next.Summary, CreatedAt: stamp,
		}
		if !firing {
			n.Tone = ToneDone
			n.Summary = resolvedSummary(f.Type, nameOr(next.SubjectName, f.SubjectID))
			next.Tone = ToneDone
		} else {
			next.OpenNoticeID = n.ID
			if n.Summary == "" {
				n.Summary = Title(f.Type, state) + ": " + nameOr(next.SubjectName, f.SubjectID) + "."
			}
		}
		if err := tx.InsertNotice(n); err != nil {
			return err
		}
		if err := tx.PutNotifyEvent(next); err != nil {
			return err
		}
		return s.fanOut(tx, n, chans, th, now)
	})
}

func nameOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// carries says whether a channel gets a notice (R5.4, R5.6, R5.7): it is on, names the event, and for a
// doctor finding the severity is at least the channel's lowest. A resolved notice goes where its firing one went.
func carries(c store.NotificationChannel, n store.Notice) bool {
	if !c.Enabled {
		return false
	}
	found := false
	for _, e := range c.Events {
		if e == n.Type {
			found = true
		}
	}
	if !found {
		return false
	}
	if n.Type == "doctor.finding" && n.Severity != "" && severityRank(n.Severity) < severityRank(c.MinSeverity) {
		return false
	}
	return true
}

// target is where a delivery goes, never a path or a token (R6.2): the host of a webhook, an email address, a login.
func target(c store.NotificationChannel) string {
	switch c.Kind {
	case KindWebhook:
		return hostOf(c.URL)
	case KindEmail:
		return c.Address
	}
	return c.OwnerLogin
}

// fanOut writes one delivery per channel that carries the notice, inside the caller's transaction (R6.4). Over
// the hourly cap a personal channel's delivery is held for one summary (R5.12). The fifth notice of one type
// inside 60 s starts one grouped delivery; later ones count into it (R5.13).
func (s *Service) fanOut(tx *store.Tx, n store.Notice, chans []store.NotificationChannel, th Thresholds, now time.Time) error {
	stamp := store.FormatTime(now)
	for _, c := range chans {
		if !carries(c, n) {
			continue
		}
		d := store.NoticeDelivery{
			ID: randomID("dlv_"), NoticeID: n.ID, ChannelID: c.ID, ChannelKind: c.Kind, ChannelName: c.Name,
			OwnerLogin: c.OwnerLogin, Target: target(c), Status: store.DeliveryQueued, NextTryAt: stamp,
			CreatedAt: stamp, UpdatedAt: stamp,
		}
		if c.Scope == ScopePersonal {
			sent, err := tx.CountChannelDeliveriesSince(c.ID, now.Add(-time.Hour))
			if err != nil {
				return err
			}
			if sent >= th.HourlyCap {
				d.Status = store.DeliveryHeld
				if err := tx.InsertDelivery(d); err != nil {
					return err
				}
				continue
			}
			gk := n.Type + "|" + n.State
			recent, err := tx.CountGroupKeySince(c.ID, gk, now.Add(-BurstWindow))
			if err != nil {
				return err
			}
			d.GroupKey = gk
			if recent >= BurstCount-1 {
				g, ok, err := tx.FindOpenGroup(c.ID, gk, now.Add(-BurstWindow))
				if err != nil {
					return err
				}
				if ok {
					if err := tx.BumpGroup(g.ID, now); err != nil {
						return err
					}
					d.Status = store.DeliveryGrouped
				} else {
					// The group goes out when the window ends, so it counts the whole burst.
					d.GroupCount = int64(BurstCount)
					d.NextTryAt = store.FormatTime(now.Add(BurstWindow))
				}
			}
		}
		if err := tx.InsertDelivery(d); err != nil {
			return err
		}
	}
	return nil
}

// jsonSafe keeps one line for logs and errors.
func jsonSafe(s string) string { return strings.TrimSpace(oneLine(s)) }
