package checks

import (
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/internal/doctor"
)

func fbRepo(where string, fallbackOn bool) doctor.Repository {
	r := repo(1, "o/r", map[string]string{"CRSP_RUNS_ON_LINUX": `["ubuntu-latest"]`})
	r.Where, r.FallbackOn = where, fallbackOn
	switch where {
	case "waiting":
		r.WaitingOSes = []string{"linux"}
	case "hosted":
		r.HostedOSes = []string{"linux"}
	}
	return r
}

func TestRepoWaitingIsInfoOnlyWithFallbackOff(t *testing.T) { // doctor R5.39
	g := newGH()
	fs := run(t, doctor.Env{GitHub: g, Fleet: &fleet{repos: []doctor.Repository{fbRepo("waiting", false)}}}, "repo.waiting")
	if len(fs) != 1 || fs[0].Severity != doctor.Info {
		t.Fatalf("%+v", fs)
	}
	for _, r := range []doctor.Repository{fbRepo("waiting", true), fbRepo("crsp", false), fbRepo("hosted", true)} {
		if fs := run(t, doctor.Env{GitHub: g, Fleet: &fleet{repos: []doctor.Repository{r}}}, "repo.waiting"); len(fs) != 0 {
			t.Fatalf("%s/%v: %+v", r.Where, r.FallbackOn, fs)
		}
	}
}

func TestRepoFallbackWarnsWhileOn(t *testing.T) { // doctor R5.40
	g := newGH()
	fs := run(t, doctor.Env{GitHub: g, Fleet: &fleet{repos: []doctor.Repository{fbRepo("hosted", true)}}}, "repo.fallback")
	if len(fs) != 1 || fs[0].Severity != doctor.Warn {
		t.Fatalf("%+v", fs)
	}
	fs = run(t, doctor.Env{GitHub: g, Fleet: &fleet{repos: []doctor.Repository{fbRepo("crsp", true)}}}, "repo.fallback")
	if len(fs) != 1 || fs[0].Severity != doctor.OK {
		t.Fatalf("idle: %+v", fs)
	}
	if fs := run(t, doctor.Env{GitHub: g, Fleet: &fleet{repos: []doctor.Repository{fbRepo("waiting", false)}}}, "repo.fallback"); len(fs) != 0 {
		t.Fatalf("off: %+v", fs)
	}
}
