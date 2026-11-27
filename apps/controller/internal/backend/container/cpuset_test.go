package container

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend/sys"
)

func TestAllocationNeverOverlaps(t *testing.T) {
	a := NewCPUAllocator(8)
	seen := map[int]string{}
	for _, h := range []string{"p1", "p2", "p3"} {
		set, err := a.Allocate(h, 2)
		if err != nil {
			t.Fatal(err)
		}
		ids, err := ParseCPUSet(set)
		if err != nil || len(ids) != 2 {
			t.Fatalf("%s got %q (%v)", h, set, err)
		}
		for _, id := range ids {
			if o, dup := seen[id]; dup {
				t.Fatalf("CPU %d given to %s and %s", id, o, h)
			}
			seen[id] = h
		}
	}
}

func TestAllocationNeverOverlapsConcurrently(t *testing.T) {
	a := NewCPUAllocator(16)
	var wg sync.WaitGroup
	var mu sync.Mutex
	seen := map[int]bool{}
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			set, err := a.Allocate("h"+string(rune('a'+i)), 2)
			if err != nil {
				t.Error(err)
				return
			}
			ids, err := ParseCPUSet(set)
			if err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			defer mu.Unlock()
			for _, id := range ids {
				if seen[id] {
					t.Errorf("CPU %d twice", id)
				}
				seen[id] = true
			}
		}(i)
	}
	wg.Wait()
	if len(seen) != 16 || a.Free() != 0 {
		t.Fatalf("seen %d, free %d", len(seen), a.Free())
	}
}

func TestCPUsAreFreedOnRelease(t *testing.T) {
	a := NewCPUAllocator(4)
	if _, err := a.Allocate("p1", 4); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Allocate("p2", 1); !errors.Is(err, ErrCPUBudget) {
		t.Fatalf("full budget must refuse, got %v", err)
	}
	a.Release("p1")
	a.Release("p1") // twice is safe
	if a.Free() != 4 || a.Held("p1") != "" {
		t.Fatalf("free %d held %q", a.Free(), a.Held("p1"))
	}
	if set, err := a.Allocate("p2", 2); err != nil || set != "0-1" {
		t.Fatalf("got %q, %v", set, err)
	}
}

func TestRefusesWhenBudgetExhaustedAndHoldsNothing(t *testing.T) {
	a := NewCPUAllocator(3)
	if _, err := a.Allocate("p1", 2); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Allocate("p2", 2); !errors.Is(err, ErrCPUBudget) {
		t.Fatalf("got %v", err)
	}
	if a.Free() != 1 || a.Held("p2") != "" {
		t.Fatalf("a refused request kept CPUs: free %d", a.Free())
	}
	if _, err := NewCPUAllocator(0).Allocate("p", 1); !errors.Is(err, ErrCPUBudget) {
		t.Fatalf("no budget must refuse, got %v", err)
	}
}

func TestSameHolderGetsSameSet(t *testing.T) {
	a := NewCPUAllocator(4)
	s1, _ := a.Allocate("p", 2)
	s2, err := a.Allocate("p", 2)
	if err != nil || s1 != s2 || a.Free() != 2 {
		t.Fatalf("%q %q %v free %d", s1, s2, err, a.Free())
	}
	if _, err := a.Allocate("p", 3); err == nil {
		t.Fatal("a different size for a holder must be refused")
	}
}

func TestCPUSetFormatAndParse(t *testing.T) {
	for in, want := range map[string]string{"0-3": "0-3", "0,2": "0,2", "0-1,4,6-7": "0-1,4,6-7", "5": "5", "": ""} {
		ids, err := ParseCPUSet(in)
		if err != nil || FormatCPUSet(ids) != want {
			t.Errorf("%q: %v %q", in, err, FormatCPUSet(ids))
		}
	}
	for _, bad := range []string{"a", "3-1", "-2", "1,,2", "1-"} {
		if _, err := ParseCPUSet(bad); err == nil {
			t.Errorf("%q must be refused", bad)
		}
	}
}

func TestResourceArgsMemoryAndSwapEqual(t *testing.T) {
	got := strings.Join(ResourceArgs(4096, "2-3"), " ")
	if got != "--memory 4096m --memory-swap 4096m --cpuset-cpus 2-3" {
		t.Fatal(got)
	}
	if len(ResourceArgs(0, "")) != 0 {
		t.Fatal("no caps, no flags")
	}
	if got := strings.Join(ResourceArgs(0, "0"), " "); got != "--cpuset-cpus 0" {
		t.Fatal(got)
	}
}

func TestRunArgsCarryCpusetAndMemoryCap(t *testing.T) {
	a, err := RunArgs(PoolSpec{Owner: "o", Repo: "r", ImageTag: "crsp-pool:k", Limits: Limits{MemoryMB: 2048, CPUs: 2}, CPUSet: "4-5"})
	if err != nil {
		t.Fatal(err)
	}
	j := strings.Join(a, " ")
	for _, w := range []string{"--memory 2048m --memory-swap 2048m", "--cpuset-cpus 4-5", "--cpus 2"} {
		if !strings.Contains(j, w) {
			t.Errorf("missing %q in %s", w, j)
		}
	}
}

func TestCPUsFor(t *testing.T) {
	for in, want := range map[float64]int{0: 0, -1: 0, 1: 1, 1.5: 2, 2.5: 3, 4: 4} {
		if got := CPUsFor(in); got != want {
			t.Errorf("%v: %d", in, got)
		}
	}
}

func TestBackendPinsAndFreesPoolCPUs(t *testing.T) {
	b := New(sys.Deps{}, nil, Config{CPUBudget: 4})
	p1 := PoolSpec{Owner: "o", Repo: "a", Limits: Limits{CPUs: 2}}
	p2 := PoolSpec{Owner: "o", Repo: "b", Limits: Limits{CPUs: 2}}
	p3 := PoolSpec{Owner: "o", Repo: "c", Limits: Limits{CPUs: 1}}
	if err := b.pin(&p1); err != nil || p1.CPUSet != "0-1" {
		t.Fatalf("%q %v", p1.CPUSet, err)
	}
	if err := b.pin(&p2); err != nil || p2.CPUSet != "2-3" {
		t.Fatalf("%q %v", p2.CPUSet, err)
	}
	if err := b.pin(&p3); !errors.Is(err, ErrCPUBudget) {
		t.Fatalf("got %v", err)
	}
	b.unpin(PoolName("o", "a"))
	if err := b.pin(&p3); err != nil || p3.CPUSet != "0" {
		t.Fatalf("%q %v", p3.CPUSet, err)
	}
	// No CPU limit, or no budget: no pinning and no error.
	none := PoolSpec{Owner: "o", Repo: "d"}
	if err := b.pin(&none); err != nil || none.CPUSet != "" {
		t.Fatalf("%q %v", none.CPUSet, err)
	}
	if err := New(sys.Deps{}, nil, Config{}).pin(&p1); err != nil || p1.CPUSet != "0-1" {
		t.Fatalf("no budget must leave the set alone: %q %v", p1.CPUSet, err)
	}
}

// pinFake answers the two engine calls RecoverCPUSets makes.
type pinFake struct{ sets map[string]string }

func (f pinFake) Detect(context.Context) (Status, error) { return Status{}, nil }
func (f pinFake) Command(args ...string) (sys.Command, error) {
	return sys.Command{Name: "docker", Args: args}, nil
}
func (f pinFake) Run(_ context.Context, args ...string) (sys.Result, error) {
	if args[0] == "ps" {
		var names []string
		for n := range f.sets {
			names = append(names, n)
		}
		return sys.Result{Stdout: strings.Join(names, "\n")}, nil
	}
	return sys.Result{Stdout: f.sets[args[len(args)-1]] + "\n"}, nil
}

func TestCrashRecoveryAdoptsLiveSets(t *testing.T) {
	b := New(sys.Deps{}, pinFake{sets: map[string]string{"crsp-pool-x": "0-1", "crsp-pool-y": "", "crsp-pool-z": "3"}}, Config{CPUBudget: 4})
	if err := b.RecoverCPUSets(context.Background()); err != nil {
		t.Fatal(err)
	}
	if b.cpus.Free() != 1 {
		t.Fatalf("free %d, want 1", b.cpus.Free())
	}
	set, err := b.cpus.Allocate("new", 1)
	if err != nil || set != "2" {
		t.Fatalf("a new container must get the free CPU: %q %v", set, err)
	}
	// A set outside the budget is reported, the rest still adopted.
	b2 := New(sys.Deps{}, pinFake{sets: map[string]string{"crsp-pool-q": "9"}}, Config{CPUBudget: 4})
	if err := b2.RecoverCPUSets(context.Background()); err == nil {
		t.Fatal("a set outside the budget must be reported")
	}
}
