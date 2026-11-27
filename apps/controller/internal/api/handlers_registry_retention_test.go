package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/audit"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/auth"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry/retention"
)

type fakeRetention struct {
	img        registry.Image
	use        registry.InUse
	deleteErr  error
	confirm    bool
	by         string
	report     retention.BudgetReport
	putErr     error
	settings   registry.Settings
	pending    string
	settingsOK error
	cleanupErr error
	cleanup    retention.Cleanup
}

func (f *fakeRetention) Image(_ context.Context, id string) (registry.Image, error) {
	if id != f.img.ID {
		return registry.Image{}, registry.ErrNotFound
	}
	return f.img, nil
}
func (f *fakeRetention) Delete(_ context.Context, _ string, confirm bool, by string) (registry.InUse, error) {
	f.confirm, f.by = confirm, by
	if !confirm {
		return f.use, retention.ErrNeedsConfirmation
	}
	return f.use, f.deleteErr
}
func (f *fakeRetention) BudgetReport(context.Context) (retention.BudgetReport, error) {
	return f.report, nil
}
func (f *fakeRetention) PutBudgets(_ context.Context, std, custom int64, _ string) (retention.BudgetReport, error) {
	if f.putErr != nil {
		return f.report, f.putErr
	}
	f.report.Standard.LimitBytes, f.report.Custom.LimitBytes = std, custom
	return f.report, nil
}
func (f *fakeRetention) Settings(context.Context) (registry.Settings, error) { return f.settings, nil }
func (f *fakeRetention) PendingFolder(registry.Settings) string              { return f.pending }
func (f *fakeRetention) PutSettings(_ context.Context, in registry.Settings, _ string) (registry.Settings, error) {
	if f.settingsOK != nil {
		return f.settings, f.settingsOK
	}
	f.settings = in
	return in, nil
}
func (f *fakeRetention) StartCleanup(context.Context, string, string) (retention.Cleanup, error) {
	return f.cleanup, f.cleanupErr
}

func retentionMux(f *fakeRetention) *http.ServeMux {
	mux := http.NewServeMux()
	Register(mux, Options{Version: "t", DB: fakeDB{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), RegistryRetention: f, Authn: asRole(auth.RoleOwner)})
	return mux
}

func newFakeRetention() *fakeRetention {
	return &fakeRetention{
		img: registry.Image{ID: "img_1", Kind: registry.KindStandard, Name: "crsp/std/node", Key: "37fef5dbb41b", Profile: "node", Arch: "amd64",
			State: registry.StateSuperseded, Manifest: "sha256:abc", KeepUntil: time.Date(2026, 11, 21, 3, 0, 0, 0, time.UTC),
			PushedAt: time.Date(2026, 11, 10, 3, 0, 0, 0, time.UTC)},
		report: retention.BudgetReport{
			Standard:      registry.Budget{Kind: registry.KindStandard, LimitBytes: 40e9, UsedBytes: 41e9},
			Custom:        registry.Budget{Kind: registry.KindCustom, LimitBytes: 30e9, UsedBytes: 1e9, SharedBytes: 5},
			FreeDiskBytes: 200e9, MaxTotalBytes: 100e9,
		},
		settings: registry.Settings{Folder: "/data/registry", UnusedDays: 30, KeepOldDays: 7, VariantGoneDays: 7, VariantUnused: 14, CleanupHour: 3},
	}
}

type retErrBody struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Details []struct {
			Kind string `json:"kind"`
			Text string `json:"text"`
		} `json:"details"`
	} `json:"error"`
}

// R5.46: a delete without confirm answers 409 needs_confirmation naming the jobs, the pointers and the rollback days.
func TestDeleteImageNeedsConfirmationNamesWhatUsesIt(t *testing.T) {
	f := newFakeRetention()
	f.use = registry.InUse{RunningJobs: []string{"o/app ci.yml test (started 14:05)"},
		Pointers: []registry.Pointer{{Repository: "o/app", Profile: "node", Arch: "amd64"}}, InRollback: true}
	rec := do(retentionMux(f), http.MethodDelete, "/api/v1/registry/images/img_1", "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var b retErrBody
	_ = json.Unmarshal(rec.Body.Bytes(), &b)
	if b.Error.Code != CodeNeedsConfirmation || len(b.Error.Details) != 3 {
		t.Fatalf("%+v", b)
	}
	kinds := b.Error.Details[0].Kind + b.Error.Details[1].Kind + b.Error.Details[2].Kind
	if kinds != "jobpointerrollback" || !strings.Contains(b.Error.Details[0].Text, "o/app ci.yml test") ||
		!strings.Contains(b.Error.Details[2].Text, "Sat 21 Nov") {
		t.Fatalf("%+v", b.Error.Details)
	}
}

// R5.46: an image a running job uses is refused even with confirm: 409 conflict naming the job.
func TestDeleteImageRefusedWhileAJobUsesIt(t *testing.T) {
	f := newFakeRetention()
	f.use = registry.InUse{RunningJobs: []string{"o/app ci.yml test"}}
	f.deleteErr = registry.ErrInUse
	rec := do(retentionMux(f), http.MethodDelete, "/api/v1/registry/images/img_1?confirm=true", "")
	var b retErrBody
	_ = json.Unmarshal(rec.Body.Bytes(), &b)
	if rec.Code != http.StatusConflict || b.Error.Code != CodeConflict || !strings.Contains(b.Error.Message, "o/app ci.yml test") {
		t.Fatalf("%d %+v", rec.Code, b)
	}
}

func TestDeleteImageWithConfirmAnswers202(t *testing.T) {
	f := newFakeRetention()
	rec := do(retentionMux(f), http.MethodDelete, "/api/v1/registry/images/img_1?confirm=true", "")
	if rec.Code != http.StatusAccepted || !f.confirm || f.by == "" {
		t.Fatalf("%d confirm=%v by=%q %s", rec.Code, f.confirm, f.by, rec.Body)
	}
	var img struct {
		ID    string `json:"id"`
		State string `json:"state"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &img)
	if img.ID != "img_1" || img.State != "deleted" {
		t.Fatalf("%+v", img)
	}
	if rec := do(retentionMux(f), http.MethodDelete, "/api/v1/registry/images/nope?confirm=true", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown image: %d", rec.Code)
	}
}

// R5.41: budgets show use against the limit; a sum above half the free disk is 400 and names the most.
func TestBudgetsGetAndPut(t *testing.T) {
	f := newFakeRetention()
	mux := retentionMux(f)
	rec := do(mux, http.MethodGet, "/api/v1/registry/budgets", "")
	var got struct {
		Standard struct {
			LimitBytes int64 `json:"limit_bytes"`
			Over       bool  `json:"over"`
		} `json:"standard"`
		Custom        struct{ Over bool } `json:"custom"`
		MaxTotalBytes int64               `json:"max_total_bytes"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if rec.Code != 200 || got.Standard.LimitBytes != 40e9 || !got.Standard.Over || got.Custom.Over || got.MaxTotalBytes != 100e9 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if rec := do(mux, http.MethodPut, "/api/v1/registry/budgets", `{"standard_bytes":50000000000,"custom_bytes":20000000000}`); rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	f.putErr = &retention.BudgetTooBig{MaxTotalBytes: 100e9}
	rec = do(mux, http.MethodPut, "/api/v1/registry/budgets", `{"standard_bytes":90000000000,"custom_bytes":20000000000}`)
	var b retErrBody
	_ = json.Unmarshal(rec.Body.Bytes(), &b)
	if rec.Code != http.StatusBadRequest || b.Error.Code != CodeBadRequest || !strings.Contains(b.Error.Message, "100000000000") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

// R5.42: settings carry the days and the cleanup time, the alone rules, and a pending folder.
func TestStoreSettingsGetAndPut(t *testing.T) {
	f := newFakeRetention()
	f.pending = "/mnt/big/registry"
	mux := retentionMux(f)
	rec := do(mux, http.MethodGet, "/api/v1/registry/settings", "")
	var got struct {
		Folder        string   `json:"folder"`
		FolderPending string   `json:"folder_pending"`
		UnusedDays    int      `json:"unused_days"`
		AloneRules    []string `json:"alone_rules"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if rec.Code != 200 || got.Folder != "/data/registry" || got.FolderPending != "/mnt/big/registry" || got.UnusedDays != 30 ||
		strings.Join(got.AloneRules, ",") != "superseded,variant,dangling" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	body := `{"folder":"/data/registry","unused_days":45,"keep_old_days":7,"variant_gone_days":7,"variant_unused_days":14,"cleanup_weekday":0,"cleanup_hour":4}`
	if rec := do(mux, http.MethodPut, "/api/v1/registry/settings", body); rec.Code != 200 || f.settings.UnusedDays != 45 || f.settings.CleanupHour != 4 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	f.settingsOK = &retention.ValidationError{Msg: "the folder must be a full path"}
	rec = do(mux, http.MethodPut, "/api/v1/registry/settings", body)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "full path") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

// R5.48: Clean up now answers 202 with the running cleanup, and 409 while one runs.
func TestStartCleanup(t *testing.T) {
	f := newFakeRetention()
	f.cleanup = retention.Cleanup{ID: 7, Trigger: "now", State: "running", StartedAt: time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)}
	mux := retentionMux(f)
	rec := do(mux, http.MethodPost, "/api/v1/registry/cleanup", "")
	if rec.Code != http.StatusAccepted || !strings.Contains(rec.Body.String(), `"state":"running"`) {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	f.cleanupErr = retention.ErrCleanupRunning
	if rec := do(mux, http.MethodPost, "/api/v1/registry/cleanup", ""); rec.Code != http.StatusConflict {
		t.Fatalf("%d", rec.Code)
	}
}

func TestRetentionRoutesAnswer501WithoutAService(t *testing.T) {
	mux := http.NewServeMux()
	Register(mux, Options{Version: "t", DB: fakeDB{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Authn: asRole(auth.RoleOwner)})
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/registry/budgets"}, {http.MethodGet, "/api/v1/registry/settings"},
		{http.MethodPost, "/api/v1/registry/cleanup"}, {http.MethodDelete, "/api/v1/registry/images/x"},
	} {
		if rec := do(mux, c.method, c.path, ""); rec.Code != http.StatusNotImplemented {
			t.Fatalf("%s %s: %d", c.method, c.path, rec.Code)
		}
	}
}

type recordingAudit struct {
	actor  audit.Actor
	action string
	target audit.Target
}

func (r *recordingAudit) Record(_ context.Context, a audit.Actor, action string, t audit.Target, _ map[string]any) error {
	r.actor, r.action, r.target = a, action, t
	return nil
}
func (r *recordingAudit) RecordRefused(context.Context, audit.Actor, string, audit.Target, map[string]any) error {
	return nil
}

// R5.51: the retention audit entries reach the audit log with the actor and the target.
func TestRetentionAuditorWritesToTheAuditLog(t *testing.T) {
	rec := &recordingAudit{}
	err := RetentionAuditor(rec).Record(context.Background(), retention.Actor{Login: "luis-mc", Source: "web"}, retention.ActionDelete, "registry_image", "img_1", nil)
	if err != nil || rec.action != "registry.image.delete" || rec.actor.Login != "luis-mc" || rec.actor.Source != "web" || rec.target.ID != "img_1" {
		t.Fatalf("%v %+v", err, rec)
	}
}
