package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/auth"
	"github.com/luis-mc/ci-runners-shared-pool/internal/machinehealth"
)

func (f *healthFake) Uptime(_ context.Context, id string) (machinehealth.Uptime, error) {
	at := time.Date(2026, 11, 22, 11, 0, 0, 0, time.UTC)
	u75, t50 := 75.0, 50.0
	return machinehealth.Uptime{MachineID: id, Watched: true, Since: at.Add(-3 * time.Hour),
		Windows: []machinehealth.UptimeWindow{
			{Name: "24h", Covered: 4 * time.Hour, Online: 3 * time.Hour, Taking: 2 * time.Hour, UptimePct: &u75, TakingPct: &t50},
			{Name: "7d", Covered: 4 * time.Hour},
			{Name: "30d", Covered: 4 * time.Hour},
		},
		Periods: []machinehealth.Period{
			{Start: at, Reason: "on_battery"},
			{Start: at.Add(-2 * time.Hour), End: at.Add(-time.Hour), Offline: true, Reason: machinehealth.OfflineHeartbeatLost},
		}}, nil
}

func TestMachineUptimeRoute(t *testing.T) {
	mux, id := healthMux(t, auth.RoleViewer, &healthFake{})
	rec := do(mux, http.MethodGet, "/api/v1/machines/"+id+"/uptime", "")
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	var out struct {
		Watched bool `json:"watched"`
		Windows []struct {
			Window  string   `json:"window"`
			Covered int64    `json:"covered_seconds"`
			Uptime  *float64 `json:"uptime_pct"`
		} `json:"windows"`
		Periods []struct {
			Offline bool    `json:"offline"`
			Reason  string  `json:"reason"`
			Ended   *string `json:"ended_at"`
		} `json:"periods"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if !out.Watched || len(out.Windows) != 3 || out.Windows[0].Window != "24h" || out.Windows[0].Covered != 4*3600 ||
		out.Windows[0].Uptime == nil || *out.Windows[0].Uptime != 75 {
		t.Fatalf("%s", rec.Body)
	}
	if len(out.Periods) != 2 || out.Periods[0].Reason != "on_battery" || out.Periods[0].Offline || out.Periods[0].Ended != nil ||
		!out.Periods[1].Offline || out.Periods[1].Ended == nil {
		t.Fatalf("%s", rec.Body)
	}
	if rec := do(mux, http.MethodGet, "/api/v1/machines/nobody/uptime", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown machine: %d", rec.Code)
	}
}

func TestMachineUptimeOffAnswers501(t *testing.T) {
	mux, id := healthMux(t, auth.RoleViewer, nil)
	if rec := do(mux, http.MethodGet, "/api/v1/machines/"+id+"/uptime", ""); rec.Code != http.StatusNotImplemented {
		t.Fatalf("%d", rec.Code)
	}
}
