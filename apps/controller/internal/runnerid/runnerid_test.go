package runnerid

import (
	"context"
	"errors"
	"sync"
	"testing"
)

type memTable struct {
	mu sync.Mutex
	m  map[string]int
}

func newMem() *memTable { return &memTable{m: map[string]int{}} }

func (t *memTable) Get(_ context.Context, n string) (int, bool, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	id, ok := t.m[n]
	return id, ok, nil
}

func (t *memTable) All(context.Context) (map[string]int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := map[string]int{}
	for k, v := range t.m {
		out[k] = v
	}
	return out, nil
}

func (t *memTable) Put(_ context.Context, n string, id int) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.m[n] = id
	return nil
}

func TestStableAllocation(t *testing.T) {
	tab := newMem()
	a, err := New(Default(), tab, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	first, err := a.ID(ctx, "runner-a")
	if err != nil || first != DefaultLow {
		t.Fatalf("first = %d, %v", first, err)
	}
	// A new allocator over the same table (agent restart) gives the same id.
	b, _ := New(Default(), tab, nil)
	again, err := b.ID(ctx, "runner-a")
	if err != nil || again != first {
		t.Fatalf("after restart = %d, %v; want %d", again, err, first)
	}
	// Another runner in between does not move it.
	if _, err := b.ID(ctx, "runner-b"); err != nil {
		t.Fatal(err)
	}
	if id, _ := a.ID(ctx, "runner-a"); id != first {
		t.Fatalf("moved to %d", id)
	}
}

func TestNoOverlapBetweenRunners(t *testing.T) {
	a, _ := New(Range{64000, 64049}, newMem(), nil)
	seen := map[int]string{}
	for i := 0; i < 50; i++ {
		name := string(rune('a'+i%26)) + string(rune('a'+i/26))
		id, err := a.ID(context.Background(), name)
		if err != nil {
			t.Fatal(err)
		}
		if prev, dup := seen[id]; dup {
			t.Fatalf("id %d given to %s and %s", id, prev, name)
		}
		if !a.Range.Contains(id) {
			t.Fatalf("id %d outside %s", id, a.Range)
		}
		seen[id] = name
	}
}

func TestNoOverlapWithHostUsers(t *testing.T) {
	host := func(_ context.Context, id int) (bool, error) { return id == 64000 || id == 64001, nil }
	a, _ := New(Default(), newMem(), host)
	id, err := a.ID(context.Background(), "r")
	if err != nil || id != 64002 {
		t.Fatalf("id = %d, %v; want 64002", id, err)
	}
	if err := CheckHost(context.Background(), Default(), host); !errors.Is(err, ErrHostOverlap) {
		t.Fatalf("CheckHost = %v", err)
	}
}

func TestExhaustion(t *testing.T) {
	a, _ := New(Range{64000, 64001}, newMem(), nil)
	ctx := context.Background()
	for _, n := range []string{"a", "b"} {
		if _, err := a.ID(ctx, n); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a.ID(ctx, "c"); !errors.Is(err, ErrExhausted) {
		t.Fatalf("err = %v; want ErrExhausted", err)
	}
	// An existing runner still resolves when the range is full.
	if id, err := a.ID(ctx, "a"); err != nil || id != 64000 {
		t.Fatalf("a = %d, %v", id, err)
	}
}

func TestConcurrentAllocationIsUnique(t *testing.T) {
	a, _ := New(Default(), newMem(), nil)
	var wg sync.WaitGroup
	ids := make([]int, 40)
	for i := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, err := a.ID(context.Background(), "r"+string(rune('A'+i)))
			if err != nil {
				t.Error(err)
			}
			ids[i] = id
		}()
	}
	wg.Wait()
	seen := map[int]bool{}
	for _, id := range ids {
		if seen[id] {
			t.Fatalf("duplicate %d", id)
		}
		seen[id] = true
	}
}

func TestParseAndValidate(t *testing.T) {
	if r, err := Parse(""); err != nil || r != Default() {
		t.Fatalf("empty = %v, %v", r, err)
	}
	if r, err := Parse("65000-65100"); err != nil || r.Size() != 101 {
		t.Fatalf("parse = %v, %v", r, err)
	}
	for _, bad := range []string{"x", "1-2", "64000-63000", "64000-65534", "500-64000", "a-b"} {
		if _, err := Parse(bad); !errors.Is(err, ErrBadRange) {
			t.Errorf("Parse(%q) = %v; want ErrBadRange", bad, err)
		}
	}
}
