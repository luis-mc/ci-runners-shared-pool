package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/assign"
	"github.com/luis-mc/ci-runners-shared-pool/internal/auth"
	"github.com/luis-mc/ci-runners-shared-pool/internal/scheduler/admit"
)

type fakeLeases struct{ states []assign.State }

func (f *fakeLeases) List(_ context.Context, states ...assign.State) ([]assign.Lease, error) {
	f.states = states
	return []assign.Lease{{JobID: 5, RunID: 50, Repository: "o/r", JobName: "e2e", Labels: []string{"linux"}, State: assign.Waiting,
		Req: admit.Req{CPUs: 6, MemoryGiB: 4}, QueuedAt: time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC),
		Reason: "waiting for room on M1: needs 6 CPU, 5 free", ReasonCode: assign.CodePool}}, nil
}

func TestListAssignLeasesShowsTheReason(t *testing.T) {
	f := &fakeLeases{}
	mux := http.NewServeMux()
	Register(mux, Options{Version: "t", DB: fakeDB{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Assign: f, Authn: asRole(auth.RoleViewer)})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/assign/leases?state=waiting", nil))
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var out struct {
		Leases []struct {
			JobID      int64  `json:"job_id"`
			Reason     string `json:"reason"`
			ReasonCode string `json:"reason_code"`
			Cpus       int    `json:"cpus"`
		} `json:"leases"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || len(out.Leases) != 1 {
		t.Fatalf("body: %s", rec.Body)
	}
	l := out.Leases[0]
	if l.JobID != 5 || l.ReasonCode != "pool" || l.Cpus != 6 || l.Reason != "waiting for room on M1: needs 6 CPU, 5 free" {
		t.Fatalf("lease: %+v", l)
	}
	if len(f.states) != 1 || f.states[0] != assign.Waiting {
		t.Fatalf("the state filter reaches the store: %v", f.states)
	}
}
