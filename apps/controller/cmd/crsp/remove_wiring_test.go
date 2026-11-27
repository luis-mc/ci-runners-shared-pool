package main

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/store"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/audit"
)

func fakeRemove(started *[][]string, fail error) *removeService {
	return &removeService{
		exe:   func() (string, error) { return "crsp", nil },
		start: func(_ string, args []string) error { *started = append(*started, args); return fail },
	}
}

func TestRemoveStartsTheUninstallForThisMachine(t *testing.T) {
	var got [][]string
	r := fakeRemove(&got, nil)
	actor := audit.Actor{Login: "ana", Source: audit.SourceWeb}
	if err := r.Remove(context.Background(), actor, "", false); err != nil {
		t.Fatal(err)
	}
	if err := r.Remove(context.Background(), actor, "", true); err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"uninstall", "--keep-data", "--yes", "--actor", "ana"},
		{"uninstall", "--delete-data", "--yes", "--actor", "ana"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestRemoveRefusesAnotherMachine(t *testing.T) {
	var got [][]string
	r := fakeRemove(&got, nil) // no machines service: no other ID is this machine
	if err := r.Remove(context.Background(), audit.Actor{}, "m2", true); !errors.Is(err, errRemoteRemove) || len(got) != 0 {
		t.Fatalf("err %v, started %v", err, got)
	}
}

func TestRemoveReportsAStartFailure(t *testing.T) {
	var got [][]string
	boom := errors.New("boom")
	r := fakeRemove(&got, boom)
	if err := r.Remove(context.Background(), audit.Actor{}, "", false); !errors.Is(err, boom) {
		t.Fatalf("err %v", err)
	}
}

func TestRemoveWritesAnAuditRowWithActorAndResult(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	log := audit.New(db)
	var got [][]string
	r := fakeRemove(&got, nil)
	r.log = log
	if err := r.Remove(ctx, audit.Actor{Login: "ana", Source: audit.SourceWeb}, "", true); err != nil {
		t.Fatal(err)
	}
	page, err := log.List(ctx, audit.Filter{TargetType: "update", Limit: 10})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("rows %v, err %v", page.Items, err)
	}
	if e := page.Items[0]; e.Action != "update.remove.started" || e.Actor != "ana" {
		t.Fatalf("row %+v", e)
	}
}
