package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/internal/api/gen"
	"github.com/luis-mc/ci-runners-shared-pool/internal/auth"
	agentv1 "github.com/luis-mc/ci-runners-shared-pool/internal/gen/agent/v1"
	"github.com/luis-mc/ci-runners-shared-pool/internal/machines"
)

func poolMux(t *testing.T) (*http.ServeMux, string) {
	t.Helper()
	_, mux, id := poolSvcMux(t)
	return mux, id
}

func poolSvcMux(t *testing.T) (*machines.Service, *http.ServeMux, string) {
	t.Helper()
	svc := machines.New(machines.Options{Repo: machines.NewMemRepo(), ControllerAddress: "pool.lan", AgentPort: 8461, Fingerprint: strings.Repeat("ab", 32)})
	code, err := svc.CreateJoinCode(context.Background(), "owner", 0)
	if err != nil {
		t.Fatal(err)
	}
	res, err := svc.Join(context.Background(), machines.JoinInput{Code: code.Code, Facts: machines.Facts{HostName: "box-1", OS: "linux", Arch: "x64", Threads: 8, MemoryBytes: 32 << 30}})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.HandleLoad(context.Background(), res.MachineID, &agentv1.LoadReport{MemoryPct: 25, MemoryKnown: true}); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	Register(mux, Options{Version: "test", DB: fakeDB{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Machines: svc, CA: fakeCA{}, Authn: asRole(auth.RoleOwner)})
	return svc, mux, res.MachineID
}

// machines R5.58 over the API: saved when it fits, 400 with the reason when it
// does not, 409 needs_confirmation for a shrink until confirm_shrink is sent.
func TestPoolSizeThroughTheAPI(t *testing.T) {
	mux, id := poolMux(t)
	rec := doM(t, mux, "PATCH", "/api/v1/machines/"+id, map[string]any{"pool_memory_gib": 16.5, "pool_cpus": 4})
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	var m gen.Machine
	_ = json.Unmarshal(rec.Body.Bytes(), &m)
	if m.Settings.PoolMemoryGib == nil || *m.Settings.PoolMemoryGib != 16.5 || *m.Settings.PoolCpus != 4 {
		t.Fatalf("settings = %+v", m.Settings)
	}
	rec = doM(t, mux, "PATCH", "/api/v1/machines/"+id, map[string]any{"pool_memory_gib": 30, "pool_cpus": 4})
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "memory budget") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	rec = doM(t, mux, "PATCH", "/api/v1/machines/"+id, map[string]any{"pool_memory_gib": 8, "pool_cpus": 4, "confirm": true})
	if rec.Code != 409 {
		t.Fatalf("a shrink needs its own yes: %d %s", rec.Code, rec.Body)
	}
	assertEnvelope(t, rec, CodeNeedsConfirmation)
	rec = doM(t, mux, "PATCH", "/api/v1/machines/"+id, map[string]any{"pool_memory_gib": 8, "pool_cpus": 4, "confirm_shrink": true})
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

// machines R5.58: the dry run behind the console's Review step answers the
// result and the nearest sizes and saves nothing.
func TestPoolSizeCheckDryRun(t *testing.T) {
	mux, id := poolMux(t)
	check := func(mem float64, cpus int) gen.PoolSizeCheck {
		t.Helper()
		rec := doM(t, mux, "POST", "/api/v1/machines/"+id+"/pool-size-check", map[string]any{"pool_memory_gib": mem, "pool_cpus": cpus})
		if rec.Code != 200 {
			t.Fatalf("%d %s", rec.Code, rec.Body)
		}
		var c gen.PoolSizeCheck
		_ = json.Unmarshal(rec.Body.Bytes(), &c)
		return c
	}
	if c := check(16.5, 4); !c.Known || !c.Ok || c.NeedsShrinkConfirm {
		t.Fatalf("a size that fits: %+v", c)
	}
	c := check(30, 4)
	if !c.Known || c.Ok || c.Reason == nil || len(c.Nearest) == 0 || c.Short == nil || *c.Short != "memory" || c.RoomGib == nil {
		t.Fatalf("a size over the budget names the budget and the nearest size: %+v", c)
	}
	// LOOK22: room 19.2 GiB, CPU budget 4: half is 9 GiB and 2 CPUs, three quarters 14 GiB and 3 CPUs.
	if c.RecommendedHalf == nil || c.RecommendedHalf.MemoryGib != 9 || c.RecommendedHalf.Cpus != 2 ||
		c.RecommendedThreeQuarters == nil || c.RecommendedThreeQuarters.MemoryGib != 14 || c.RecommendedThreeQuarters.Cpus != 3 {
		t.Fatalf("recommended sizes: %+v %+v", c.RecommendedHalf, c.RecommendedThreeQuarters)
	}
	if c := check(0, 0); c.RecommendedHalf != nil {
		t.Fatalf("clearing carries no recommendation: %+v", c)
	}
	if rec := doM(t, mux, "GET", "/api/v1/machines/"+id, nil); strings.Contains(rec.Body.String(), `"pool_cpus":4`) {
		t.Fatalf("the check saved a size: %s", rec.Body)
	}
	if rec := doM(t, mux, "PATCH", "/api/v1/machines/"+id, map[string]any{"pool_memory_gib": 16.5, "pool_cpus": 4}); rec.Code != 200 {
		t.Fatal(rec.Body)
	}
	if c := check(8, 4); !c.Ok || !c.NeedsShrinkConfirm || c.CurrentCpus != 4 || c.CurrentMemoryGib != 16.5 {
		t.Fatalf("a lower size says it needs the explicit yes: %+v", c)
	}
	if c := check(0, 0); !c.Known || !c.Ok || !c.NeedsShrinkConfirm {
		t.Fatalf("clearing is a shrink: %+v", c)
	}
	if rec := doM(t, mux, "POST", "/api/v1/machines/"+id+"/pool-size-check", map[string]any{"pool_memory_gib": 8, "pool_cpus": 0}); rec.Code != 400 {
		t.Fatalf("memory without CPUs is refused: %d", rec.Code)
	}
}

// machines R5.68, OPS61: before 24 h of host-use samples the check says it is
// measuring and keeps today's fixed share; with them it recommends from the
// p95 and warns, never blocks, when a Custom size leaves the PC less than that.
func TestPoolSizeCheckSaysMeasuredOrMeasuring(t *testing.T) {
	svc, mux, id := poolSvcMux(t)
	check := func(mem float64, cpus int) gen.PoolSizeCheck {
		t.Helper()
		rec := doM(t, mux, "POST", "/api/v1/machines/"+id+"/pool-size-check", map[string]any{"pool_memory_gib": mem, "pool_cpus": cpus})
		if rec.Code != 200 {
			t.Fatalf("%d %s", rec.Code, rec.Body)
		}
		var c gen.PoolSizeCheck
		_ = json.Unmarshal(rec.Body.Bytes(), &c)
		return c
	}
	report := func(spanH int64, p95GiB int64) {
		t.Helper()
		if err := svc.HandleLoad(context.Background(), id, &agentv1.LoadReport{MemoryPct: 25, MemoryKnown: true,
			HostUseReady: spanH >= 24, HostUseP95Bytes: p95GiB << 30, HostUseSpanSeconds: spanH * 3600}); err != nil {
			t.Fatal(err)
		}
	}
	report(6, 0)
	c := check(8, 4)
	if c.Basis == nil || *c.Basis != gen.Measuring || c.HostSpanHours == nil || *c.HostSpanHours != 6 || c.HostP95Gib != nil ||
		c.RecommendedHalf.MemoryGib != 9 {
		t.Fatalf("measuring keeps the fixed share: %+v", c)
	}
	// Ready flag set but under 24 h: still measuring (the controller does not trust a short span).
	if err := svc.HandleLoad(context.Background(), id, &agentv1.LoadReport{MemoryPct: 25, MemoryKnown: true, HostUseReady: true, HostUseP95Bytes: 5 << 30, HostUseSpanSeconds: 3600}); err != nil {
		t.Fatal(err)
	}
	if c := check(8, 4); *c.Basis != gen.Measuring {
		t.Fatalf("%+v", c)
	}
	// 32 GiB, budget 85%: 27.2 - 10 - 1 = 16.2 GiB room; live room is 19.2, so the presets use 16.2.
	report(30, 10)
	c = check(16, 4)
	if *c.Basis != gen.Measured || c.HostP95Gib == nil || *c.HostP95Gib != 10 || c.MeasuredRoomGib == nil || *c.MeasuredRoomGib < 16.19 || *c.MeasuredRoomGib > 16.21 ||
		c.RecommendedHalf.MemoryGib != 8 || c.LeavesLessThanHost == nil || *c.LeavesLessThanHost || !c.Ok {
		t.Fatalf("measured: %+v", c)
	}
	// 19 GiB fits the live room (19.2) and leaves 13 GiB, above the p95 of 10: no warning. With a p95 of 14 it warns and still fits.
	report(30, 14)
	if c := check(19, 4); !c.Ok || c.LeavesLessThanHost == nil || !*c.LeavesLessThanHost || *c.LeavesGib != 13 {
		t.Fatalf("a warning is not a block: %+v", c)
	}
}
