package runners

import (
	"context"
	"errors"
	"testing"
)

// OPS19: a runner that is in the list is found by its id and by its name;
// only a runner that is not there is "not found".
func TestRunnerIsFoundForRemove(t *testing.T) {
	r := newRig(machine("linux", "x64", true))
	ctx := context.Background()
	rec := r.add(t)
	for _, key := range []string{rec.ID, rec.Name} {
		got, _, err := r.svc.BeginRemove(ctx, key, false, "luis-mc")
		var nc *NeedsConfirmation
		if !errors.As(err, &nc) || got.ID != rec.ID {
			t.Fatalf("key %q: %+v %v", key, got, err)
		}
		if _, err := r.svc.GetView(ctx, key); err != nil {
			t.Fatalf("view %q: %v", key, err)
		}
	}
	if _, _, err := r.svc.BeginRemove(ctx, "nope", false, "luis-mc"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing runner: %v", err)
	}
}

// OPS19: two runners with one name are not guessed between.
func TestFindRefusesAmbiguousName(t *testing.T) {
	r := newRig(machine("linux", "x64", true))
	ctx := context.Background()
	_ = r.svc.Store.InsertRunner(ctx, Record{ID: "r_1", Name: "dup", Managed: true})
	_ = r.svc.Store.InsertRunner(ctx, Record{ID: "r_2", Name: "dup", Managed: true})
	if _, err := r.svc.find(ctx, "dup"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err %v", err)
	}
}

// OPS19: every change to a record is told, so the list reads again.
func TestNotifyStoreTellsEveryChange(t *testing.T) {
	var got []string
	s := NotifyStore{Store: NewMemStore(), OnChange: func(r Record, gone bool) {
		if gone {
			got = append(got, "gone")
		} else {
			got = append(got, string(r.State))
		}
	}}
	ctx := context.Background()
	_ = s.InsertRunner(ctx, Record{ID: "r_1", Name: "a", State: Creating})
	_ = s.SetRunnerState(ctx, "r_1", Draining, "remove asked")
	_ = s.DeleteRunner(ctx, "r_1")
	if len(got) != 3 || got[0] != "creating" || got[1] != "draining" || got[2] != "gone" {
		t.Fatalf("%v", got)
	}
	if err := s.SetRunnerState(ctx, "nope", Idle, ""); !errors.Is(err, ErrNotFound) || len(got) != 3 {
		t.Fatalf("a failed change was told: %v %v", err, got)
	}
}
