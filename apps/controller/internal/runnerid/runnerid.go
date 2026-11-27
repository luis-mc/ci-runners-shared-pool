// Package runnerid gives each runner one uid/gid that never changes. The ids
// come from one reserved range that no host user uses; the allocation is kept
// in the store, so a restart or a re-created container gets the same id and
// the files of its caches and work folder keep their owner (runners R5.72,
// handover lesson 12).
package runnerid

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
)

// Default range: 1000 ids above every normal host user and below the 65534
// "nobody" id.
const (
	DefaultLow  = 64000
	DefaultHigh = 64999
)

var (
	// ErrExhausted means every id of the range is taken.
	ErrExhausted = errors.New("no free runner id: the reserved range is full")
	// ErrBadRange means the range is not usable.
	ErrBadRange = errors.New("bad runner id range")
	// ErrHostOverlap means a host user or group already holds ids of the range.
	ErrHostOverlap = errors.New("the runner id range overlaps a host user or group")
)

// Range is the reserved ids, both ends included.
type Range struct{ Low, High int }

// Default is 64000-64999.
func Default() Range { return Range{DefaultLow, DefaultHigh} }

// Size is the number of ids.
func (r Range) Size() int { return r.High - r.Low + 1 }

// Contains reports whether id is in the range.
func (r Range) Contains(id int) bool { return id >= r.Low && id <= r.High }

// String reads as "64000-64999".
func (r Range) String() string { return fmt.Sprintf("%d-%d", r.Low, r.High) }

// Validate refuses a range that holds root, system ids or the nobody id.
func (r Range) Validate() error {
	if r.Low < 1000 || r.High < r.Low || r.High >= 65534 {
		return fmt.Errorf("%w: %s (use ids from 1000 to 65533, low not above high)", ErrBadRange, r)
	}
	return nil
}

// Parse reads "low-high"; empty gives the default.
func Parse(s string) (Range, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Default(), nil
	}
	lo, hi, ok := strings.Cut(s, "-")
	if !ok {
		return Range{}, fmt.Errorf("%w: %q (write low-high)", ErrBadRange, s)
	}
	l, err1 := strconv.Atoi(strings.TrimSpace(lo))
	h, err2 := strconv.Atoi(strings.TrimSpace(hi))
	if err1 != nil || err2 != nil {
		return Range{}, fmt.Errorf("%w: %q (write low-high)", ErrBadRange, s)
	}
	r := Range{l, h}
	return r, r.Validate()
}

// Table is where allocations are kept (the store).
type Table interface {
	// Get returns the id of a runner; ok is false when none was given.
	Get(ctx context.Context, name string) (id int, ok bool, err error)
	// All returns every allocation, name to id.
	All(ctx context.Context) (map[string]int, error)
	// Put records an allocation.
	Put(ctx context.Context, name string, id int) error
}

// Host answers whether the host already uses an id as a uid or a gid.
type Host func(ctx context.Context, id int) (bool, error)

// Allocator hands out ids. It is safe for concurrent use.
type Allocator struct {
	Range Range
	Table Table
	// Host, when set, makes the allocator skip an id a host user or group
	// holds, so the range never overlaps host accounts.
	Host Host

	mu sync.Mutex
}

// New builds an allocator over a validated range.
func New(r Range, t Table, h Host) (*Allocator, error) {
	if err := r.Validate(); err != nil {
		return nil, err
	}
	if t == nil {
		return nil, errors.New("runner ids need a table")
	}
	return &Allocator{Range: r, Table: t, Host: h}, nil
}

// ID returns the id of the runner, allocating the lowest free one the first
// time. The same name always gets the same id. An id recorded outside the
// range (the range was changed) is kept, so the owner of existing files does
// not change; the owner moves the range, not the runners.
func (a *Allocator) ID(ctx context.Context, name string) (int, error) {
	if name == "" {
		return 0, errors.New("runner id: a name is needed")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if id, ok, err := a.Table.Get(ctx, name); err != nil {
		return 0, fmt.Errorf("runner id: %w", err)
	} else if ok {
		return id, nil
	}
	used, err := a.Table.All(ctx)
	if err != nil {
		return 0, fmt.Errorf("runner id: %w", err)
	}
	taken := make(map[int]bool, len(used))
	for _, id := range used {
		taken[id] = true
	}
	for id := a.Range.Low; id <= a.Range.High; id++ {
		if taken[id] {
			continue
		}
		if a.Host != nil {
			busy, err := a.Host(ctx, id)
			if err != nil {
				return 0, fmt.Errorf("runner id: host check: %w", err)
			}
			if busy {
				continue
			}
		}
		if err := a.Table.Put(ctx, name, id); err != nil {
			return 0, fmt.Errorf("runner id: %w", err)
		}
		return id, nil
	}
	return 0, fmt.Errorf("%w (%s, %d runners)", ErrExhausted, a.Range, len(used))
}

// CheckHost returns ErrHostOverlap when any id of the range is in use by the
// host. It is for the doctor and for a range change; ID skips busy ids on its own.
func CheckHost(ctx context.Context, r Range, h Host) error {
	for id := r.Low; id <= r.High; id++ {
		busy, err := h(ctx, id)
		if err != nil {
			return err
		}
		if busy {
			return fmt.Errorf("%w: id %d is in use (range %s)", ErrHostOverlap, id, r)
		}
	}
	return nil
}
