package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/audit"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/auth"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/update"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/update/lifecycle"
)

type fakeLifecycle struct {
	status    update.LifecycleStatus
	installed []string
	remote    []bool
	checkedBy []string
}

func (f *fakeLifecycle) Status(context.Context) update.LifecycleStatus { return f.status }
func (f *fakeLifecycle) Check(_ context.Context, actor string) (update.LifecycleStatus, error) {
	f.checkedBy = append(f.checkedBy, actor)
	return f.status, nil
}
func (f *fakeLifecycle) Install(_ context.Context, actor string, remote bool, version string) (update.LifecycleStatus, error) {
	if version != "" && version != f.status.Available {
		return f.status, update.ErrNoOffer
	}
	f.installed, f.remote = append(f.installed, actor), append(f.remote, remote)
	return f.status, nil
}
func (f *fakeLifecycle) SetPolicy(_ context.Context, p string, w *lifecycle.Window) (update.LifecycleStatus, error) {
	f.status.Policy, f.status.Window = p, w
	return f.status, nil
}
func (f *fakeLifecycle) History(context.Context) ([]update.HistoryRow, error) {
	return []update.HistoryRow{{At: time.Unix(1, 0).UTC(), Actor: "luis", Action: "install", Version: "1.5.0", Result: "ok"}}, nil
}

type fakeFleet struct{ asked []string }

func (f *fakeFleet) Fleet(context.Context) ([]FleetMachine, error) {
	return []FleetMachine{{ID: "m1", Name: "here", Version: "1.4.0", State: "up_to_date", Local: true}, {ID: "m2", Name: "there", Version: "1.4.0", State: "up_to_date"}}, nil
}
func (f *fakeFleet) AskUpdate(id, _ string) error { f.asked = append(f.asked, id); return nil }

type fakeRemove struct{ got []bool }

func (f *fakeRemove) Remove(_ context.Context, _ audit.Actor, _ string, del bool) error {
	f.got = append(f.got, del)
	return nil
}

func lifecycleMux(l *fakeLifecycle, fl *fakeFleet, rm *fakeRemove) *http.ServeMux {
	mux := http.NewServeMux()
	Register(mux, Options{Version: "1.4.0", DB: okPinger{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Update: &fakeUpdate{}, Lifecycle: l, Fleet: fl, Remove: rm, Authn: asRole(auth.RoleOwner)})
	return mux
}

func post(mux *http.ServeMux, target, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, target, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, r)
	return rec
}

func TestLifecycleStatusShape(t *testing.T) {
	l := &fakeLifecycle{status: update.LifecycleStatus{State: "up_to_date", Current: "1.4.0", Policy: "automatic_when_idle"}}
	rec := doReq(lifecycleMux(l, &fakeFleet{}, &fakeRemove{}), "GET", "/api/v1/update/status")
	var got map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	for _, k := range []string{"state", "current", "available", "required", "policy", "window", "next_check_at", "failures", "waiting_for_jobs"} {
		if _, ok := got[k]; !ok {
			t.Errorf("status has no %q: %s", k, rec.Body)
		}
	}
	if got["window"] != nil || got["failures"] != nil || got["available"] != nil {
		t.Errorf("empty values are null: %s", rec.Body)
	}
}

func TestLifecycleInstallLocalRemoteAndPolicy(t *testing.T) {
	l := &fakeLifecycle{status: update.LifecycleStatus{State: "review", Current: "1.4.0", Available: "1.5.0"}}
	fl := &fakeFleet{}
	mux := lifecycleMux(l, fl, &fakeRemove{})
	if rec := post(mux, "/api/v1/update/install", `{"version":"1.5.0"}`); rec.Code != 202 || len(l.installed) != 1 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if rec := post(mux, "/api/v1/update/install", `{"version":"9.9.9"}`); rec.Code != http.StatusConflict {
		t.Fatalf("wrong version: %d %s", rec.Code, rec.Body)
	}
	if rec := post(mux, "/api/v1/update/install", `{"version":"1.5.0","machine_id":"m2"}`); rec.Code != 202 || len(fl.asked) != 1 || fl.asked[0] != "m2" {
		t.Fatalf("remote machine: %d %s %v", rec.Code, rec.Body, fl.asked)
	}
	if rec := post(mux, "/api/v1/update/policy", `{"policy":"ask_first","window":{"start":"01:00","end":"05:00"}}`); rec.Code != 200 || l.status.Policy != "ask_first" || l.status.Window == nil {
		t.Fatalf("policy: %d %s", rec.Code, rec.Body)
	}
}

func TestLifecycleAliasesAndHistoryFleet(t *testing.T) {
	l := &fakeLifecycle{status: update.LifecycleStatus{State: "up_to_date", Current: "1.4.0"}}
	mux := lifecycleMux(l, &fakeFleet{}, &fakeRemove{})
	if rec := post(mux, "/api/v1/update/check", ``); rec.Code != 200 || len(l.checkedBy) != 1 {
		t.Fatalf("the old check path audits through the lifecycle: %d %v", rec.Code, l.checkedBy)
	}
	if rec := doReq(mux, "GET", "/api/v1/update/history"); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"action":"install"`) {
		t.Fatalf("history: %d %s", rec.Code, rec.Body)
	}
	if rec := doReq(mux, "GET", "/api/v1/update/fleet"); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"machine_id":"m2"`) {
		t.Fatalf("fleet: %d %s", rec.Code, rec.Body)
	}
}

func TestRemoveNeedsAChoice(t *testing.T) {
	rm := &fakeRemove{}
	mux := lifecycleMux(&fakeLifecycle{}, &fakeFleet{}, rm)
	for _, body := range []string{`{}`, `{"delete_data":null}`, `{"machine_id":"m1"}`} {
		if rec := post(mux, "/api/v1/remove", body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d", body, rec.Code)
		}
	}
	if len(rm.got) != 0 {
		t.Fatal("a remove without a choice ran")
	}
	if rec := post(mux, "/api/v1/remove", `{"delete_data":false}`); rec.Code != 202 || len(rm.got) != 1 || rm.got[0] {
		t.Fatalf("keep my data: %d %v", rec.Code, rm.got)
	}
	if rec := post(mux, "/api/v1/remove", `{"delete_data":true}`); rec.Code != 202 || !rm.got[1] {
		t.Fatalf("delete my data too: %d %v", rec.Code, rm.got)
	}
}
