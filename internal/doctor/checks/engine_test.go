package checks

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/doctor"
)

type engineFake struct {
	states []doctor.EngineState
	err    error
}

func (e engineFake) Engines(context.Context) ([]doctor.EngineState, error) { return e.states, e.err }

func engineRun(t *testing.T, src doctor.EngineSource) []doctor.Finding {
	t.Helper()
	reg := doctor.NewRegistry()
	Register(reg)
	e := &doctor.Engine{Registry: reg, Store: doctor.NewMemStore(), Now: func() time.Time { return now },
		Env: func(context.Context) (doctor.Env, error) {
			return doctor.Env{Fleet: noEngineFleet{}, Engines: src}, nil
		}}
	r, err := e.Run(context.Background(), doctor.Scope{Checks: []string{"machine.engine"}}, doctor.TriggerCLI, "test")
	if err != nil {
		t.Fatal(err)
	}
	return r.Findings
}

type noEngineFleet struct{ doctor.Fleet }

// TestMachineEngineSaysWhichEngineAndWhetherItAnswers: runners R5.47.
func TestMachineEngineSaysWhichEngineAndWhetherItAnswers(t *testing.T) {
	fs := engineRun(t, engineFake{states: []doctor.EngineState{
		{Machine: named(win(), "wsl"), Kind: "docker-wsl2-windows-cli", Usable: true, Version: "27.3.1"},
		{Machine: named(win(), "desktop"), Kind: "docker-desktop", Usable: true, Version: "27.3.1"},
		{Machine: named(win(), "wslcmd"), Kind: "docker-wsl2-wsl", Usable: true, Version: "26.1.4"},
		{Machine: named(win(), "down"), Kind: "docker-desktop", Reason: "the engine does not answer"},
		{Machine: named(win(), "none"), Kind: "none", Reason: "no docker engine"},
		{Machine: named(linux(), "lin"), Kind: "docker-native", Usable: true, Version: "27.3.1"},
		{Machine: named(linux(), "lin-none"), Kind: "none", Reason: "the docker command is not installed"},
	}})
	if len(fs) != 7 {
		t.Fatalf("%d findings", len(fs))
	}
	for name, want := range map[string]struct {
		sev doctor.Severity
		in  string
	}{
		"wsl":      {doctor.OK, "Docker in WSL2, through the Windows docker command 27.3.1"},
		"desktop":  {doctor.OK, "Docker Desktop 27.3.1"},
		"wslcmd":   {doctor.OK, "`wsl docker`"},
		"down":     {doctor.Warn, "not usable"},
		"none":     {doctor.Warn, "not usable"},
		"lin":      {doctor.OK, "uses Docker 27.3.1"},
		"lin-none": {doctor.Warn, "not usable"},
	} {
		f := find(t, fs, name)
		if f.Severity != want.sev || !strings.Contains(f.Title, want.in) {
			t.Errorf("%s: %v %q", name, f.Severity, f.Title)
		}
	}
	if f := find(t, fs, "down"); !strings.Contains(f.Fix.Text, "Docker Desktop") || !strings.Contains(f.Detail, "does not answer") {
		t.Errorf("down: %+v", f)
	}
	if f := find(t, fs, "none"); !strings.Contains(f.Fix.Text, "WSL2") {
		t.Errorf("none on Windows: %+v", f)
	}
	if f := find(t, fs, "lin-none"); !strings.Contains(f.Fix.Text, "systemctl") {
		t.Errorf("none on Linux: %+v", f)
	}
}

func TestMachineEngineSkipsWithoutAReport(t *testing.T) {
	for name, src := range map[string]doctor.EngineSource{"nil": nil, "unreachable": engineFake{err: doctor.ErrUnreachable}} {
		fs := engineRun(t, src)
		if len(fs) != 1 || fs[0].Severity != doctor.Skip {
			t.Errorf("%s: %+v", name, fs)
		}
	}
}
