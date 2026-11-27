package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api/gen"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/audit"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backup"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/repos"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/settings"
)

// Handlers of the operations area (CRSP18): settings (observability R5.32),
// the audit log (R5.24 to R5.27) and backups (operations R5.21 to R5.27).
// Role checks (viewer, admin, owner) belong to the auth layer; the actor it
// puts in the context with audit.WithActor is the one audited here.

// opsFailure is an error already mapped to the API envelope.
type opsFailure struct {
	status int
	body   gen.ErrorEnvelope
}

func envelope(status int, code, msg string, details ...gen.ErrorDetail) opsFailure {
	f := opsFailure{status: status, body: gen.ErrorEnvelope{Error: gen.ErrorBody{Code: code, Message: msg}}}
	if len(details) > 0 {
		f.body.Error.Details = &details
	}
	return f
}

// unavailable answers a route whose service the controller did not start. It
// is the same answer an unbuilt operation gives, so a server without the
// operations area behaves like one that has not built it yet.
func unavailable() opsFailure {
	return opsFailure{status: http.StatusNotImplemented, body: notImplemented()}
}

// opsError maps an error of the operations services to the API envelope.
func (s *server) opsError(err error) opsFailure {
	var rng *settings.RangeError
	var nc *backup.NeedsConfirmation
	switch {
	case errors.As(err, &rng):
		details := make([]gen.ErrorDetail, 0, len(rng.Problems))
		for _, p := range rng.Problems {
			details = append(details, gen.ErrorDetail{Kind: "field", Text: p})
		}
		return envelope(http.StatusBadRequest, CodeBadRequest, rng.Error(), details...)
	case errors.As(err, &nc):
		details := make([]gen.ErrorDetail, 0, len(nc.Plan.Replaces))
		for _, r := range nc.Plan.Replaces {
			details = append(details, gen.ErrorDetail{Kind: "replaces", Text: r})
		}
		return envelope(http.StatusConflict, CodeNeedsConfirmation, nc.Error(), details...)
	case errors.Is(err, backup.ErrNotFound):
		return envelope(http.StatusNotFound, CodeNotFound, "no such backup")
	case errors.Is(err, backup.ErrNewerSchema):
		return envelope(http.StatusConflict, CodeConflict, backup.ErrNewerSchema.Error())
	case errors.Is(err, backup.ErrBadArchive):
		return envelope(http.StatusBadRequest, CodeBadRequest, err.Error())
	case errors.Is(err, backup.ErrAlreadyActive), errors.Is(err, backup.ErrNoGoodBackup):
		return envelope(http.StatusConflict, CodeConflict, err.Error())
	case errors.Is(err, audit.ErrBadCursor):
		return envelope(http.StatusBadRequest, CodeBadRequest, "the cursor is not valid")
	}
	s.opts.Logger.Error("operations request failed", "err", err)
	return envelope(http.StatusInternalServerError, CodeInternal, "internal error")
}

func intPtr(v int) *int { return &v }

func toGenSettings(st settings.Settings) gen.Settings {
	t := st.BackupTimeLocal
	return gen.Settings{
		RetentionJobHistoryDays:    intPtr(st.RetentionJobHistoryDays),
		RetentionLogsDays:          intPtr(st.RetentionLogsDays),
		RetentionLogsMibPerMachine: intPtr(st.RetentionLogsMiBPerMachine),
		RetentionAuditDays:         intPtr(st.RetentionAuditDays),
		RetentionLoadRawHours:      intPtr(st.RetentionLoadRawHours),
		BackupTimeLocal:            &t,
		BackupKeepDays:             intPtr(st.BackupKeepDays),
		MaxEventStreams:            intPtr(st.MaxEventStreams),
		FallbackGraceMinutes:       intPtr(st.FallbackGraceMinutes),
		OverflowLabel:              &st.OverflowLabel,
		OverflowTargets:            toGenTargets(st.OverflowTargets),
		AutoUpdate:                 &st.AutoUpdate,

		InsightsUpdatePeriod:     (*gen.SettingsInsightsUpdatePeriod)(&st.InsightsUpdatePeriod),
		InsightsUpdateEveryNRuns: intPtr(st.InsightsUpdateEveryNthRuns),
	}
}

// GetSettings implements GET /api/v1/settings.
func (s *server) GetSettings(ctx context.Context, _ gen.GetSettingsRequestObject) (gen.GetSettingsResponseObject, error) {
	if s.opts.Settings == nil {
		f := unavailable()
		return gen.GetSettingsdefaultJSONResponse{StatusCode: f.status, Body: f.body}, nil
	}
	st, err := s.opts.Settings.Get(ctx)
	if err != nil {
		f := s.opsError(err)
		return gen.GetSettingsdefaultJSONResponse{StatusCode: f.status, Body: f.body}, nil
	}
	out := toGenSettings(st)
	if a := s.ghArea(); a != nil && a.Repos != nil {
		out.OverflowUsage = s.overflowUsage(ctx, a.Repos)
	}
	return gen.GetSettings200JSONResponse(out), nil
}

// overflowUsage is the minutes each overflow target took this month, all repositories together (R5.115).
func (s *server) overflowUsage(ctx context.Context, svc *repos.Service) *[]gen.OverflowUsage {
	us, err := svc.OverflowUsage(ctx, time.Now())
	if err != nil {
		s.opts.Logger.Warn("overflow usage failed", "err", err)
		return nil
	}
	out := make([]gen.OverflowUsage, 0, len(us))
	for _, u := range us {
		out = append(out, gen.OverflowUsage{Kind: gen.OverflowUsageKind(u.Kind), UsedMinutes: u.UsedMinutes})
	}
	return &out
}

// UpdateSettings implements PATCH /api/v1/settings.
func (s *server) UpdateSettings(ctx context.Context, req gen.UpdateSettingsRequestObject) (gen.UpdateSettingsResponseObject, error) {
	fail := func(f opsFailure) (gen.UpdateSettingsResponseObject, error) {
		return gen.UpdateSettingsdefaultJSONResponse{StatusCode: f.status, Body: f.body}, nil
	}
	if s.opts.Settings == nil {
		return fail(unavailable())
	}
	if req.Body == nil {
		return fail(envelope(http.StatusBadRequest, CodeBadRequest, "the request is not valid"))
	}
	b := req.Body
	st, err := s.opts.Settings.Update(ctx, audit.ActorFrom(ctx), settings.Patch{
		RetentionJobHistoryDays:    b.RetentionJobHistoryDays,
		RetentionLogsDays:          b.RetentionLogsDays,
		RetentionLogsMiBPerMachine: b.RetentionLogsMibPerMachine,
		RetentionAuditDays:         b.RetentionAuditDays,
		RetentionLoadRawHours:      b.RetentionLoadRawHours,
		BackupTimeLocal:            b.BackupTimeLocal,
		BackupKeepDays:             b.BackupKeepDays,
		MaxEventStreams:            b.MaxEventStreams,
		FallbackGraceMinutes:       b.FallbackGraceMinutes,
		OverflowLabel:              b.OverflowLabel,
		OverflowTargets:            fromGenTargets(b.OverflowTargets),
		AutoUpdate:                 b.AutoUpdate,

		InsightsUpdatePeriod:       (*string)(b.InsightsUpdatePeriod),
		InsightsUpdateEveryNthRuns: b.InsightsUpdateEveryNRuns,
	})
	if err != nil {
		return fail(s.opsError(err))
	}
	return gen.UpdateSettings200JSONResponse(toGenSettings(st)), nil
}

// ListAuditEvents implements GET /api/v1/audit.
func (s *server) ListAuditEvents(ctx context.Context, req gen.ListAuditEventsRequestObject) (gen.ListAuditEventsResponseObject, error) {
	fail := func(f opsFailure) (gen.ListAuditEventsResponseObject, error) {
		return gen.ListAuditEventsdefaultJSONResponse{StatusCode: f.status, Body: f.body}, nil
	}
	if s.opts.Audit == nil {
		return fail(unavailable())
	}
	p := req.Params
	str := func(v *string) string {
		if v == nil {
			return ""
		}
		return *v
	}
	f := audit.Filter{Actor: str(p.Actor), Action: str(p.Action), TargetType: str(p.TargetType), TargetID: str(p.TargetId)}
	if p.From != nil {
		f.From = *p.From
	}
	if p.To != nil {
		f.To = *p.To
	}
	if p.Cursor != nil {
		f.Cursor = *p.Cursor
	}
	if p.Limit != nil {
		f.Limit = *p.Limit
	}
	page, err := s.opts.Audit.List(ctx, f)
	if err != nil {
		return fail(s.opsError(err))
	}
	out := gen.AuditPage{Items: make([]gen.AuditEvent, 0, len(page.Items))}
	for _, e := range page.Items {
		out.Items = append(out.Items, toGenAuditEvent(e))
	}
	if page.NextCursor != "" {
		c := page.NextCursor
		out.NextCursor = &c
	}
	return gen.ListAuditEvents200JSONResponse(out), nil
}

func toGenAuditEvent(e audit.Event) gen.AuditEvent {
	ev := gen.AuditEvent{
		Id: e.ID, At: e.At, Action: e.Action, Actor: e.Actor, Summary: e.Summary,
		TargetType: e.TargetType, TargetId: e.TargetID,
		Result: gen.AuditEventResult(e.Result), Source: gen.AuditEventSource(e.Source),
	}
	if e.ActorID != "" {
		id := e.ActorID
		ev.ActorId = &id
	}
	return ev
}

func toGenBackup(b backup.Backup) gen.Backup {
	out := gen.Backup{Id: b.ID, File: b.File, CreatedAt: b.CreatedAt, State: gen.BackupState(b.State)}
	size, schema := b.SizeBytes, b.SchemaVersion
	out.SizeBytes, out.SchemaVersion = &size, &schema
	if b.CheckResult != "" {
		c := b.CheckResult
		out.CheckResult = &c
	}
	return out
}

func toGenDrill(d backup.Drill) gen.BackupDrill {
	out := gen.BackupDrill{Id: d.ID, StartedAt: d.StartedAt, State: gen.BackupDrillState(d.State)}
	if d.BackupID != "" {
		b := d.BackupID
		out.BackupId = &b
	}
	if d.Detail != "" {
		det := d.Detail
		out.Detail = &det
	}
	if !d.FinishedAt.IsZero() {
		t := d.FinishedAt
		out.FinishedAt = &t
	}
	return out
}

// ListBackups implements GET /api/v1/backups.
func (s *server) ListBackups(ctx context.Context, _ gen.ListBackupsRequestObject) (gen.ListBackupsResponseObject, error) {
	fail := func(f opsFailure) (gen.ListBackupsResponseObject, error) {
		return gen.ListBackupsdefaultJSONResponse{StatusCode: f.status, Body: f.body}, nil
	}
	if s.opts.Backup == nil {
		return fail(unavailable())
	}
	list, drill, err := s.opts.Backup.List(ctx)
	if err != nil {
		return fail(s.opsError(err))
	}
	out := gen.BackupList{Items: make([]gen.Backup, 0, len(list))}
	for _, b := range list {
		out.Items = append(out.Items, toGenBackup(b))
	}
	if drill != nil {
		d := toGenDrill(*drill)
		out.LastDrill = &d
	}
	return gen.ListBackups200JSONResponse(out), nil
}

// RunBackup implements POST /api/v1/backups.
func (s *server) RunBackup(ctx context.Context, _ gen.RunBackupRequestObject) (gen.RunBackupResponseObject, error) {
	fail := func(f opsFailure) (gen.RunBackupResponseObject, error) {
		return gen.RunBackupdefaultJSONResponse{StatusCode: f.status, Body: f.body}, nil
	}
	if s.opts.Backup == nil {
		return fail(unavailable())
	}
	b, err := s.opts.Backup.RunBackup(ctx, audit.ActorFrom(ctx))
	if err != nil {
		return fail(s.opsError(err))
	}
	return gen.RunBackup202JSONResponse(toGenBackup(b)), nil
}

// RunBackupDrill implements POST /api/v1/backups/drills.
func (s *server) RunBackupDrill(ctx context.Context, _ gen.RunBackupDrillRequestObject) (gen.RunBackupDrillResponseObject, error) {
	fail := func(f opsFailure) (gen.RunBackupDrillResponseObject, error) {
		return gen.RunBackupDrilldefaultJSONResponse{StatusCode: f.status, Body: f.body}, nil
	}
	if s.opts.Backup == nil {
		return fail(unavailable())
	}
	d, err := s.opts.Backup.RunDrill(ctx, audit.ActorFrom(ctx))
	if err != nil && d.ID == "" {
		return fail(s.opsError(err))
	}
	// A drill that ran and failed is a result, not a request error.
	return gen.RunBackupDrill202JSONResponse(toGenDrill(d)), nil
}

// RestoreBackup implements POST /api/v1/backups/{backupId}/restore.
func (s *server) RestoreBackup(ctx context.Context, req gen.RestoreBackupRequestObject) (gen.RestoreBackupResponseObject, error) {
	fail := func(f opsFailure) (gen.RestoreBackupResponseObject, error) {
		return gen.RestoreBackupdefaultJSONResponse{StatusCode: f.status, Body: f.body}, nil
	}
	if s.opts.Backup == nil {
		return fail(unavailable())
	}
	confirm := req.Params.Confirm != nil && *req.Params.Confirm
	plan, err := s.opts.Backup.Restore(ctx, audit.ActorFrom(ctx), req.BackupId, confirm)
	if err != nil {
		return fail(s.opsError(err))
	}
	b, err := s.opts.Backup.Get(ctx, plan.BackupID)
	if errors.Is(err, backup.ErrNotFound) {
		b, err = backup.Backup{ID: plan.BackupID, File: plan.File, CreatedAt: plan.CreatedAt, State: backup.StateGood}, nil
	}
	if err != nil {
		return fail(s.opsError(err))
	}
	return gen.RestoreBackup202JSONResponse(toGenBackup(b)), nil
}

// toGenTargets maps the saved overflow priority list to the API (R5.114).
func toGenTargets(in []settings.OverflowTarget) *[]gen.OverflowTarget {
	out := make([]gen.OverflowTarget, 0, len(in))
	for _, t := range in {
		g := gen.OverflowTarget{Kind: gen.OverflowTargetKind(t.Kind), Enabled: t.Enabled, Label: &t.Label, CapMinutes: &t.CapMinutes, TotalCapMinutes: &t.TotalCapMinutes}
		systems := make([]gen.OverflowTargetSystems, 0, len(t.Systems))
		for _, s := range t.Systems {
			systems = append(systems, gen.OverflowTargetSystems(s))
		}
		g.Systems = &systems
		out = append(out, g)
	}
	return &out
}

func fromGenTargets(in *[]gen.OverflowTarget) *[]settings.OverflowTarget {
	if in == nil {
		return nil
	}
	out := make([]settings.OverflowTarget, 0, len(*in))
	for _, g := range *in {
		t := settings.OverflowTarget{Kind: string(g.Kind), Enabled: g.Enabled}
		if g.Label != nil {
			t.Label = *g.Label
		}
		if g.CapMinutes != nil {
			t.CapMinutes = *g.CapMinutes
		}
		if g.TotalCapMinutes != nil {
			t.TotalCapMinutes = *g.TotalCapMinutes
		}
		if g.Systems != nil {
			for _, s := range *g.Systems {
				t.Systems = append(t.Systems, string(s))
			}
		}
		out = append(out, t)
	}
	return &out
}
