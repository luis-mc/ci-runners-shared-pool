package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api/gen"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/audit"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry/retention"
)

// RegistryRetentionService is what the retention routes need (registry R5.41,
// R5.42, R5.46, R5.48). *retention.Service implements it.
type RegistryRetentionService interface {
	Image(ctx context.Context, id string) (registry.Image, error)
	Delete(ctx context.Context, imageID string, confirm bool, by string) (registry.InUse, error)
	BudgetReport(ctx context.Context) (retention.BudgetReport, error)
	PutBudgets(ctx context.Context, standard, custom int64, by string) (retention.BudgetReport, error)
	Settings(ctx context.Context) (registry.Settings, error)
	PendingFolder(st registry.Settings) string
	PutSettings(ctx context.Context, in registry.Settings, by string) (registry.Settings, error)
	StartCleanup(ctx context.Context, trigger, by string) (retention.Cleanup, error)
}

var _ RegistryRetentionService = (*retention.Service)(nil)

// RetentionAuditor writes the retention audit entries (R5.51) to the audit log.
func RetentionAuditor(rec audit.Recorder) retention.Auditor { return auditAdapter{rec} }

type auditAdapter struct{ rec audit.Recorder }

func (a auditAdapter) Record(ctx context.Context, actor retention.Actor, action, targetType, targetID string, details map[string]any) error {
	return a.rec.Record(ctx, audit.Actor{Login: actor.Login, Source: actor.Source}, action,
		audit.Target{Type: targetType, ID: targetID}, details)
}

func (s *server) retentionFail(err error) (int, gen.ErrorEnvelope) {
	var ve *retention.ValidationError
	var big *retention.BudgetTooBig
	switch {
	case errors.As(err, &big):
		return http.StatusBadRequest, errEnvelope(CodeBadRequest, fmt.Sprintf(
			"the two budgets may add up to at most %d bytes: half the free disk of the registry folder", big.MaxTotalBytes))
	case errors.As(err, &ve):
		return http.StatusBadRequest, errEnvelope(CodeBadRequest, ve.Msg)
	case errors.Is(err, registry.ErrNotFound):
		return http.StatusNotFound, errEnvelope(CodeNotFound, "no such image")
	case errors.Is(err, retention.ErrCleanupRunning):
		return http.StatusConflict, errEnvelope(CodeConflict, "a cleanup is running")
	}
	s.opts.Logger.Error("registry retention", "err", err)
	return http.StatusInternalServerError, errEnvelope(CodeInternal, "internal error")
}

// inUseDetails names what uses an image, for the question card and the refusal.
func inUseDetails(u registry.InUse, keepUntil time.Time) []gen.ErrorDetail {
	var d []gen.ErrorDetail
	for _, j := range u.RunningJobs {
		d = append(d, gen.ErrorDetail{Kind: "job", Text: j})
	}
	for _, p := range u.Pointers {
		d = append(d, gen.ErrorDetail{Kind: "pointer", Id: strPtr(p.Repository),
			Text: fmt.Sprintf("%s uses it for %s on %s", p.Repository, p.Profile, p.Arch)})
	}
	if u.InRollback {
		d = append(d, gen.ErrorDetail{Kind: "rollback", Text: "kept for a rollback until " + keepUntil.Format("Mon 2 Jan")})
	}
	if u.PendingPulls > 0 {
		d = append(d, gen.ErrorDetail{Kind: "pull", Text: fmt.Sprintf("%d token(s) that name it have not ended", u.PendingPulls)})
	}
	return d
}

func genImage(img registry.Image) gen.RegistryImage {
	out := gen.RegistryImage{
		Id: img.ID, Key: string(img.Key), Kind: gen.RegistryImageKind(img.Kind), Name: img.Name, Profile: img.Profile,
		Arch: gen.RegistryArch(img.Arch), State: gen.RegistryImageState(img.State), ManifestDigest: img.Manifest,
		BaseName: strPtr(img.BaseName), BaseDigest: strPtr(img.BaseDigest), Branch: strPtr(img.Branch), Commit: strPtr(img.Commit),
		KeepUntil: timePtr(img.KeepUntil), LastPullAt: timePtr(img.LastPullAt), PromotedAt: timePtr(img.PromotedAt),
		PushedAt: img.PushedAt, PushedBy: strPtr(img.PushedBy), Replaces: strPtr(img.Replaces), ReplacedBy: strPtr(img.ReplacedBy),
		SizeBytes: img.SizeBytes, UniqueBytes: img.UniqueBytes, SourceRepository: strPtr(img.SourceRepository),
		StackHash: strPtr(img.StackHash), ScanState: gen.RegistryScanState(img.Scan),
		Layers: []gen.RegistryLayer{}, Lineage: []gen.RegistryLineageEntry{}, Machines: []gen.RegistryImageMachine{},
		Pointers: []gen.RegistryPointer{}, PullsByDay: []gen.RegistryPullDay{},
	}
	if out.ScanState == "" {
		out.ScanState = gen.RegistryScanStateNotScanned
	}
	return out
}

// DeleteRegistryImage implements DELETE /api/v1/registry/images/{id} (registry R5.46, R5.47, R5.51).
func (s *server) DeleteRegistryImage(ctx context.Context, req gen.DeleteRegistryImageRequestObject) (gen.DeleteRegistryImageResponseObject, error) {
	if s.opts.RegistryRetention == nil {
		return gen.DeleteRegistryImagedefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: notImplemented()}, nil
	}
	img, err := s.opts.RegistryRetention.Image(ctx, req.RegistryImageId)
	if err != nil {
		code, body := s.retentionFail(err)
		return gen.DeleteRegistryImagedefaultJSONResponse{StatusCode: code, Body: body}, nil
	}
	confirm := req.Params.Confirm != nil && *req.Params.Confirm
	u, err := s.opts.RegistryRetention.Delete(ctx, req.RegistryImageId, confirm, audit.ActorFrom(ctx).Login)
	switch {
	case err == nil:
		img.State = registry.StateDeleted
		return gen.DeleteRegistryImage202JSONResponse(genImage(img)), nil
	case errors.Is(err, retention.ErrNeedsConfirmation):
		d := inUseDetails(u, img.KeepUntil)
		return gen.DeleteRegistryImagedefaultJSONResponse{StatusCode: http.StatusConflict, Body: gen.ErrorEnvelope{Error: gen.ErrorBody{
			Code: CodeNeedsConfirmation, Message: "this delete needs a yes; it names what uses the image", Details: &d}}}, nil
	case errors.Is(err, registry.ErrInUse):
		d := inUseDetails(u, img.KeepUntil)
		msg := "a running job uses this image"
		if len(u.RunningJobs) > 0 {
			msg = "a running job uses this image: " + u.RunningJobs[0]
		} else if u.PendingPulls > 0 {
			msg = "a token that names this image has not ended yet; try again in a few minutes"
		}
		return gen.DeleteRegistryImagedefaultJSONResponse{StatusCode: http.StatusConflict, Body: gen.ErrorEnvelope{Error: gen.ErrorBody{
			Code: CodeConflict, Message: msg, Details: &d}}}, nil
	}
	code, body := s.retentionFail(err)
	return gen.DeleteRegistryImagedefaultJSONResponse{StatusCode: code, Body: body}, nil
}

func genBudgets(r retention.BudgetReport) gen.RegistryBudgets {
	mk := func(b registry.Budget) gen.RegistryBudget {
		return gen.RegistryBudget{Kind: gen.RegistryImageKind(b.Kind), LimitBytes: b.LimitBytes, UsedBytes: b.UsedBytes,
			SharedBytes: b.SharedBytes, Over: b.UsedBytes > b.LimitBytes}
	}
	return gen.RegistryBudgets{Standard: mk(r.Standard), Custom: mk(r.Custom), FreeDiskBytes: r.FreeDiskBytes, MaxTotalBytes: r.MaxTotalBytes}
}

// GetRegistryBudgets implements GET /api/v1/registry/budgets (registry R5.41).
func (s *server) GetRegistryBudgets(ctx context.Context, _ gen.GetRegistryBudgetsRequestObject) (gen.GetRegistryBudgetsResponseObject, error) {
	if s.opts.RegistryRetention == nil {
		return gen.GetRegistryBudgetsdefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: notImplemented()}, nil
	}
	r, err := s.opts.RegistryRetention.BudgetReport(ctx)
	if err != nil {
		code, body := s.retentionFail(err)
		return gen.GetRegistryBudgetsdefaultJSONResponse{StatusCode: code, Body: body}, nil
	}
	return gen.GetRegistryBudgets200JSONResponse(genBudgets(r)), nil
}

// PutRegistryBudgets implements PUT /api/v1/registry/budgets (registry R5.41, R5.51).
func (s *server) PutRegistryBudgets(ctx context.Context, req gen.PutRegistryBudgetsRequestObject) (gen.PutRegistryBudgetsResponseObject, error) {
	if s.opts.RegistryRetention == nil {
		return gen.PutRegistryBudgetsdefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: notImplemented()}, nil
	}
	if req.Body == nil {
		return gen.PutRegistryBudgetsdefaultJSONResponse{StatusCode: http.StatusBadRequest, Body: errEnvelope(CodeBadRequest, "the request is not valid")}, nil
	}
	r, err := s.opts.RegistryRetention.PutBudgets(ctx, req.Body.StandardBytes, req.Body.CustomBytes, audit.ActorFrom(ctx).Login)
	if err != nil {
		code, body := s.retentionFail(err)
		return gen.PutRegistryBudgetsdefaultJSONResponse{StatusCode: code, Body: body}, nil
	}
	return gen.PutRegistryBudgets200JSONResponse(genBudgets(r)), nil
}

func (s *server) genStoreSettings(st registry.Settings) gen.RegistryStoreSettings {
	alone := []gen.RegistryRule{gen.RegistryRuleSuperseded, gen.RegistryRuleVariant, gen.RegistryRuleDangling}
	out := gen.RegistryStoreSettings{
		Folder: st.Folder, UnusedDays: st.UnusedDays, KeepOldDays: st.KeepOldDays, VariantGoneDays: st.VariantGoneDays,
		VariantUnusedDays: st.VariantUnused, CleanupWeekday: int(st.CleanupWeekday), CleanupHour: st.CleanupHour, AloneRules: &alone,
	}
	if p := s.opts.RegistryRetention.PendingFolder(st); p != "" {
		out.FolderPending = &p
	}
	return out
}

// GetRegistryStoreSettings implements GET /api/v1/registry/settings (registry R5.42).
func (s *server) GetRegistryStoreSettings(ctx context.Context, _ gen.GetRegistryStoreSettingsRequestObject) (gen.GetRegistryStoreSettingsResponseObject, error) {
	if s.opts.RegistryRetention == nil {
		return gen.GetRegistryStoreSettingsdefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: notImplemented()}, nil
	}
	st, err := s.opts.RegistryRetention.Settings(ctx)
	if err != nil {
		code, body := s.retentionFail(err)
		return gen.GetRegistryStoreSettingsdefaultJSONResponse{StatusCode: code, Body: body}, nil
	}
	return gen.GetRegistryStoreSettings200JSONResponse(s.genStoreSettings(st)), nil
}

// PutRegistryStoreSettings implements PUT /api/v1/registry/settings (registry R5.42, R5.51).
func (s *server) PutRegistryStoreSettings(ctx context.Context, req gen.PutRegistryStoreSettingsRequestObject) (gen.PutRegistryStoreSettingsResponseObject, error) {
	if s.opts.RegistryRetention == nil {
		return gen.PutRegistryStoreSettingsdefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: notImplemented()}, nil
	}
	if req.Body == nil {
		return gen.PutRegistryStoreSettingsdefaultJSONResponse{StatusCode: http.StatusBadRequest, Body: errEnvelope(CodeBadRequest, "the request is not valid")}, nil
	}
	b := req.Body
	st, err := s.opts.RegistryRetention.PutSettings(ctx, registry.Settings{
		Folder: b.Folder, UnusedDays: b.UnusedDays, KeepOldDays: b.KeepOldDays, VariantGoneDays: b.VariantGoneDays,
		VariantUnused: b.VariantUnusedDays, CleanupWeekday: time.Weekday(b.CleanupWeekday), CleanupHour: b.CleanupHour,
	}, audit.ActorFrom(ctx).Login)
	if err != nil {
		code, body := s.retentionFail(err)
		return gen.PutRegistryStoreSettingsdefaultJSONResponse{StatusCode: code, Body: body}, nil
	}
	return gen.PutRegistryStoreSettings200JSONResponse(s.genStoreSettings(st)), nil
}

// StartRegistryCleanup implements POST /api/v1/registry/cleanup (registry R5.48).
func (s *server) StartRegistryCleanup(ctx context.Context, _ gen.StartRegistryCleanupRequestObject) (gen.StartRegistryCleanupResponseObject, error) {
	if s.opts.RegistryRetention == nil {
		return gen.StartRegistryCleanupdefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: notImplemented()}, nil
	}
	c, err := s.opts.RegistryRetention.StartCleanup(ctx, retention.TriggerNow, audit.ActorFrom(ctx).Login)
	if err != nil {
		code, body := s.retentionFail(err)
		return gen.StartRegistryCleanupdefaultJSONResponse{StatusCode: code, Body: body}, nil
	}
	return gen.StartRegistryCleanup202JSONResponse(gen.RegistryCleanup{
		Id: c.ID, Trigger: gen.RegistryCleanupTrigger(c.Trigger), State: gen.RegistryCleanupState(c.State),
		FreedBytes: c.FreedBytes, StartedAt: c.StartedAt, FinishedAt: timePtr(c.FinishedAt), Error: strPtr(c.Error),
	}), nil
}
