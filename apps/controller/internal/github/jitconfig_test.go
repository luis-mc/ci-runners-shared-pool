package github

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

func TestJITConfigNeverStoredOrLogged(t *testing.T) { // R5.34
	e := newEnv(t)
	var logs strings.Builder
	e.client.cfg.Logger = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	e.fake.AddInstall(10, "alice", "User", 1)
	e.fake.AddRepo(100, 10, "alice", "demo", true, 1)
	ctx := context.Background()

	scope := RunnerScope{Owner: "alice", Repo: "demo"}
	cfg, err := e.client.GenerateJITConfig(ctx, 10, scope, JITRequest{Name: "crsp-job-1", Labels: []string{"self-hosted", "linux"}})
	if err != nil || cfg.Encoded() == "" || cfg.RunnerID == 0 {
		t.Fatalf("jit config: %+v %v", cfg, err)
	}
	if !e.fake.HasJITConfig(cfg.Encoded()) {
		t.Fatal("the fake did not issue this config")
	}
	reqs := e.fake.JITRequests()
	if len(reqs) != 1 || reqs[0].Name != "crsp-job-1" || reqs[0].Group != 1 || len(reqs[0].Labels) != 2 {
		t.Fatalf("request seen by GitHub: %+v", reqs)
	}
	if e.fake.Count("POST", "/repos/alice/demo/actions/runners/generate-jitconfig") != 1 {
		t.Fatal("repository scope must call the repository endpoint")
	}
	// Every way of printing the value is masked.
	raw, _ := json.Marshal(cfg)
	for _, s := range []string{cfg.String(), fmt.Sprintf("%v %+v %#v", cfg, cfg, cfg), logs.String(), string(raw)} {
		if strings.Contains(s, cfg.Encoded()) {
			t.Fatalf("the JIT config reached text: %q", s)
		}
	}
}

func TestJITConfigOrganizationScopeAndName(t *testing.T) { // R5.34
	e := newEnv(t)
	e.fake.AddInstall(10, "acme", "Organization", 1)
	ctx := context.Background()
	if _, err := e.client.GenerateJITConfig(ctx, 10, RunnerScope{Owner: "acme", GroupID: 7}, JITRequest{Name: "n1"}); err != nil {
		t.Fatal(err)
	}
	if e.fake.Count("POST", "/orgs/acme/actions/runners/generate-jitconfig") != 1 {
		t.Fatal("organization scope must call the organization endpoint")
	}
	if got := e.fake.JITRequests(); got[0].Group != 7 {
		t.Fatalf("group %d, want 7", got[0].Group)
	}
	if _, err := e.client.GenerateJITConfig(ctx, 10, RunnerScope{Owner: "acme"}, JITRequest{}); err == nil {
		t.Fatal("a JIT config with no runner name must be refused")
	}
}
