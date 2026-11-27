package configio

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/audit"
)

var who = audit.Actor{Login: "owner", Source: audit.SourceWeb}

func TestRefusesStalePlan(t *testing.T) {
	e, pool, _, _, _ := newEngine()
	ctx := context.Background()
	p, _ := e.Plan(ctx, []byte(planFile))
	pool.items[""]["default_job_slots"] = 3 // the pool changed after the plan
	if _, err := e.Apply(ctx, who, p.ID, p.Removals); !errors.Is(err, ErrStale) {
		t.Fatalf("err = %v, want ErrStale", err)
	}
	if pool.items[""]["budget_memory_pct"] != 85 {
		t.Fatal("a stale plan changed the pool")
	}
	if _, err := e.Apply(ctx, who, "plan-nope", nil); !errors.Is(err, ErrUnknownPlan) {
		t.Fatalf("err = %v, want ErrUnknownPlan", err)
	}
}

func TestRemovalNeedsYes(t *testing.T) {
	e, _, _, chans, _ := newEngine()
	ctx := context.Background()
	p, _ := e.Plan(ctx, []byte(planFile))
	_, err := e.Apply(ctx, who, p.ID, []string{"notifications/other"})
	var nc *NeedsConfirmation
	if !errors.As(err, &nc) || !reflect.DeepEqual(nc.Removals, []string{"notifications/ops-mail"}) {
		t.Fatalf("err = %v, want NeedsConfirmation naming ops-mail", err)
	}
	if _, ok := chans.items["ops-mail"]; !ok {
		t.Fatal("removal ran without a yes")
	}
	if _, err := e.Apply(ctx, who, p.ID, []string{"notifications/ops-mail"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := chans.items["ops-mail"]; ok {
		t.Fatal("removal did not run with the yes")
	}
}

func TestAuditPerChange(t *testing.T) {
	e, pool, machines, chans, a := newEngine()
	ctx := context.Background()
	p, _ := e.Plan(ctx, []byte(planFile))
	res, err := e.Apply(ctx, who, p.ID, p.Removals)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Applied) != len(p.Changes) || len(a.rows) != len(p.Changes) {
		t.Fatalf("applied %d, audited %d, want %d", len(res.Applied), len(a.rows), len(p.Changes))
	}
	for _, r := range a.rows {
		if r.Action != "config.apply" || r.Refused {
			t.Errorf("row = %+v", r)
		}
	}
	if pool.items[""]["budget_memory_pct"] != int64(70) || machines.items["build-02"]["budget_cpu_pct"] != int64(40) || chans.items["pager"] == nil {
		t.Fatalf("state after apply: %v %v %v", pool.items, machines.items, chans.items)
	}
	// A second plan of the same file is empty: nothing left to do.
	p2, _ := e.Plan(ctx, []byte(planFile))
	if !p2.Empty() {
		t.Fatalf("plan after apply = %+v", p2.Changes)
	}
	// The applied plan cannot be applied twice.
	if _, err := e.Apply(ctx, who, p.ID, p.Removals); !errors.Is(err, ErrUnknownPlan) {
		t.Fatalf("err = %v", err)
	}
}

func TestFailedChangeIsAuditedRefusedAndStops(t *testing.T) {
	e, pool, _, _, a := newEngine()
	ctx := context.Background()
	p, _ := e.Plan(ctx, []byte(planFile))
	pool.failWrites = true
	res, err := e.Apply(ctx, who, p.ID, p.Removals)
	if err == nil || len(res.Applied) != 0 {
		t.Fatalf("res = %+v err = %v", res, err)
	}
	if len(a.rows) != 1 || !a.rows[0].Refused {
		t.Fatalf("rows = %+v", a.rows)
	}
}
