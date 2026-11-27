package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/auth"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/machines"
)

type sshStub struct{ ran []string }

func (s *sshStub) HostKey(context.Context, machines.SSHTarget) (string, error) {
	return "SHA256:hostkey", nil
}

func (s *sshStub) Run(_ context.Context, _ machines.SSHTarget, cmd string) (string, error) {
	s.ran = append(s.ran, cmd)
	return "", nil
}

func sshMux(t *testing.T) (*http.ServeMux, *machines.Service, *sshStub) {
	t.Helper()
	stub := &sshStub{}
	svc := machines.New(machines.Options{Repo: machines.NewMemRepo(), ControllerAddress: "pool.lan", AgentPort: 8461, Fingerprint: strings.Repeat("ab", 32), SSH: stub})
	mux := http.NewServeMux()
	Register(mux, Options{Version: "test", DB: fakeDB{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Machines: svc, CA: fakeCA{}, Authn: asRole(auth.RoleOwner)})
	return mux, svc, stub
}

func TestSSHJoinConfirmsHostKeyThenRuns(t *testing.T) { // RES3
	mux, _, stub := sshMux(t)
	body := map[string]any{"os": "linux", "host": "build1.lan", "user": "ci", "identity": "KEYFILE-CONTENT"}
	rec := doM(t, mux, "POST", "/api/v1/join-codes/ssh", body)
	if rec.Code != 409 || !strings.Contains(rec.Body.String(), "SHA256:hostkey") || len(stub.ran) != 0 {
		t.Fatalf("first call must ask for the host key: %d %s", rec.Code, rec.Body)
	}
	body["host_fingerprint"] = "SHA256:hostkey"
	rec = doM(t, mux, "POST", "/api/v1/join-codes/ssh", body)
	if rec.Code != 201 || len(stub.ran) != 1 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	for _, leak := range []string{"KEYFILE-CONTENT", "--join-code"} {
		if strings.Contains(rec.Body.String(), leak) {
			t.Fatalf("answer leaks %q: %s", leak, rec.Body)
		}
	}
	var jc struct{ ID, State, Progress string }
	_ = json.Unmarshal(rec.Body.Bytes(), &jc)
	if jc.State != "waiting" || jc.Progress != "waiting" {
		t.Fatalf("%+v", jc)
	}
	body["os"] = "windows"
	if rec = doM(t, mux, "POST", "/api/v1/join-codes/ssh", body); rec.Code != 400 && rec.Code != 422 {
		t.Fatalf("windows has no SSH join: %d", rec.Code)
	}
}

func TestJoinCodeProgressWaitingConnectedReady(t *testing.T) { // RES3
	mux, svc, _ := sshMux(t)
	rec := doM(t, mux, "POST", "/api/v1/join-codes", map[string]any{})
	var created struct {
		Code     string
		JoinCode struct{ ID, Progress string } `json:"join_code"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	get := func() (state, progress string) {
		var jc struct{ State, Progress string }
		_ = json.Unmarshal(doM(t, mux, "GET", "/api/v1/join-codes/"+created.JoinCode.ID, nil).Body.Bytes(), &jc)
		return jc.State, jc.Progress
	}
	if st, pr := get(); st != "waiting" || pr != "waiting" {
		t.Fatalf("waiting: %s %s", st, pr)
	}
	res, err := svc.Join(context.Background(), machines.JoinInput{Code: created.Code, Facts: machines.Facts{HostName: "box", OS: "linux", Arch: "x64"}})
	if err != nil {
		t.Fatal(err)
	}
	if st, pr := get(); st != "joined" || pr != "connected" {
		t.Fatalf("connected: %s %s", st, pr)
	}
	if err := svc.MarkSeen(context.Background(), res.MachineID); err != nil {
		t.Fatal(err)
	}
	if st, pr := get(); st != "joined" || pr != "ready" {
		t.Fatalf("ready: %s %s", st, pr)
	}
	var second struct {
		JoinCode struct{ ID string } `json:"join_code"`
	}
	_ = json.Unmarshal(doM(t, mux, "POST", "/api/v1/join-codes", map[string]any{}).Body.Bytes(), &second)
	created.JoinCode.ID = second.JoinCode.ID
	doM(t, mux, "DELETE", "/api/v1/join-codes/"+second.JoinCode.ID, nil)
	if _, pr := get(); pr != "" {
		t.Fatalf("a withdrawn code has no progress: %q", pr)
	}
}
