package checks

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/doctor"
)

type fakeReach struct {
	ls  []LoopbackListener
	err error
}

func (f fakeReach) Loopback(context.Context) ([]LoopbackListener, error) { return f.ls, f.err }

func runReach(t *testing.T, src ReachSource, repos []doctor.Repository) []doctor.Finding {
	t.Helper()
	reg := doctor.NewRegistry()
	RegisterReach(reg, src)
	env := doctor.Env{Fleet: &fleet{repos: repos}}
	e := &doctor.Engine{Registry: reg, Store: doctor.NewMemStore(), Now: func() time.Time { return now },
		Env: func(context.Context) (doctor.Env, error) { return env, nil }}
	r, err := e.Run(context.Background(), doctor.Scope{Checks: []string{"security.reach"}}, doctor.TriggerCLI, "test")
	if err != nil {
		t.Fatal(err)
	}
	return r.Findings
}

func TestReachLinkPerRepo(t *testing.T) {
	fs := runReach(t, nil, []doctor.Repository{{ID: 1, FullName: "o/a"}, {ID: 2, FullName: "o/b"}})
	if len(fs) != 2 {
		t.Fatalf("%d", len(fs))
	}
	for _, f := range fs {
		if f.Severity != doctor.Info || f.Fix.Link != ReachLink {
			t.Errorf("%+v", f)
		}
	}
}

func TestLoopbackListenerWarns(t *testing.T) {
	m := healthyMachine()
	src := fakeReach{ls: []LoopbackListener{{Machine: m, Port: 5432, Process: "postgres", RunsJobs: true}, {Machine: m, Port: 22, Process: "sshd", RunsJobs: false}}}
	fs := runReach(t, src, nil)
	if len(fs) != 1 || fs[0].Severity != doctor.Warn {
		t.Fatalf("%+v", fs)
	}
	fs = runReach(t, fakeReach{err: errors.New("no")}, nil)
	if len(fs) != 1 || fs[0].Severity != doctor.Skip {
		t.Fatalf("%+v", fs)
	}
}
