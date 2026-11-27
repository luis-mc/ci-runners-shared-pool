package envsets

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/redact"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/secrets"
)

func newReg() (*Registry, *secrets.Memory, *MemoryMeta) {
	sm, mm := secrets.NewMemory(), &MemoryMeta{}
	return &Registry{Meta: mm, Secrets: sm, Redactor: redact.New()}, sm, mm
}

func TestMergeOrderMachineSetsRunner(t *testing.T) {
	ctx := context.Background()
	r, _, _ := newReg()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(r.Put(ctx, Set{Name: "dotnet-8", Vars: []Var{{Name: "DOTNET_ROOT", Value: "/a"}, {Name: "A", Value: "dotnet"}}}))
	must(r.Put(ctx, Set{Name: "later", Vars: []Var{{Name: "A", Value: "later"}, {Name: "B", Value: "later"}}}))
	got, err := r.Merge(ctx, Layers{
		Machine: []Var{{Name: "A", Value: "machine"}, {Name: "B", Value: "machine"}, {Name: "M", Value: "machine"}},
		Sets:    []string{"dotnet-8", "later"},
		Runner:  []Var{{Name: "B", Value: "runner"}},
	})
	must(err)
	want := map[string][2]string{
		"A":           {"later", "set:later"},
		"B":           {"runner", "runner"},
		"DOTNET_ROOT": {"/a", "set:dotnet-8"},
		"M":           {"machine", "machine"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i, v := range got {
		w := want[v.Name]
		if v.Value != w[0] || v.Source != w[1] {
			t.Errorf("%s = %q from %q, want %q from %q", v.Name, v.Value, v.Source, w[0], w[1])
		}
		if i > 0 && got[i-1].Name > v.Name {
			t.Error("not sorted")
		}
	}
}

func TestMergeSetOrderMatters(t *testing.T) {
	ctx := context.Background()
	r, _, _ := newReg()
	_ = r.Put(ctx, Set{Name: "one", Vars: []Var{{Name: "X", Value: "1"}}})
	_ = r.Put(ctx, Set{Name: "two", Vars: []Var{{Name: "X", Value: "2"}}})
	a, _ := r.Merge(ctx, Layers{Sets: []string{"one", "two"}})
	b, _ := r.Merge(ctx, Layers{Sets: []string{"two", "one"}})
	if a[0].Value != "2" || b[0].Value != "1" {
		t.Fatalf("a=%v b=%v", a, b)
	}
}

func TestMissingSetFailsTheMerge(t *testing.T) {
	r, _, _ := newReg()
	_, err := r.Merge(context.Background(), Layers{Sets: []string{"nope"}})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestSensitiveValueNeverInMetaAndMaskedInViews(t *testing.T) {
	ctx := context.Background()
	r, sm, mm := newReg()
	const v = "s3cr3t-value-123"
	if err := r.Put(ctx, Set{Name: "nuget", Vars: []Var{{Name: "FEED_LOGIN", Value: v, Sensitive: true}, {Name: "FEED", Value: "https://x"}}}); err != nil {
		t.Fatal(err)
	}
	for n, d := range mm.m {
		if strings.Contains(string(d), v) {
			t.Fatalf("meta %s holds the value: %s", n, d)
		}
	}
	if len(sm.Keys()) != 1 {
		t.Fatalf("keys = %v", sm.Keys())
	}
	s, _ := r.Get(ctx, "nuget")
	for _, x := range s.Vars {
		if x.Sensitive && x.Value != "" {
			t.Fatal("Get leaks the value")
		}
	}
	got, err := r.Merge(ctx, Layers{Sets: []string{"nuget"}})
	if err != nil {
		t.Fatal(err)
	}
	real := false
	for _, x := range got {
		if x.Name == "FEED_LOGIN" && x.Value == v {
			real = true
		}
	}
	if !real {
		t.Fatal("merge must carry the real value for the runner env")
	}
	for _, x := range Redacted(got) {
		if x.Name == "FEED_LOGIN" && (x.Value != Mask || !x.Sensitive) {
			t.Fatalf("view = %+v", x)
		}
	}
	if out := r.Redactor.Redact("curl " + v + " done"); strings.Contains(out, v) {
		t.Fatalf("log not redacted: %s", out)
	}
}

func TestSensitiveEditKeepsValueAndDropsRemoved(t *testing.T) {
	ctx := context.Background()
	r, sm, _ := newReg()
	_ = r.Put(ctx, Set{Name: "s", Vars: []Var{{Name: "A_LOGIN", Value: "value-aaaa", Sensitive: true}, {Name: "B_LOGIN", Value: "value-bbbb", Sensitive: true}}})
	if err := r.Put(ctx, Set{Name: "s", Vars: []Var{{Name: "A_LOGIN", Sensitive: true}}}); err != nil {
		t.Fatal(err)
	}
	b, err := sm.Get(ctx, secretKey("s", "A_LOGIN"))
	if err != nil || string(b) != "value-aaaa" {
		t.Fatalf("kept value = %q, %v", b, err)
	}
	if _, err := sm.Get(ctx, secretKey("s", "B_LOGIN")); !errors.Is(err, secrets.ErrNotFound) {
		t.Fatalf("removed value stays: %v", err)
	}
	if err := r.Put(ctx, Set{Name: "n", Vars: []Var{{Name: "NEW", Sensitive: true}}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("new sensitive without value: %v", err)
	}
	if err := r.Delete(ctx, "s"); err != nil || len(sm.Keys()) != 0 {
		t.Fatalf("delete: %v keys %v", err, sm.Keys())
	}
}

func TestSecretStoreDownFailsClosed(t *testing.T) {
	r, sm, _ := newReg()
	sm.Down = true
	err := r.Put(context.Background(), Set{Name: "s", Vars: []Var{{Name: "X_LOGIN", Value: "value-aaaa", Sensitive: true}}})
	if !errors.Is(err, secrets.ErrUnavailable) {
		t.Fatalf("err = %v", err)
	}
}

func TestValidation(t *testing.T) {
	for _, s := range []Set{
		{Name: "Bad Name"},
		{Name: "ok", Vars: []Var{{Name: "1X"}}},
		{Name: "ok", Vars: []Var{{Name: "GITHUB_ENV", Value: "x"}}},
		{Name: "ok", Vars: []Var{{Name: "actions_runner_input_jitconfig"}}},
		{Name: "ok", Vars: []Var{{Name: "A"}, {Name: "A"}}},
		{Name: "ok", Vars: []Var{{Name: "A", Value: "a\nb"}}},
	} {
		if err := ValidateSet(s); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v: err = %v", s, err)
		}
	}
	if _, err := Lines([]Resolved{{Name: "A", Value: "x\ny"}}); err == nil {
		t.Error("Lines must refuse a line break")
	}
}
