package configio

import (
	"context"
	"reflect"
	"testing"
)

const planFile = `format: crsp-config
version: 1
pool:
  budget_memory_pct: 70
  default_job_slots: 2
machines:
  - name: build-01
    budget_cpu_pct: 60
  - name: build-02
    budget_cpu_pct: 40
notifications:
  - name: pager
    kind: webhook
`

func TestPlanListsEachChangeAndChangesNothing(t *testing.T) {
	e, pool, machines, chans, a := newEngine()
	before := []map[string]Fields{pool.items, machines.items, chans.items}
	p, err := e.Plan(context.Background(), []byte(planFile))
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Problems) != 0 || p.ID == "" {
		t.Fatalf("plan = %+v", p)
	}
	got := map[string]Kind{}
	for _, c := range p.Changes {
		got[c.Ref()] = c.Kind
	}
	want := map[string]Kind{"pool": Changed, "machines/build-02": Changed, "notifications/pager": Added, "notifications/ops-mail": Removed}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("changes = %v, want %v", got, want)
	}
	for _, c := range p.Changes {
		if c.Ref() == "pool" && (len(c.Fields) != 1 || c.Fields[0].Field != "budget_memory_pct" || c.Fields[0].Old != int64(85) || c.Fields[0].New != int64(70)) {
			t.Errorf("pool fields = %+v", c.Fields)
		}
	}
	if !reflect.DeepEqual(p.Removals, []string{"notifications/ops-mail"}) {
		t.Errorf("removals = %v", p.Removals)
	}
	if !reflect.DeepEqual(before, []map[string]Fields{pool.items, machines.items, chans.items}) || len(a.rows) != 0 {
		t.Fatal("a plan changed the state or wrote an audit row")
	}
}

func TestPlanLeavesOutMachinesAlone(t *testing.T) {
	e, _, _, _, _ := newEngine()
	// build-02 is left out of the file; a machine is never removed from a file.
	p, _ := e.Plan(context.Background(), []byte("format: crsp-config\nversion: 1\nmachines:\n  - name: build-01\n    budget_cpu_pct: 60\n"))
	if len(p.Changes) != 0 || len(p.Problems) != 0 {
		t.Fatalf("plan = %+v, want nothing", p)
	}
	p, _ = e.Plan(context.Background(), []byte("format: crsp-config\nversion: 1\nmachines:\n  - name: ghost\n    budget_cpu_pct: 60\n"))
	if len(p.Problems) != 1 || p.ID != "" {
		t.Fatalf("plan = %+v, want a problem for the machine that cannot be added", p)
	}
}

func TestPlanNamesAnUnknownField(t *testing.T) {
	e, _, _, _, _ := newEngine()
	p, _ := e.Plan(context.Background(), []byte("format: crsp-config\nversion: 1\npool:\n  nope: 1\n"))
	if len(p.Problems) != 1 || p.ID != "" {
		t.Fatalf("plan = %+v", p)
	}
}
