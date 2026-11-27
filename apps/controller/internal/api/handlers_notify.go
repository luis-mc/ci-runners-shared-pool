package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api/gen"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/audit"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/auth"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/notify"
)

// Handlers of the notification channels (notifications R5.7 to R5.9, R5.19,
// R5.29 to R5.31; web-console 4.9). A secret is write-only: it goes to the OS
// secret store inside the service and never comes back.

// NotifyService is what the notification routes need. *notify.Service implements it.
type NotifyService interface {
	List(ctx context.Context, who notify.Caller) ([]notify.Channel, error)
	Create(ctx context.Context, who notify.Caller, in notify.Input) (notify.Channel, error)
	Update(ctx context.Context, who notify.Caller, id string, p notify.Patch) (notify.Channel, error)
	Delete(ctx context.Context, who notify.Caller, id string, confirm bool) error
	Test(ctx context.Context, who notify.Caller, id string) (notify.TestResult, error)
	// CRSP258: real notices and the pool thresholds.
	Notices(ctx context.Context, who notify.Caller, limit int) ([]notify.NoticeRow, error)
	Thresholds(ctx context.Context) notify.Thresholds
	SetThresholds(ctx context.Context, who notify.Caller, t notify.Thresholds) (notify.Thresholds, error)
}

var _ NotifyService = (*notify.Service)(nil)

// notifyCaller says who asks, from the signed-in principal.
func notifyCaller(ctx context.Context) (notify.Caller, bool) {
	p, ok := PrincipalFrom(ctx)
	if !ok {
		return notify.Caller{}, false
	}
	return notify.Caller{
		Login: p.Login,
		Admin: p.Role.AtLeast(auth.RoleAdmin),
		Actor: audit.Actor{Login: p.Login, ID: strconv.FormatInt(p.UserID, 10), Source: p.Source},
	}, true
}

// notifyFail maps a notification error to the error envelope.
func (s *server) notifyFail(err error) opsFailure {
	var inv *notify.InvalidError
	var nc *notify.NeedsConfirmation
	switch {
	case errors.As(err, &inv):
		details := make([]gen.ErrorDetail, 0, len(inv.Problems))
		for _, p := range inv.Problems {
			details = append(details, gen.ErrorDetail{Kind: "field", Text: p})
		}
		return envelope(http.StatusBadRequest, CodeBadRequest, inv.Error(), details...)
	case errors.As(err, &nc):
		text := "Channel " + nc.Name + " stops carrying its events."
		if len(nc.Events) > 0 {
			text = "Channel " + nc.Name + " stops carrying " + joinWords(nc.Events) + "."
		}
		return envelope(http.StatusConflict, CodeNeedsConfirmation, "removing this channel needs a yes; it names what stops",
			gen.ErrorDetail{Kind: "item", Text: text})
	case errors.Is(err, notify.ErrForbidden):
		return envelope(http.StatusForbidden, CodeForbidden, "only an admin or the owner may change a webhook")
	case errors.Is(err, notify.ErrNotFound):
		return envelope(http.StatusNotFound, CodeNotFound, "no such notification channel")
	case errors.Is(err, notify.ErrTooSoon):
		return envelope(http.StatusTooManyRequests, CodeRateLimited, "a test of this channel was sent a moment ago; wait 10 seconds")
	}
	// The error text is not shown: it can name a secret store path.
	s.opts.Logger.Error("notification request failed", "err", err)
	return envelope(http.StatusInternalServerError, CodeInternal, "internal error")
}

func joinWords(items []string) string {
	out := ""
	for i, it := range items {
		switch {
		case i == 0:
		case i == len(items)-1:
			out += " and "
		default:
			out += ", "
		}
		out += it
	}
	return out
}

func notifyUnwired() opsFailure { return unavailable() }

func notifyUnsigned() opsFailure {
	return envelope(http.StatusUnauthorized, CodeUnauthenticated, "sign in first")
}

func toGenChannel(c notify.Channel) gen.NotificationChannel {
	out := gen.NotificationChannel{
		Id: c.ID, Kind: gen.NotificationKind(c.Kind), Name: c.Name, Scope: gen.NotificationChannelScope(c.Scope),
		Enabled: c.Enabled, MinSeverity: gen.NotificationSeverity(c.MinSeverity), SecretSet: c.SecretSet,
		Events: make([]gen.NotificationEvent, 0, len(c.Events)),
	}
	for _, e := range c.Events {
		out.Events = append(out.Events, gen.NotificationEvent(e))
	}
	if c.Address != "" {
		a := c.Address
		out.Address = &a
	}
	if c.URL != "" {
		u := c.URL
		out.Url = &u
	}
	if c.SMTP != nil {
		sm := &gen.SmtpSettings{Host: c.SMTP.Host, Port: c.SMTP.Port, HasPassword: c.SMTP.HasPassword}
		if c.SMTP.Username != "" {
			u := c.SMTP.Username
			sm.Username = &u
		}
		if c.SMTP.From != "" {
			f := c.SMTP.From
			sm.From = &f
		}
		out.Smtp = sm
	}
	out.LastTestAt, out.LastTestOk = c.LastTestAt, c.LastTestOK
	return out
}

func notifyStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// ListNotificationChannels implements GET /api/v1/notification-channels.
func (s *server) ListNotificationChannels(ctx context.Context, _ gen.ListNotificationChannelsRequestObject) (gen.ListNotificationChannelsResponseObject, error) {
	fail := func(f opsFailure) (gen.ListNotificationChannelsResponseObject, error) {
		return gen.ListNotificationChannelsdefaultJSONResponse{StatusCode: f.status, Body: f.body}, nil
	}
	if s.opts.Notify == nil {
		return fail(notifyUnwired())
	}
	who, ok := notifyCaller(ctx)
	if !ok {
		return fail(notifyUnsigned())
	}
	list, err := s.opts.Notify.List(ctx, who)
	if err != nil {
		return fail(s.notifyFail(err))
	}
	out := gen.NotificationChannelList{Items: make([]gen.NotificationChannel, 0, len(list))}
	for _, c := range list {
		out.Items = append(out.Items, toGenChannel(c))
	}
	return gen.ListNotificationChannels200JSONResponse(out), nil
}

// CreateNotificationChannel implements POST /api/v1/notification-channels.
func (s *server) CreateNotificationChannel(ctx context.Context, req gen.CreateNotificationChannelRequestObject) (gen.CreateNotificationChannelResponseObject, error) {
	fail := func(f opsFailure) (gen.CreateNotificationChannelResponseObject, error) {
		return gen.CreateNotificationChanneldefaultJSONResponse{StatusCode: f.status, Body: f.body}, nil
	}
	if s.opts.Notify == nil {
		return fail(notifyUnwired())
	}
	who, ok := notifyCaller(ctx)
	if !ok {
		return fail(notifyUnsigned())
	}
	if req.Body == nil {
		return fail(envelope(http.StatusBadRequest, CodeBadRequest, "the request is not valid"))
	}
	b := req.Body
	in := notify.Input{
		Kind: string(b.Kind), Name: b.Name, Enabled: b.Enabled, Address: notifyStr(b.Address), URL: notifyStr(b.Url), Secret: notifyStr(b.Secret),
	}
	for _, e := range b.Events {
		in.Events = append(in.Events, string(e))
	}
	if b.MinSeverity != nil {
		in.MinSeverity = string(*b.MinSeverity)
	}
	if b.Smtp != nil {
		in.SMTP = &notify.SMTPInput{Host: b.Smtp.Host, Port: b.Smtp.Port, Username: notifyStr(b.Smtp.Username), From: notifyStr(b.Smtp.From), Password: notifyStr(b.Smtp.Password)}
	}
	c, err := s.opts.Notify.Create(ctx, who, in)
	if err != nil {
		return fail(s.notifyFail(err))
	}
	return gen.CreateNotificationChannel201JSONResponse(toGenChannel(c)), nil
}

// UpdateNotificationChannel implements PATCH /api/v1/notification-channels/{channelId}.
func (s *server) UpdateNotificationChannel(ctx context.Context, req gen.UpdateNotificationChannelRequestObject) (gen.UpdateNotificationChannelResponseObject, error) {
	fail := func(f opsFailure) (gen.UpdateNotificationChannelResponseObject, error) {
		return gen.UpdateNotificationChanneldefaultJSONResponse{StatusCode: f.status, Body: f.body}, nil
	}
	if s.opts.Notify == nil {
		return fail(notifyUnwired())
	}
	who, ok := notifyCaller(ctx)
	if !ok {
		return fail(notifyUnsigned())
	}
	if req.Body == nil {
		return fail(envelope(http.StatusBadRequest, CodeBadRequest, "the request is not valid"))
	}
	b := req.Body
	p := notify.Patch{Name: b.Name, Enabled: b.Enabled, Address: b.Address, URL: b.Url, Secret: b.Secret}
	if b.Events != nil {
		ev := make([]string, 0, len(*b.Events))
		for _, e := range *b.Events {
			ev = append(ev, string(e))
		}
		p.Events = &ev
	}
	if b.MinSeverity != nil {
		v := string(*b.MinSeverity)
		p.MinSeverity = &v
	}
	if b.Smtp != nil {
		p.SMTP = &notify.SMTPPatch{Host: b.Smtp.Host, Port: b.Smtp.Port, Username: b.Smtp.Username, From: b.Smtp.From, Password: b.Smtp.Password}
	}
	c, err := s.opts.Notify.Update(ctx, who, req.ChannelId, p)
	if err != nil {
		return fail(s.notifyFail(err))
	}
	return gen.UpdateNotificationChannel200JSONResponse(toGenChannel(c)), nil
}

// DeleteNotificationChannel implements DELETE /api/v1/notification-channels/{channelId}.
func (s *server) DeleteNotificationChannel(ctx context.Context, req gen.DeleteNotificationChannelRequestObject) (gen.DeleteNotificationChannelResponseObject, error) {
	fail := func(f opsFailure) (gen.DeleteNotificationChannelResponseObject, error) {
		return gen.DeleteNotificationChanneldefaultJSONResponse{StatusCode: f.status, Body: f.body}, nil
	}
	if s.opts.Notify == nil {
		return fail(notifyUnwired())
	}
	who, ok := notifyCaller(ctx)
	if !ok {
		return fail(notifyUnsigned())
	}
	confirm := req.Params.Confirm != nil && *req.Params.Confirm
	if err := s.opts.Notify.Delete(ctx, who, req.ChannelId, confirm); err != nil {
		return fail(s.notifyFail(err))
	}
	return gen.DeleteNotificationChannel204Response{}, nil
}

// TestNotificationChannel implements POST /api/v1/notification-channels/{channelId}/test.
func (s *server) TestNotificationChannel(ctx context.Context, req gen.TestNotificationChannelRequestObject) (gen.TestNotificationChannelResponseObject, error) {
	fail := func(f opsFailure) (gen.TestNotificationChannelResponseObject, error) {
		return gen.TestNotificationChanneldefaultJSONResponse{StatusCode: f.status, Body: f.body}, nil
	}
	if s.opts.Notify == nil {
		return fail(notifyUnwired())
	}
	who, ok := notifyCaller(ctx)
	if !ok {
		return fail(notifyUnsigned())
	}
	res, err := s.opts.Notify.Test(ctx, who, req.ChannelId)
	if err != nil {
		return fail(s.notifyFail(err))
	}
	out := gen.NotificationTestResult{Ok: res.OK, SentAt: res.SentAt}
	if res.Message != "" {
		m := res.Message
		out.Message = &m
	}
	return gen.TestNotificationChannel200JSONResponse(out), nil
}

func toGenThresholds(t notify.Thresholds) gen.NotificationThresholds {
	return gen.NotificationThresholds{
		OfflineGraceMinutes: t.OfflineGraceMinutes, JobWaitingMinutes: t.JobWaitingMinutes, DiskPercent: t.DiskPercent,
		FlapWindowMinutes: t.FlapWindowMinutes, HourlyCap: t.HourlyCap,
	}
}

// ListNotices implements GET /api/v1/notices (notifications R5.32).
func (s *server) ListNotices(ctx context.Context, req gen.ListNoticesRequestObject) (gen.ListNoticesResponseObject, error) {
	fail := func(f opsFailure) (gen.ListNoticesResponseObject, error) {
		return gen.ListNoticesdefaultJSONResponse{StatusCode: f.status, Body: f.body}, nil
	}
	if s.opts.Notify == nil {
		return fail(notifyUnwired())
	}
	who, ok := notifyCaller(ctx)
	if !ok {
		return fail(notifyUnsigned())
	}
	limit := 0
	if req.Params.Limit != nil {
		limit = *req.Params.Limit
	}
	rows, err := s.opts.Notify.Notices(ctx, who, limit)
	if err != nil {
		return fail(s.notifyFail(err))
	}
	out := gen.NoticeList{Items: make([]gen.Notice, 0, len(rows))}
	for _, r := range rows {
		n := gen.Notice{
			Id: r.ID, Type: r.Type, Subject: r.Subject, State: gen.NoticeState(r.State), Tone: gen.NoticeTone(r.Tone),
			Summary: r.Summary, At: r.At, Flaps: r.Flaps, Deliveries: make([]gen.NoticeDelivery, 0, len(r.Deliveries)),
		}
		if r.SubjectKind != "" {
			k := r.SubjectKind
			n.SubjectKind = &k
		}
		if r.SubjectID != "" {
			id := r.SubjectID
			n.SubjectId = &id
		}
		if r.Severity != "" {
			sv := r.Severity
			n.Severity = &sv
		}
		if r.Link != "" {
			l := r.Link
			n.Link = &l
		}
		for _, d := range r.Deliveries {
			gd := gen.NoticeDelivery{Id: d.ID, Channel: d.Channel, Kind: gen.NotificationKind(d.Kind), Target: d.Target,
				Status: gen.NoticeDeliveryStatus(d.Status), Attempt: d.Attempt, NextTryAt: d.NextTry}
			if d.LastError != "" {
				e := d.LastError
				gd.LastError = &e
			}
			if d.Count > 0 {
				c := d.Count
				gd.Count = &c
			}
			n.Deliveries = append(n.Deliveries, gd)
		}
		out.Items = append(out.Items, n)
	}
	return gen.ListNotices200JSONResponse(out), nil
}

// GetNotificationThresholds implements GET /api/v1/notification-thresholds (R5.8).
func (s *server) GetNotificationThresholds(ctx context.Context, _ gen.GetNotificationThresholdsRequestObject) (gen.GetNotificationThresholdsResponseObject, error) {
	if s.opts.Notify == nil {
		f := notifyUnwired()
		return gen.GetNotificationThresholdsdefaultJSONResponse{StatusCode: f.status, Body: f.body}, nil
	}
	return gen.GetNotificationThresholds200JSONResponse(toGenThresholds(s.opts.Notify.Thresholds(ctx))), nil
}

// SetNotificationThresholds implements PUT /api/v1/notification-thresholds (R5.8, R6.6).
func (s *server) SetNotificationThresholds(ctx context.Context, req gen.SetNotificationThresholdsRequestObject) (gen.SetNotificationThresholdsResponseObject, error) {
	fail := func(f opsFailure) (gen.SetNotificationThresholdsResponseObject, error) {
		return gen.SetNotificationThresholdsdefaultJSONResponse{StatusCode: f.status, Body: f.body}, nil
	}
	if s.opts.Notify == nil {
		return fail(notifyUnwired())
	}
	who, ok := notifyCaller(ctx)
	if !ok {
		return fail(notifyUnsigned())
	}
	if req.Body == nil {
		return fail(envelope(http.StatusBadRequest, CodeBadRequest, "the request is not valid"))
	}
	b := req.Body
	got, err := s.opts.Notify.SetThresholds(ctx, who, notify.Thresholds{
		OfflineGraceMinutes: b.OfflineGraceMinutes, JobWaitingMinutes: b.JobWaitingMinutes, DiskPercent: b.DiskPercent,
		FlapWindowMinutes: b.FlapWindowMinutes, HourlyCap: b.HourlyCap,
	})
	if err != nil {
		return fail(s.notifyFail(err))
	}
	return gen.SetNotificationThresholds200JSONResponse(toGenThresholds(got)), nil
}
