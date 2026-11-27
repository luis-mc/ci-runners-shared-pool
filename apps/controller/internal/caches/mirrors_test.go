package caches_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/audit"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/caches"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/mirror"
)

type fakeMirrors struct {
	rows  []mirror.Row
	wiped []string
	done  chan struct{}
}

func (f *fakeMirrors) Rows(context.Context) ([]mirror.Row, error) { return f.rows, nil }
func (f *fakeMirrors) Wipe(_ context.Context, _ audit.Actor, admin, confirm bool, id string) (mirror.Plan, int64, error) {
	if !confirm {
		return mirror.Plan{Items: []mirror.Item{{Text: "git mirror of o/r", Size: 2 << 20}}, Total: 2 << 20, Jobs: []string{"o/r run 9"}}, 0, mirror.ErrNeedsYes
	}
	f.wiped = append(f.wiped, id)
	close(f.done)
	return mirror.Plan{}, 1, nil
}

func TestHolderRowsJoinTheListAndWipeNeedsAYes(t *testing.T) {
	fm := &fakeMirrors{done: make(chan struct{}), rows: []mirror.Row{
		{ID: "mirror:git_mirror:o/r", Kind: "git_mirror", Name: "o/r", Size: 2 << 20, Status: "ok"},
		{ID: "mirror:tool_cache", Kind: "tool_cache", Size: 5, Entries: 1},
	}}
	s := caches.New(caches.Options{Machines: &fakeMachines{}, Mirrors: fm, Now: func() time.Time { return time.Unix(9, 0) }})
	got, err := s.List(context.Background(), "", "git_mirror")
	if err != nil || len(got) != 1 || got[0].RunnerName != "o/r" || got[0].MachineID != caches.DefaultHolderID {
		t.Fatalf("list %+v %v", got, err)
	}
	_, err = s.BeginWipe(context.Background(), audit.Actor{Login: "luis"}, "mirror:git_mirror:o/r", false)
	var nc *caches.NeedsConfirmation
	if !errors.As(err, &nc) || len(nc.Items) != 2 {
		t.Fatalf("want question card with the item and the job, got %v", err)
	}
	c, err := s.BeginWipe(context.Background(), audit.Actor{Login: "luis"}, "mirror:git_mirror:o/r", true)
	if err != nil || c.State != caches.StateWiping {
		t.Fatalf("begin %+v %v", c, err)
	}
	<-fm.done
	if len(fm.wiped) != 1 {
		t.Fatalf("wiped %v", fm.wiped)
	}
}
