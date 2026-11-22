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

	"github.com/luis-mc/ci-runners-shared-pool/internal/auth"
	agentv1 "github.com/luis-mc/ci-runners-shared-pool/internal/gen/agent/v1"
	"github.com/luis-mc/ci-runners-shared-pool/internal/machinehealth"
	"github.com/luis-mc/ci-runners-shared-pool/internal/machines"
)

type healthFake struct {
	view   machinehealth.View
	stats  machinehealth.MachineStats
	th     machinehealth.Thresholds
	window string
}

func (f *healthFake) View(context.Context, string) (machinehealth.View, error) { return f.view, nil }
func (f *healthFake) MachineJobStats(_ context.Context, _, w string) (machinehealth.MachineStats, error) {
	f.window = w
	if _, err := machinehealth.WindowDuration(w); err != nil {
		return machinehealth.MachineStats{}, err
	}
	return f.stats, nil
}
func (f *healthFake) RunnerJobStats(_ context.Context, id, w string) (machinehealth.RunnerStats, time.Time, time.Time, error) {
	if _, err := machinehealth.WindowDuration(w); err != nil {
		return machinehealth.RunnerStats{}, time.Time{}, time.Time{}, err
	}
	return machinehealth.RunnerStats{RunnerID: id, Stats: f.stats.Machine}, f.stats.From, f.stats.To, nil
}
func (f *healthFake) Thresholds(context.Context) (machinehealth.Thresholds, error) { return f.th, nil }
func (f *healthFake) SetThresholds(_ context.Context, t machinehealth.Thresholds, _ string) error {
	if err := t.Validate(); err != nil {
		return err
	}
	f.th = t
	return nil
}

func healthMux(t *testing.T, role auth.Role, f HealthService) (*http.ServeMux, string) {
	t.Helper()
	repo := machines.NewMemRepo()
	svc := machines.New(machines.Options{Repo: repo, ControllerAddress: "pool.lan", AgentPort: 8461, Fingerprint: strings.Repeat("ab", 32)})
	code, _ := svc.CreateJoinCode(context.Background(), "owner", 0)
	res, err := svc.Join(context.Background(), machines.JoinInput{Code: code.Code, Facts: machines.Facts{HostName: "box-1", OS: "linux", Arch: "x64", Threads: 8, MemoryBytes: 32 << 30}})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	Register(mux, Options{Version: "t", DB: fakeDB{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Machines: svc, Health: f, Authn: asRole(role)})
	return mux, res.MachineID
}

func TestMachineHealthRoute(t *testing.T) {
	days := 2.5
	rep := &agentv1.HealthReport{
		Boot:         &agentv1.BootHealth{Known: true, UptimeSeconds: 100, LastBootUnixMs: 1700000000000, UnexpectedRestart: true},
		Thermal:      &agentv1.ThermalHealth{UnknownReason: "Windows gives it only to an administrator"},
		Updates:      &agentv1.UpdateHealth{Known: true, RestartPending: true, RestartAllowedNow: true},
		Power:        &agentv1.PowerHealth{SourceKnown: true, OnBattery: false, SleepUnknownReason: "no systemd"},
		Connectivity: &agentv1.ConnectivityHealth{RttKnown: true, RttMs: 12, Reconnects_24H: 0, SkewKnown: true, ClockSkewSeconds: 0},
	}
	f := &healthFake{view: machinehealth.View{Reported: true, ReceivedAt: time.Now(), Report: rep, ControllerVersion: "1.4.0", RestartMayHappenNow: true, RestartMayHitJobs: true,
		Disks: []machinehealth.Disk{{Role: "system", Path: "/", Known: true, Total: 100, Free: 10, FreePct: 10, DaysUntilFull: &days}, {Role: "docker", Path: "/d", UnknownReason: "gone"}}}}
	mux, id := healthMux(t, auth.RoleViewer, f)
	rec := do(mux, http.MethodGet, "/api/v1/machines/"+id+"/health", "")
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	boot := out["boot"].(map[string]any)
	if boot["unexpected_restart"] != true || boot["uptime_seconds"].(float64) != 100 {
		t.Fatalf("boot %v", boot)
	}
	th := out["thermal"].(map[string]any)
	if _, has := th["max_temp_celsius"]; has || th["unknown_reason"] == "" {
		t.Fatalf("unknown must be absent with a reason, never 0: %v", th)
	}
	vols := out["volumes"].([]any)
	if len(vols) != 2 || vols[0].(map[string]any)["days_until_full"].(float64) != 2.5 {
		t.Fatalf("%v", vols)
	}
	if _, has := vols[1].(map[string]any)["free_bytes"]; has {
		t.Fatalf("an unknown volume has no free bytes: %v", vols[1])
	}
	if out["updates"].(map[string]any)["restart_may_hit_jobs"] != true || out["power"].(map[string]any)["on_battery"] != false {
		t.Fatalf("%v", out)
	}
	if out["power"].(map[string]any)["sleep_unknown_reason"] == nil {
		t.Fatalf("%v", out["power"])
	}
	if rec := do(mux, http.MethodGet, "/api/v1/machines/nobody/health", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown machine: %d", rec.Code)
	}
	f.view = machinehealth.View{MachineID: id}
	rec = do(mux, http.MethodGet, "/api/v1/machines/"+id+"/health", "")
	out = nil
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out["reported"] != false || out["boot"] != nil {
		t.Fatalf("not reported yet: %v", out)
	}
}

func TestJobStatsRoutes(t *testing.T) {
	busy := 12.5
	rate := 0.5
	f := &healthFake{stats: machinehealth.MachineStats{Window: "7d", From: time.Now().Add(-7 * 24 * time.Hour), To: time.Now(),
		Machine: machinehealth.Stats{BusyPct: &busy, Rates: machinehealth.Rates{Failed: &rate}}}}
	f.stats.Machine.Counts.Failed, f.stats.Machine.Counts.Succeeded = 1, 1
	f.stats.Machine.LastFailure = &machinehealth.JobFailure{JobID: 7, Repository: "o/r", Name: "test", RunID: 9, Class: "failed", At: time.Now(), JobURL: "https://gh/j", RunURL: "https://gh/r"}
	f.stats.Runners = []machinehealth.RunnerStats{{RunnerID: "ra", RunnerName: "runner-a", Stats: f.stats.Machine}}
	mux, id := healthMux(t, auth.RoleViewer, f)
	rec := do(mux, http.MethodGet, "/api/v1/machines/"+id+"/job-stats?window=7d", "")
	if rec.Code != 200 || f.window != "7d" {
		t.Fatalf("%d %s window %q", rec.Code, rec.Body, f.window)
	}
	var out struct {
		Window  string `json:"window"`
		Machine struct {
			Counts      struct{ Failed, Total int }
			BusyPct     float64 `json:"busy_pct"`
			Rates       struct{ Failed float64 }
			LastFailure struct {
				JobURL string `json:"job_url"`
				RunURL string `json:"run_url"`
			} `json:"last_failure"`
		}
		Runners []struct {
			RunnerID string `json:"runner_id"`
		}
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.Window != "7d" || out.Machine.Counts.Total != 2 || out.Machine.BusyPct != 12.5 || out.Machine.Rates.Failed != 0.5 || out.Machine.LastFailure.RunURL == "" || len(out.Runners) != 1 {
		t.Fatalf("%+v", out)
	}
	if rec := do(mux, http.MethodGet, "/api/v1/machines/"+id+"/job-stats", ""); rec.Code != 200 || f.window != "24h" {
		t.Fatalf("default window: %d %q", rec.Code, f.window)
	}
	if rec := do(mux, http.MethodGet, "/api/v1/machines/"+id+"/job-stats?window=30d", ""); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad window: %d %s", rec.Code, rec.Body)
	}
	if rec := do(mux, http.MethodGet, "/api/v1/runners/ra/job-stats?window=24h", ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"runner_id":"ra"`) {
		t.Fatalf("runner: %d %s", rec.Code, rec.Body)
	}
}

func TestHealthThresholdsRoutes(t *testing.T) {
	f := &healthFake{th: machinehealth.DefaultThresholds}
	viewer, _ := healthMux(t, auth.RoleViewer, f)
	if rec := do(viewer, http.MethodGet, "/api/v1/health-thresholds", ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"disk_days_under":3`) {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	body := `{"disk_days_under":5,"disk_free_pct_under":10,"no_heartbeat_seconds":120,"clock_skew_seconds":30,"oom_window_minutes":60}`
	if rec := do(viewer, http.MethodPut, "/api/v1/health-thresholds", body); rec.Code != http.StatusForbidden {
		t.Fatalf("a viewer does not write: %d", rec.Code)
	}
	admin, _ := healthMux(t, auth.RoleAdmin, f)
	if rec := do(admin, http.MethodPut, "/api/v1/health-thresholds", body); rec.Code != 200 || f.th.DiskDaysUnder != 5 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if rec := do(admin, http.MethodPut, "/api/v1/health-thresholds", strings.Replace(body, `"clock_skew_seconds":30`, `"clock_skew_seconds":0`, 1)); rec.Code != http.StatusBadRequest {
		t.Fatalf("out of range: %d", rec.Code)
	}
}

func TestHealthRoutesOffAnswer501(t *testing.T) {
	mux := http.NewServeMux()
	Register(mux, Options{Version: "t", DB: fakeDB{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Authn: asRole(auth.RoleAdmin)})
	for _, p := range []string{"/api/v1/health-thresholds", "/api/v1/runners/x/job-stats", "/api/v1/machines/x/health", "/api/v1/machines/x/job-stats"} {
		if rec := do(mux, http.MethodGet, p, ""); rec.Code != http.StatusNotImplemented {
			t.Errorf("%s: %d", p, rec.Code)
		}
	}
}
