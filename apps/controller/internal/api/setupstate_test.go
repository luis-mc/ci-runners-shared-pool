package api

import (
	"context"
	"errors"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/auth"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/machines"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/repos"
)

// listFake serves only List; any other call panics on the nil interface.
type listFake struct {
	MachinesService
	views []machines.View
	err   error
}

func (f listFake) List(context.Context) ([]machines.View, error) { return f.views, f.err }

func TestSetupStateSteps(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	a := &GitHubArea{Auth: h.auth, Repos: h.repos}
	st := auth.SetupState{HasApp: true, HasOwner: true}

	steps := func(m MachinesService) (string, string, string) {
		s := &server{opts: Options{Machines: m}}
		out := a.setupState(ctx, nil, st, s.machineJoined(ctx, a))
		return string(out.Steps.ConnectGithub), string(out.Steps.FirstMachine), string(out.Steps.FirstRepository)
	}

	if c, m, r := steps(listFake{}); c != "done" || m != "todo" || r != "todo" {
		t.Fatalf("empty: %s %s %s", c, m, r)
	}
	if _, m, _ := steps(nil); m != "todo" {
		t.Fatalf("machines off: %s", m)
	}
	if _, m, _ := steps(listFake{err: errors.New("db down")}); m != "todo" {
		t.Fatalf("list error: %s", m)
	}
	if _, m, _ := steps(listFake{views: []machines.View{{}}}); m != "done" {
		t.Fatalf("joined machine: %s", m)
	}

	if err := h.rstore.UpsertRepository(ctx, repos.Repository{ID: 7, FullName: "o/home-automation", PoolOn: true}); err != nil {
		t.Fatal(err)
	}
	if c, m, r := steps(listFake{views: []machines.View{{}}}); c != "done" || m != "done" || r != "done" {
		t.Fatalf("all: %s %s %s", c, m, r)
	}
}
