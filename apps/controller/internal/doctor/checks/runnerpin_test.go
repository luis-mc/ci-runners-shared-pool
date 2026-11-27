package checks

import (
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/doctor"
)

type pinFake struct{ st doctor.RunnerPinStatus }

func (p pinFake) RunnerPin() doctor.RunnerPinStatus { return p.st }

// Operations R5.20: ok under 21 days behind, warn from 21, fail from 28; skip when unknown.
func TestRunnerPinAge(t *testing.T) {
	cases := []struct {
		name string
		env  doctor.Env
		want doctor.Severity
	}{
		{"no source", doctor.Env{}, doctor.Skip},
		{"not known yet", doctor.Env{RunnerPin: pinFake{}}, doctor.Skip},
		{"newest", doctor.Env{RunnerPin: pinFake{doctor.RunnerPinStatus{Known: true, Pinned: "2.340.0", Latest: "2.340.0"}}}, doctor.OK},
		{"20 days", doctor.Env{RunnerPin: pinFake{doctor.RunnerPinStatus{Known: true, Pinned: "2.338.2", Latest: "2.340.0", BehindDays: 20}}}, doctor.OK},
		{"21 days", doctor.Env{RunnerPin: pinFake{doctor.RunnerPinStatus{Known: true, Pinned: "2.338.2", Latest: "2.340.0", BehindDays: 21}}}, doctor.Warn},
		{"28 days", doctor.Env{RunnerPin: pinFake{doctor.RunnerPinStatus{Known: true, Pinned: "2.338.2", Latest: "2.340.0", BehindDays: 28}}}, doctor.Fail},
	}
	for _, c := range cases {
		got := run(t, c.env, "runner.pin_age")
		if len(got) != 1 || got[0].Severity != c.want {
			t.Fatalf("%s: %+v", c.name, got)
		}
	}
}
