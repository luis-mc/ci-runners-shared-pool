package machines

import (
	"context"
	"fmt"
	"strings"

	"github.com/luis-mc/ci-runners-shared-pool/internal/poolsize"
)

// PoolSizeInputs builds the figures of the pool-size check from what the
// controller knows: the machine facts, the newest load the agent reported
// (R5.17: its memory figure counts the WSL VM) and the declared `.wslconfig`.
// The second value says why it cannot be built, empty when it can.
func (s *Service) poolSizeInputs(id string, f Facts, set Settings) (poolsize.Inputs, string) {
	if f.MemoryBytes <= 0 || f.Threads <= 0 {
		return poolsize.Inputs{}, "the memory and CPUs of this machine are not known yet"
	}
	s.mu.Lock()
	l, ok := s.loads[id]
	s.mu.Unlock()
	if !ok || !l.MemoryKnown {
		return poolsize.Inputs{}, "the memory use of this machine is not known yet; the pool size is checked once its agent reports"
	}
	used := int64(float64(f.MemoryBytes) * l.MemoryPct / 100)
	outside := used - l.WSLVMBytes
	if outside < 0 {
		outside = 0
	}
	return poolsize.Inputs{
		TotalBytes: f.MemoryBytes, TotalCPUs: f.Threads,
		UsedOutsideBytes: outside, WSLVMBytes: l.WSLVMBytes,
		DockerCeilingBytes: f.WSL.MemoryBytes,
		BudgetMemPct:       set.BudgetMemoryPct, BudgetCPUPct: set.BudgetCPUPct,
		HostP95Bytes: l.HostUseP95Bytes, HostReady: l.HostMeasured(),
		HostSpanHours: l.HostUseSpan.Hours(),
		WSLPool:       f.WSL.ConfigPresent || l.WSLVMBytes > 0,
	}, ""
}

// CheckPoolSize runs the check of R5.58 for a proposed size and returns the
// whole result, with the sizing inputs, for the console to show.
func (s *Service) CheckPoolSize(id string, m Machine, cur, next Settings, confirmShrink bool) (poolsize.Result, error) {
	in, why := s.poolSizeInputs(id, m.Facts, next)
	if why != "" {
		return poolsize.Result{}, fmt.Errorf("%w: %s", ErrInvalid, why)
	}
	return poolsize.Check(in, poolsize.Request{
		MemGiB: next.PoolMemoryGiB, CPUs: next.PoolCPUs,
		CurMemGiB: cur.PoolMemoryGiB, CurCPUs: cur.PoolCPUs, ConfirmShrink: confirmShrink,
	}), nil
}

// checkPoolSize refuses a pool size that does not fit the budget and asks for
// a second, explicit yes to a size below the current one (R5.58). A shrink is
// never a side effect of another change: ConfirmShrink is a field of its own
// that Confirm does not set.
func (s *Service) checkPoolSize(id string, m Machine, cur, next Settings, confirmShrink bool) error {
	if why := (poolsize.WSLConfig{
		MemoryBytes: m.Facts.WSL.MemoryBytes, MemorySet: m.Facts.WSL.MemoryBytes > 0,
		Processors: m.Facts.WSL.Processors, ProcSet: m.Facts.WSL.Processors > 0,
	}).Refusal(m.Facts.MemoryBytes, m.Facts.Threads); why != "" {
		return fmt.Errorf("%w: %s", ErrInvalid, why)
	}
	if next.PoolMemoryGiB == 0 && next.PoolCPUs == 0 {
		if cur.PoolMemoryGiB == 0 && cur.PoolCPUs == 0 {
			return nil
		}
		if !confirmShrink {
			return &NeedsConfirmationError{
				Message: "clearing the pool size lowers it; send confirm_shrink to do it",
				Names:   []string{fmt.Sprintf("pool %g GiB and %d CPUs", cur.PoolMemoryGiB, cur.PoolCPUs)},
			}
		}
		return nil
	}
	if next.PoolMemoryGiB <= 0 || next.PoolCPUs <= 0 {
		return fmt.Errorf("%w: set the pool memory and the pool CPUs together", ErrInvalid)
	}
	res, err := s.CheckPoolSize(id, m, cur, next, confirmShrink)
	if err != nil {
		return err
	}
	switch {
	case res.NeedsShrinkConfirm:
		return &NeedsConfirmationError{
			Message: "this lowers the pool size; send confirm_shrink to do it",
			Names:   []string{res.Reason},
		}
	case !res.OK:
		return fmt.Errorf("%w: %s (%s). %s", ErrInvalid, res.Reason, sizingLine(res), strings.Join(res.Nearest, "; "))
	}
	return nil
}

func sizingLine(r poolsize.Result) string {
	return fmt.Sprintf("machine %.1f GiB, used outside WSL %.1f GiB, memory budget %.1f GiB, room %.1f GiB, CPU budget %d, decided by the %s",
		r.TotalGiB, r.UsedOutsideGiB, r.MemBudgetGiB, r.RoomGiB, r.CPUBudget, r.Decided)
}

// PoolSizeCheck is the dry run of R5.58 for the console's Review step: the
// result of the check for a proposed size, the current size and whether the
// change lowers it. Nothing is saved. Known is false, with Why, when the
// controller lacks the figures to check (no facts or no load yet).
type PoolSizeCheck struct {
	Known     bool
	Why       string
	Result    poolsize.Result
	CurMemGiB float64
	CurCPUs   int
	Shrinks   bool
}

// PoolSizeCheck checks a proposed pool size on a machine without saving it.
// 0 and 0 mean clearing the size.
func (s *Service) PoolSizeCheck(ctx context.Context, id string, memGiB float64, cpus int) (PoolSizeCheck, error) {
	v, err := s.Get(ctx, id)
	if err != nil {
		return PoolSizeCheck{}, err
	}
	cur := v.Settings
	out := PoolSizeCheck{CurMemGiB: cur.PoolMemoryGiB, CurCPUs: cur.PoolCPUs}
	if memGiB < 0 || cpus < 0 {
		return out, fmt.Errorf("%w: the pool size cannot be negative", ErrInvalid)
	}
	if memGiB == 0 && cpus == 0 {
		out.Known, out.Result.OK = true, true
		out.Shrinks = cur.PoolMemoryGiB > 0 || cur.PoolCPUs > 0
		return out, nil
	}
	if memGiB <= 0 || cpus <= 0 {
		return out, fmt.Errorf("%w: set the pool memory and the pool CPUs together", ErrInvalid)
	}
	next := cur
	next.PoolMemoryGiB, next.PoolCPUs = memGiB, cpus
	if why := (poolsize.WSLConfig{
		MemoryBytes: v.Machine.Facts.WSL.MemoryBytes, MemorySet: v.Machine.Facts.WSL.MemoryBytes > 0,
		Processors: v.Machine.Facts.WSL.Processors, ProcSet: v.Machine.Facts.WSL.Processors > 0,
	}).Refusal(v.Machine.Facts.MemoryBytes, v.Machine.Facts.Threads); why != "" {
		out.Known = true
		out.Result = poolsize.Result{Reason: why, Short: "docker", Decided: ".wslconfig"}
		return out, nil
	}
	res, err := s.CheckPoolSize(id, v.Machine, cur, next, false)
	if err != nil {
		out.Why = strings.TrimPrefix(err.Error(), ErrInvalid.Error()+": ")
		return out, nil
	}
	out.Known, out.Result, out.Shrinks = true, res, res.NeedsShrinkConfirm
	return out, nil
}
