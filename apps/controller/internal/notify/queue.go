package notify

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/secrets"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/store"
)

// Delivery queue (notifications R5.14, R5.23, R5.24, R5.28, R6.3, R6.5, R6.9).

// Backoff is the wait after each failed try: about 30 s, 2 min, 10 min, 30 min, 1 h and 2 h (R5.23).
// A delivery that fails after the seventh try is marked failed.
var Backoff = []time.Duration{30 * time.Second, 2 * time.Minute, 10 * time.Minute, 30 * time.Minute, time.Hour, 2 * time.Hour}

const (
	// MaxRetryAfter is the longest Retry-After that is honoured (R5.23).
	MaxRetryAfter = time.Hour
	// WebhookPerMinute is the most deliveries one endpoint gets in a minute (R5.14).
	WebhookPerMinute = 60
	// QueueCeiling is the most queued deliveries the notifier holds (R6.9).
	QueueCeiling = 10000
	// Retention is how long notices and delivery records are kept (R6.5).
	Retention = 30 * 24 * time.Hour
	// FailingRun is how many failed deliveries in a row make an endpoint failing (R5.24).
	FailingRun = 3

	secretWait = time.Minute
	batchSize  = 50
)

// jitter spreads a backoff by up to 20 % either way.
func jitter(d time.Duration, r func() float64) time.Duration {
	return time.Duration(float64(d) * (0.8 + 0.4*r()))
}

// outcome is what one try did.
type outcome struct {
	ok         bool
	permanent  bool          // never try again (any other 4xx, a refused address)
	secretGone bool          // the secret store did not give the secret: wait, do not count (R6.3)
	retryAfter time.Duration // from a Retry-After header
	msg        string
}

// Tick runs one pass of the worker: it delivers what is due, summarises held notices, keeps the queue under
// its ceiling and prunes old rows. A pass never blocks on one slow receiver for longer than its own limit.
func (s *Service) Tick(ctx context.Context) {
	now := s.cfg.Now().UTC()
	due, err := s.cfg.DB.DueDeliveries(ctx, now, batchSize)
	if err != nil {
		s.cfg.Logger.Error("notify: read the queue", "err", err)
		return
	}
	for _, d := range due {
		if ctx.Err() != nil {
			return
		}
		s.deliver(ctx, d, now)
	}
	s.maintain(ctx, now)
}

// Source is one place that knows facts (section 3, step 1). Poll returns every fact of its scope that is true now.
type Source struct {
	Scope Scope
	Poll  func(ctx context.Context) ([]Fact, error)
}

// AddSource registers a source before Run starts.
func (s *Service) AddSource(src Source) {
	s.engMu.Lock()
	defer s.engMu.Unlock()
	s.sources = append(s.sources, src)
}

// PollEvery is how often the sources are read; a firing notice reaches the queue within this time (R6.1).
const PollEvery = 5 * time.Second

// PollSources reads every source once. A source that fails changes nothing: silence from a source is never
// taken as "all clear".
func (s *Service) PollSources(ctx context.Context) {
	s.engMu.Lock()
	srcs := append([]Source(nil), s.sources...)
	s.engMu.Unlock()
	for _, src := range srcs {
		facts, err := src.Poll(ctx)
		if err != nil {
			s.cfg.Logger.Warn("notify: source failed", "event", src.Scope.Type, "err", err)
			continue
		}
		if err := s.Observe(ctx, src.Scope, facts); err != nil {
			s.cfg.Logger.Error("notify: observe", "event", src.Scope.Type, "err", err)
		}
	}
}

// Run polls the sources every PollEvery and ticks the delivery worker every two seconds until ctx ends.
func (s *Service) Run(ctx context.Context) {
	tick := time.NewTicker(2 * time.Second)
	poll := time.NewTicker(PollEvery)
	defer tick.Stop()
	defer poll.Stop()
	s.PollSources(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-poll.C:
			s.PollSources(ctx)
		case <-tick.C:
			s.Tick(ctx)
		}
	}
}

func (s *Service) deliver(ctx context.Context, d store.NoticeDelivery, now time.Time) {
	c, err := s.cfg.DB.GetNotificationChannel(ctx, d.ChannelID)
	if errors.Is(err, store.ErrNotFound) {
		s.finish(ctx, d, store.DeliveryDropped, d.Attempt, now, "The channel was removed.", false)
		return
	}
	if err != nil {
		s.cfg.Logger.Error("notify: read channel", "channel", d.ChannelID, "err", err)
		return
	}
	if !c.Enabled {
		s.finish(ctx, d, store.DeliveryDropped, d.Attempt, now, "The channel is off.", false)
		return
	}
	n, err := s.cfg.DB.GetNotice(ctx, d.NoticeID)
	if err != nil {
		s.finish(ctx, d, store.DeliveryDropped, d.Attempt, now, "The notice is gone.", false)
		return
	}
	msg := s.compose(d, n)

	if c.Kind == KindDesktop {
		// The tray takes it from the controller (R5.15); the console keeps the notice either way (section 8).
		s.finish(ctx, d, store.DeliveryWaitingTray, d.Attempt, now, "", false)
		return
	}
	if c.Kind == KindWebhook {
		if cnt, err := s.cfg.DB.CountDeliveredSince(ctx, c.ID, now.Add(-time.Minute)); err == nil && cnt >= WebhookPerMinute {
			// Over 60 a minute: wait in the queue, never dropped (R5.14).
			_ = s.cfg.DB.SetDeliveryResult(ctx, d.ID, store.DeliveryQueued, d.Attempt, now.Add(5*time.Second), d.LastError, now, false)
			return
		}
	}
	var o outcome
	switch c.Kind {
	case KindWebhook:
		o = s.sendWebhook(ctx, c, d, msg)
	case KindEmail:
		o = s.sendEmail(ctx, c, d, msg)
	}
	s.record(ctx, c, d, o, now)
}

// record turns one try into the delivery's next state (R5.23).
func (s *Service) record(ctx context.Context, c store.NotificationChannel, d store.NoticeDelivery, o outcome, now time.Time) {
	attempt := d.Attempt + 1
	switch {
	case o.ok:
		s.finish(ctx, d, store.DeliveryDelivered, attempt, now, "", true)
	case o.secretGone:
		// Fail closed: it stays queued and raises a finding (R6.3). The try is not counted.
		_ = s.cfg.DB.SetDeliveryResult(ctx, d.ID, store.DeliveryQueued, d.Attempt, now.Add(secretWait), o.msg, now, false)
	case o.permanent || int(d.Attempt) >= len(Backoff):
		s.finish(ctx, d, store.DeliveryFailed, attempt, now, o.msg, false)
	default:
		wait := jitter(Backoff[d.Attempt], rand.Float64)
		if o.retryAfter > 0 {
			ra := min(o.retryAfter, MaxRetryAfter)
			wait = max(wait, ra)
		}
		_ = s.cfg.DB.SetDeliveryResult(ctx, d.ID, store.DeliveryQueued, attempt, now.Add(wait), o.msg, now, false)
	}
	s.checkEndpoint(ctx, c, o)
}

func (s *Service) finish(ctx context.Context, d store.NoticeDelivery, status string, attempt int64, now time.Time, lastErr string, delivered bool) {
	if err := s.cfg.DB.SetDeliveryResult(ctx, d.ID, status, attempt, now, lastErr, now, delivered); err != nil {
		s.cfg.Logger.Error("notify: record delivery", "delivery", d.ID, "err", err)
	}
}

// checkEndpoint raises a doctor.finding of severity warning for a webhook after three failed deliveries in a
// row, or when its secret cannot be read, and resolves it when one is delivered (R5.24, R6.3).
func (s *Service) checkEndpoint(ctx context.Context, c store.NotificationChannel, o outcome) {
	if c.Kind != KindWebhook {
		return
	}
	failing, why := false, ""
	switch {
	case o.secretGone:
		failing, why = true, "The secret store did not give the signing secret, so nothing is sent. "+o.msg
	case o.ok:
	default:
		last, err := s.cfg.DB.RecentChannelOutcomes(ctx, c.ID, FailingRun)
		if err == nil && len(last) == FailingRun {
			failing = true
			for _, st := range last {
				if st != store.DeliveryFailed {
					failing = false
				}
			}
			why = "The last " + fmt.Sprint(FailingRun) + " deliveries failed. " + o.msg
		}
	}
	f := Fact{
		Type: "doctor.finding", SubjectKind: "endpoint", SubjectID: c.ID, SubjectName: "Webhook " + c.Name,
		Severity: "warning", Summary: "Webhook " + c.Name + " is failing. " + jsonSafe(why), Link: "/settings/notifications",
	}
	if !failing && !o.ok {
		return
	}
	if err := s.Report(ctx, f, failing); err != nil {
		s.cfg.Logger.Error("notify: report endpoint", "channel", c.ID, "err", err)
	}
}

// maintain sends one summary for notices held by the hourly cap (R5.12), keeps the queue under its ceiling
// (R6.9) and prunes rows older than 30 days (R6.5).
func (s *Service) maintain(ctx context.Context, now time.Time) {
	held, err := s.cfg.DB.ListHeldChannels(ctx)
	if err != nil {
		s.cfg.Logger.Error("notify: held notices", "err", err)
	}
	for _, h := range held {
		if now.Sub(h.Oldest) < time.Hour {
			continue
		}
		if err := s.summarise(ctx, h, now); err != nil {
			s.cfg.Logger.Error("notify: summary", "channel", h.ChannelID, "err", err)
		}
	}
	s.mu.Lock()
	due := now.Sub(s.lastMaint) >= time.Minute
	if due {
		s.lastMaint = now
	}
	s.mu.Unlock()
	if !due {
		return
	}
	if n, err := s.cfg.DB.CountQueuedDeliveries(ctx); err == nil && n > QueueCeiling {
		dropped, err := s.cfg.DB.DropOldestNoteDeliveries(ctx, n-QueueCeiling, now)
		if err != nil {
			s.cfg.Logger.Error("notify: queue ceiling", "err", err)
		}
		if dropped > 0 {
			f := Fact{Type: "doctor.finding", SubjectKind: "notifier", SubjectID: "notify-queue", SubjectName: "The notice queue", Severity: "warning",
				Summary: fmt.Sprintf("The notice queue was full. %d of the oldest Note notices were dropped.", dropped)}
			_ = s.Report(ctx, f, true)
		}
	} else if err == nil {
		_ = s.Report(ctx, Fact{Type: "doctor.finding", SubjectKind: "notifier", SubjectID: "notify-queue", SubjectName: "The notice queue"}, false)
	}
	if _, err := s.cfg.DB.PruneNotices(ctx, now.Add(-Retention)); err != nil {
		s.cfg.Logger.Error("notify: prune", "err", err)
	}
}

// summarise replaces a channel's held notices with one summary notice (R5.12).
func (s *Service) summarise(ctx context.Context, h store.HeldChannel, now time.Time) error {
	stamp := store.FormatTime(now)
	n := store.Notice{
		ID: randomID("ntc_"), EventKey: TypeSummary + "|" + h.ChannelID + "|" + stamp, Type: TypeSummary, SubjectKind: "channel",
		SubjectID: h.ChannelID, SubjectName: h.ChannelName, State: StateFiring, Tone: ToneNote,
		Summary: heldSummary(h.Count), Link: "/notices", CreatedAt: stamp,
	}
	err := s.cfg.DB.WithTx(ctx, func(tx *store.Tx) error {
		if err := tx.InsertNotice(n); err != nil {
			return err
		}
		return tx.InsertDelivery(store.NoticeDelivery{
			ID: randomID("dlv_"), NoticeID: n.ID, ChannelID: h.ChannelID, ChannelKind: h.ChannelKind, ChannelName: h.ChannelName,
			OwnerLogin: h.OwnerLogin, Status: store.DeliveryQueued, NextTryAt: stamp, CreatedAt: stamp, UpdatedAt: stamp,
			Target: h.OwnerLogin,
		})
	})
	if err != nil {
		return err
	}
	return s.cfg.DB.MarkHeldGrouped(ctx, h.ChannelID, now)
}

// errNoSecret is returned by readSecret when the store answers but has no secret, or does not answer.
var errNoSecret = errors.New("no signing secret")

func (s *Service) readSecret(ctx context.Context, key secrets.Key) ([]byte, string, error) {
	b, err := s.cfg.Secrets.Get(ctx, key)
	if err == nil {
		return b, "", nil
	}
	if errors.Is(err, secrets.ErrNotFound) {
		return nil, "This webhook has no signing secret, so nothing was sent.", errNoSecret
	}
	return nil, "The secret store did not answer.", errNoSecret
}
