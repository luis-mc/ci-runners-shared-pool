package configio

import (
	"context"
	"testing"
)

// R6.10: export, import into an empty controller, export again: the same file.
func TestExportImportExportSame(t *testing.T) {
	ctx := context.Background()
	a, _, _, _, _ := newEngine()
	first, err := a.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	b, pool, machines, chans, _ := newEngine()
	// The second controller starts empty except for what a file cannot create:
	// its machines joined by their own join file, with other settings.
	pool.items[""] = Fields{"budget_memory_pct": 10, "default_job_slots": 1, "size_slots_from_machine": false}
	machines.items["build-01"] = Fields{"budget_cpu_pct": 10, "allowed_backends": []any{}}
	machines.items["build-02"] = Fields{"budget_cpu_pct": 10, "allowed_backends": []any{}}
	chans.items = map[string]Fields{}
	p, err := b.Plan(ctx, first)
	if err != nil || len(p.Problems) != 0 {
		t.Fatalf("plan %v %+v", err, p)
	}
	if p.Empty() {
		t.Fatal("expected changes")
	}
	if _, err := b.Apply(ctx, who, p.ID, p.Removals); err != nil {
		t.Fatal(err)
	}
	second, err := b.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatalf("round trip differs:\n%s\n---\n%s", first, second)
	}
}
