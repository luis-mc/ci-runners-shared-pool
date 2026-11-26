package notify

import (
	"context"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/store"
)

// ListWindow is how far back the notices list goes (R5.32, R6.5).
const ListWindow = 30 * 24 * time.Hour

// NoticeRow is one notice of the list with its deliveries (R5.32).
type NoticeRow struct {
	ID          string
	Type        string
	SubjectKind string
	SubjectID   string
	Subject     string
	State       string
	Tone        string
	Severity    string
	Summary     string
	Link        string
	Flaps       int
	At          time.Time
	Deliveries  []DeliveryRow
}

// DeliveryRow is the result of one delivery. It names the channel and its target (a host, an address), never a path or a secret.
type DeliveryRow struct {
	ID        string
	Channel   string
	Kind      string
	Target    string
	Status    string
	Attempt   int
	NextTry   *time.Time
	LastError string
	Count     int
}

// Notices returns the notices of the last 30 days, newest first (R5.32). An admin sees every delivery; another user
// sees only the deliveries of their own channels, because a webhook's name and host belong to the pool's admins.
func (s *Service) Notices(ctx context.Context, who Caller, limit int) ([]NoticeRow, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := s.cfg.DB.ListNotices(ctx, s.cfg.Now().Add(-ListWindow), limit)
	if err != nil {
		return nil, err
	}
	ids := make([]string, len(rows))
	for i, r := range rows {
		ids[i] = r.ID
	}
	var dels []store.NoticeDelivery
	if len(ids) > 0 {
		if dels, err = s.cfg.DB.ListDeliveriesForNotices(ctx, ids); err != nil {
			return nil, err
		}
	}
	by := map[string][]DeliveryRow{}
	for _, d := range dels {
		if !who.Admin && d.OwnerLogin != who.Login {
			continue
		}
		r := DeliveryRow{ID: d.ID, Channel: d.ChannelName, Kind: d.ChannelKind, Target: d.Target, Status: d.Status,
			Attempt: int(d.Attempt), LastError: d.LastError, Count: int(d.GroupCount)}
		if d.Status == store.DeliveryQueued {
			t := store.ParseTime(d.NextTryAt)
			r.NextTry = &t
		}
		by[d.NoticeID] = append(by[d.NoticeID], r)
	}
	out := make([]NoticeRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, NoticeRow{
			ID: r.ID, Type: r.Type, SubjectKind: r.SubjectKind, SubjectID: r.SubjectID, Subject: nameOr(r.SubjectName, r.SubjectID),
			State: r.State, Tone: r.Tone, Severity: r.Severity, Summary: r.Summary, Link: r.Link, Flaps: int(r.Flaps),
			At: store.ParseTime(r.CreatedAt), Deliveries: by[r.ID],
		})
	}
	return out, nil
}
