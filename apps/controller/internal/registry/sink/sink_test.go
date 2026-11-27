package sink

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	agentv1 "github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/gen/agent/v1"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/profiles"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry"
)

const goodKey = "37fef5dbb41b"

type fakeLocator struct {
	ans registry.EnsureAnswer
	err error
}

func (f fakeLocator) Ensure(context.Context, string, registry.Key, string) (registry.EnsureAnswer, error) {
	return f.ans, f.err
}

type fakeTokens struct {
	got  []registry.Scope
	err  error
	for_ string
}

func (f *fakeTokens) Issue(_ context.Context, machineID string, want []registry.Scope) (registry.Token, error) {
	f.got, f.for_ = want, machineID
	if f.err != nil {
		return registry.Token{}, f.err
	}
	return registry.Token{Value: "tok", ExpiresAt: time.UnixMilli(1_700_000_600_000)}, nil
}
func (f *fakeTokens) Verify(context.Context, string) (string, []registry.Scope, error) {
	return "", nil, errors.New("no")
}

type fakeProfiles map[string]profiles.Profile

func (f fakeProfiles) Get(_ context.Context, id string) (profiles.Profile, error) {
	if p, ok := f[id]; ok {
		return p, nil
	}
	return profiles.Profile{}, profiles.ErrNotFound
}

type fakeRecorder struct {
	pulls  []registry.PullRecord
	pushes []registry.PushRecord
}

func (f *fakeRecorder) RecordPull(_ context.Context, r registry.PullRecord) error {
	f.pulls = append(f.pulls, r)
	return nil
}
func (f *fakeRecorder) RecordPush(_ context.Context, r registry.PushRecord) error {
	f.pushes = append(f.pushes, r)
	return nil
}

type fakePromoter struct {
	got registry.Staged
	err error
}

func (f *fakePromoter) Promote(_ context.Context, s registry.Staged) (registry.Image, error) {
	f.got = s
	if f.err != nil {
		return registry.Image{}, f.err
	}
	return registry.Image{ID: "img_1"}, nil
}

type fakeOutcomes struct {
	promoted bool
	id, why  string
	called   bool
}

func (f *fakeOutcomes) SetPushOutcome(_ context.Context, _ string, promoted bool, id, why string) error {
	f.called, f.promoted, f.id, f.why = true, promoted, id, why
	return nil
}

const keyJSON = `{"profile":"node-web","stack_hash":"3ce86298410301e478baa185c81f75e9d8e02dcf22633e16d02d5ee627d67eb0","arch":"amd64","base_digest":"sha256:1111111111111111111111111111111111111111111111111111111111111111","pins":{},"toolchains":[]}`

// A sink with no parts answers as a controller without a registry.
func TestEmptySinkSaysNotFoundAndRefusesTokens(t *testing.T) {
	s := &Sink{}
	a := s.EnsureImage(context.Background(), "m1", &agentv1.EnsureImage{Key: goodKey, Arch: "amd64"})
	if a.GetOutcome() != agentv1.EnsureImageAnswer_OUTCOME_NOT_FOUND || a.GetStagingName() != "" {
		t.Fatalf("answer %+v", a)
	}
	tok := s.RegistryToken(context.Background(), "m1", &agentv1.RegistryTokenRequest{Action: agentv1.RegistryTokenRequest_ACTION_PULL, Names: []string{"crsp/std/x"}})
	if tok.GetRefused() == "" || tok.GetToken() != "" {
		t.Fatalf("token %+v", tok)
	}
	s.PullReport(context.Background(), "m1", &agentv1.PullReport{PullId: "p"})
	s.PushReport(context.Background(), "m1", &agentv1.PushReport{PushId: "p"})
}

func TestEnsureFoundBusyAndNotFoundWithStaging(t *testing.T) {
	ctx := context.Background()
	tokens := &fakeTokens{}
	s := &Sink{Tokens: tokens, Host: "ctl:8443", Profiles: fakeProfiles{
		"node-web": {ID: "node-web", Kind: registry.KindStandard},
		"mine":     {ID: "mine", Kind: registry.KindCustom},
	}}
	req := &agentv1.EnsureImage{Key: goodKey, Arch: "x64", KeyInputJson: keyJSON, Repository: "Acme/Docs"}

	s.Locator = fakeLocator{ans: registry.EnsureAnswer{Found: true, Name: "crsp/std/node-web", Manifest: "sha256:aa", Size: 9}}
	a := s.EnsureImage(ctx, "m1", req)
	if a.GetOutcome() != agentv1.EnsureImageAnswer_OUTCOME_FOUND || a.GetName() != "crsp/std/node-web" || a.GetManifestDigest() != "sha256:aa" || a.GetRegistryHost() != "ctl:8443" {
		t.Fatalf("found %+v", a)
	}

	s.Locator = fakeLocator{ans: registry.EnsureAnswer{Busy: true}}
	if a := s.EnsureImage(ctx, "m1", req); a.GetOutcome() != agentv1.EnsureImageAnswer_OUTCOME_BUSY || a.GetRetryAfterMs() <= 0 {
		t.Fatalf("busy %+v", a)
	}
	s.Locator = fakeLocator{err: errors.New("db")}
	if a := s.EnsureImage(ctx, "m1", req); a.GetOutcome() != agentv1.EnsureImageAnswer_OUTCOME_BUSY {
		t.Fatalf("an error is busy, never a build: %+v", a)
	}

	s.Locator = fakeLocator{}
	a = s.EnsureImage(ctx, "m1", req)
	if a.GetOutcome() != agentv1.EnsureImageAnswer_OUTCOME_NOT_FOUND || a.GetTargetName() != "crsp/std/node-web" || a.GetStagingName() != "crsp/incoming/m1/std/node-web" {
		t.Fatalf("standard %+v", a)
	}
	custom := strings.Replace(keyJSON, "node-web", "mine", 1)
	a = s.EnsureImage(ctx, "m1", &agentv1.EnsureImage{Key: goodKey, Arch: "amd64", KeyInputJson: custom, Repository: "Acme/Docs"})
	if a.GetTargetName() != "crsp/custom/acme-docs/mine" || a.GetStagingName() != "crsp/incoming/m1/custom/acme-docs/mine" {
		t.Fatalf("custom %+v", a)
	}
	// A bad key is never answered with a staging name.
	if a := s.EnsureImage(ctx, "m1", &agentv1.EnsureImage{Key: "nope"}); a.GetStagingName() != "" {
		t.Fatalf("bad key %+v", a)
	}
}

func TestTokenScopesAndRefusal(t *testing.T) {
	tokens := &fakeTokens{}
	s := &Sink{Tokens: tokens}
	tok := s.RegistryToken(context.Background(), "m1", &agentv1.RegistryTokenRequest{Action: agentv1.RegistryTokenRequest_ACTION_PUSH, Names: []string{"crsp/incoming/m1/std/x"}})
	if tok.GetToken() != "tok" || tok.GetExpiresAtUnixMs() != 1_700_000_600_000 || tokens.for_ != "m1" {
		t.Fatalf("token %+v", tok)
	}
	if len(tokens.got) != 1 || tokens.got[0].Name != "crsp/incoming/m1/std/x" || tokens.got[0].Actions[0] != "push" {
		t.Fatalf("scopes %+v", tokens.got)
	}
	tokens.err = errors.New("a machine pushes only to its own staging name")
	if tok := s.RegistryToken(context.Background(), "m1", &agentv1.RegistryTokenRequest{Action: agentv1.RegistryTokenRequest_ACTION_PUSH, Names: []string{"crsp/std/x"}}); tok.GetRefused() == "" || tok.GetToken() != "" {
		t.Fatalf("refusal %+v", tok)
	}
	if tok := s.RegistryToken(context.Background(), "m1", &agentv1.RegistryTokenRequest{}); tok.GetRefused() == "" {
		t.Fatalf("no action %+v", tok)
	}
}

func TestPushedReportPromotesAndTellsTheRecords(t *testing.T) {
	rec, prom, out := &fakeRecorder{}, &fakePromoter{}, &fakeOutcomes{}
	s := &Sink{Recorder: rec, Promoter: prom, Outcomes: out, Profiles: fakeProfiles{"node-web": {ID: "node-web", Kind: registry.KindStandard, BaseName: "ubuntu:24.04"}}}
	rep := &agentv1.PushReport{PushId: "ps1", Key: goodKey, StagingName: "crsp/incoming/m1/std/node-web", TargetName: "crsp/std/node-web",
		ManifestDigest: "sha256:aa", State: agentv1.PushReport_STATE_PUSHED, KeyInputJson: keyJSON, Repository: "acme/docs", CommitSha: "c0ffee", Branch: "main"}
	s.PushReport(context.Background(), "m1", rep)
	if len(rec.pushes) != 1 || rec.pushes[0].State != "pushed" || rec.pushes[0].MachineID != "m1" {
		t.Fatalf("pushes %+v", rec.pushes)
	}
	if prom.got.Target != "crsp/std/node-web" || prom.got.Labels["io.crsp.key"] != goodKey || prom.got.Labels["io.crsp.profile"] != "node-web" {
		t.Fatalf("staged %+v", prom.got)
	}
	if !out.called || !out.promoted || out.id != "img_1" {
		t.Fatalf("outcome %+v", out)
	}

	// A refusal is recorded with its reason and is not an error.
	prom.err = errors.New("the digest differs from the staged manifest")
	s.PushReport(context.Background(), "m1", rep)
	if out.promoted || !strings.Contains(out.why, "digest") {
		t.Fatalf("refused outcome %+v", out)
	}

	// A push still moving is recorded and not promoted.
	*out = fakeOutcomes{}
	rep.State = agentv1.PushReport_STATE_PUSHING
	s.PushReport(context.Background(), "m1", rep)
	if out.called {
		t.Fatal("a push that is still moving must not be promoted")
	}
}

func TestPullReportMapsSourceAndState(t *testing.T) {
	rec := &fakeRecorder{}
	s := &Sink{Recorder: rec}
	s.PullReport(context.Background(), "m2", &agentv1.PullReport{PullId: "pl1", Key: goodKey, Arch: "x64", Source: agentv1.PullReport_SOURCE_PULLED,
		State: agentv1.PullReport_STATE_WAITING_CONTROLLER, Repository: "acme/docs", AtUnixMs: 1_700_000_000_000})
	if len(rec.pulls) != 1 {
		t.Fatalf("pulls %+v", rec.pulls)
	}
	p := rec.pulls[0]
	if p.Source != registry.SourcePulled || p.State != "waiting_controller" || p.Arch != "amd64" || p.MachineID != "m2" || p.At.UnixMilli() != 1_700_000_000_000 {
		t.Fatalf("pull %+v", p)
	}
}
