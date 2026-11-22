package repos

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/internal/runners"
)

// runnerAccepts is the GitHub rule: a runner is offered a job only when it has
// every label of the job's runs-on.
func runnerAccepts(runnerLabels, runsOn []string) bool {
	have := map[string]bool{}
	for _, l := range runnerLabels {
		have[strings.ToLower(l)] = true
	}
	for _, l := range runsOn {
		if !have[strings.ToLower(l)] {
			return false
		}
	}
	return true
}

func linuxList(t *testing.T, e *env, repo string) []string {
	t.Helper()
	var v string
	e.fake.Lock(func() { v = e.fake.Variables[repo]["CRSP_RUNS_ON_LINUX"] })
	var out []string
	if err := json.Unmarshal([]byte(v), &out); err != nil {
		t.Fatalf("variable %q: %v", v, err)
	}
	return out
}

func TestEffectiveRunnerKindsByVisibility(t *testing.T) { // github-integration R5.60
	p := DefaultPolicy()
	if p.EffectiveRunnerKinds("public") != RunnerKindsEphemeralOnly || p.RunnerKindsSet() {
		t.Fatal("public defaults to ephemeral only")
	}
	for _, v := range []string{"private", "internal", ""} {
		if p.EffectiveRunnerKinds(v) != RunnerKindsAnyRunner {
			t.Fatalf("%s defaults to any runner", v)
		}
	}
	p.RunnerKinds = RunnerKindsAnyRunner
	if p.EffectiveRunnerKinds("public") != RunnerKindsAnyRunner || !p.RunnerKindsSet() {
		t.Fatal("the owner's choice wins")
	}
	p.RunnerKinds = RunnerKindsEphemeralOnly
	if !p.EphemeralOnly("private") {
		t.Fatal("a private repository can be set to ephemeral only")
	}
}

func TestPolicyUpdateRunnerKinds(t *testing.T) {
	set := func(s string) PolicyUpdate { return PolicyUpdate{RunnerKinds: &s} }
	p, err := set("any_runner").Apply(DefaultPolicy())
	if err != nil || p.RunnerKinds != RunnerKindsAnyRunner {
		t.Fatalf("%+v %v", p, err)
	}
	p, err = set("default").Apply(p)
	if err != nil || p.RunnerKinds != "" || p.RunnerKindsSet() {
		t.Fatalf("default clears the choice: %+v %v", p, err)
	}
	if _, err := set("persistent").Apply(p); err == nil {
		t.Fatal("an unknown kind is refused")
	}
}

func TestRunsOnCarriesEphemeralLabelForPublicByDefault(t *testing.T) { // R5.60
	e := newEnv(t)
	e.world()
	e.turnOn(100, 101)
	pub, priv := linuxList(t, e, "alice/web"), linuxList(t, e, "alice/secret")
	if pub[len(pub)-1] != runners.EphemeralLabel {
		t.Fatalf("public: %v", pub)
	}
	for _, l := range priv {
		if l == runners.EphemeralLabel {
			t.Fatalf("private default carries the label: %v", priv)
		}
	}
	// A persistent runner has no ephemeral label, so it is never offered the public job.
	persistent := []string{"self-hosted", "linux", "x64", "docker", "crsp-home"}
	if runnerAccepts(persistent, pub) {
		t.Fatal("a persistent runner must not take a public repository's job")
	}
	if !runnerAccepts(persistent, priv) {
		t.Fatal("a persistent runner takes a private repository's job")
	}
	if !runnerAccepts(append(append([]string{}, persistent...), runners.EphemeralLabel), pub) {
		t.Fatal("an ephemeral runner takes the public job")
	}
}

func TestChangingRunnerKindsRewritesVariables(t *testing.T) { // R5.60
	e := newEnv(t)
	e.world()
	e.turnOn(100, 101)
	ctx := context.Background()
	anyRunner, def, eph := RunnerKindsAnyRunner, RunnerKindsDefault, RunnerKindsEphemeralOnly
	if _, err := e.svc.UpdatePolicy(ctx, adminP, 100, PolicyUpdate{RunnerKinds: &anyRunner}); err != nil {
		t.Fatal(err)
	}
	for _, l := range linuxList(t, e, "alice/web") {
		if l == runners.EphemeralLabel {
			t.Fatal("any runner omits the label")
		}
	}
	if _, err := e.svc.UpdatePolicy(ctx, adminP, 100, PolicyUpdate{RunnerKinds: &def}); err != nil {
		t.Fatal(err)
	}
	if l := linuxList(t, e, "alice/web"); l[len(l)-1] != runners.EphemeralLabel {
		t.Fatalf("default of a public repository: %v", l)
	}
	if _, err := e.svc.UpdatePolicy(ctx, adminP, 101, PolicyUpdate{RunnerKinds: &eph}); err != nil {
		t.Fatal(err)
	}
	if l := linuxList(t, e, "alice/secret"); l[len(l)-1] != runners.EphemeralLabel {
		t.Fatalf("a private repository set to ephemeral only: %v", l)
	}
}

type ephemeralSource struct {
	has bool
	err error
}

func (s ephemeralSource) EphemeralCapacity(context.Context) (bool, error) { return s.has, s.err }

func TestViewEphemeralCapacity(t *testing.T) { // R5.60
	e := newEnv(t)
	e.world()
	e.turnOn(100, 101)
	ctx := context.Background()
	get := func(id int64) *bool {
		v, err := e.svc.Get(ctx, adminP, id)
		if err != nil {
			t.Fatal(err)
		}
		return v.EphemeralCapacity
	}
	if get(100) != nil {
		t.Fatal("no source: nothing is claimed")
	}
	e.svc.SetEphemeral(ephemeralSource{has: false})
	if c := get(100); c == nil || *c {
		t.Fatal("a public repository with no ephemeral capacity")
	}
	if get(101) != nil {
		t.Fatal("a repository that may use any runner makes no claim")
	}
	e.svc.SetEphemeral(ephemeralSource{has: true})
	if c := get(100); c == nil || !*c {
		t.Fatal("capacity exists")
	}
	e.svc.SetEphemeral(ephemeralSource{err: context.Canceled})
	if get(100) != nil {
		t.Fatal("an error claims nothing")
	}
}
