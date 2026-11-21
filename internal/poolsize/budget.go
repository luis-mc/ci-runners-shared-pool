package poolsize

import (
	"fmt"
	"math"
)

// Floors and ratios of machines R5.58.
const (
	MinGiB       = 4
	MinCPUs      = 2
	GiBPerCPU    = 1.5
	DefaultMemPc = 85
	DefaultCPUPc = 60
	// SafetyMarginGiB is kept free on top of the host's own p95 use when the
	// room is sized from the measured figure (OPS61, machines R5.68).
	SafetyMarginGiB = 1.0
	// MeasuredMinHours is how many hours of samples the p95 needs before it
	// replaces the fixed share.
	MeasuredMinHours = 24
)

// Basis of a recommendation: the measured p95 of the host's own use, or the
// fixed share of the free room while the samples are fewer than 24 h.
const (
	BasisMeasured  = "measured"
	BasisMeasuring = "measuring"
)

// MeasuredRoom is the room for the pool when the host's own use is measured:
// total memory x memory budget, less the host's p95 use and the safety margin,
// never below 0 (OPS61).
func MeasuredRoom(totalGiB float64, memPct int, hostP95GiB float64) float64 {
	if memPct <= 0 {
		memPct = DefaultMemPc
	}
	room := totalGiB*float64(memPct)/100 - hostP95GiB - SafetyMarginGiB
	if room < 0 {
		return 0
	}
	return room
}

// Inputs are the figures the check reads. All memory is in bytes.
type Inputs struct {
	TotalBytes int64
	TotalCPUs  int
	// UsedOutsideBytes is memory used outside WSL (median of samples).
	UsedOutsideBytes int64
	// WSLVMBytes is what the WSL VM holds now; GuestBytes is the memory the
	// runner guests take (R5.54). Both count against the memory budget.
	WSLVMBytes, GuestBytes int64
	// DockerCeilingBytes is the most Docker can be given (the declared WSL
	// memory); 0 when there is no ceiling or it is unknown.
	DockerCeilingBytes int64
	BudgetMemPct       int
	BudgetCPUPct       int
	// HostP95Bytes is the rolling 7-day p95 of memory the PC uses without the
	// pool; HostReady is true when the samples cover 24 h, HostSpanHours is how
	// long they cover (OPS61).
	HostP95Bytes  int64
	HostReady     bool
	HostSpanHours float64
	// WSLPool is true when the pool runs in WSL2; the declared cap is
	// DockerCeilingBytes (0: `.wslconfig` sets none).
	WSLPool bool
}

// Request is the owner's pool size and what it replaces.
type Request struct {
	MemGiB float64
	CPUs   int
	// CurMemGiB and CurCPUs are the saved size; 0 means none yet.
	CurMemGiB float64
	CurCPUs   int
	// ConfirmShrink is the owner's explicit yes to a size below the current.
	ConfirmShrink bool
}

// Result is what the check shows, accepted or not. The sizing inputs are
// always filled so the console can show them (R5.58).
type Result struct {
	OK bool
	// NeedsShrinkConfirm is true when the size passes but is lower than the
	// current one and was not confirmed; OK is false then.
	NeedsShrinkConfirm bool
	Reason             string
	// Short names the budget that is short: memory, cpu, floor, ratio, docker.
	Short string
	// Nearest are sizes that pass, as sentences.
	Nearest []string

	TotalGiB, UsedOutsideGiB, MemBudgetGiB, RoomGiB float64
	CPUBudget                                       int
	DockerCeilingGiB                                float64
	Decided                                         string
	// Half and ThreeQuarters are the recommended sizes (LOOK22): that share
	// of the room. Set whenever the sizing inputs are.
	Half, ThreeQuarters Recommended

	// Basis says what the recommended sizes rest on: BasisMeasured or
	// BasisMeasuring. HostP95GiB and HostSpanHours are the measured basis;
	// MeasuredRoomGiB is total x budget less the p95 and SafetyMarginGiB, set
	// only when measured. PresetRoomGiB is the room the recommended sizes take
	// a share of: the smaller of the measured room and the room right now.
	Basis           string
	HostP95GiB      float64
	HostSpanHours   float64
	MeasuredRoomGiB float64
	PresetRoomGiB   float64
	// LeavesGiB is what the size leaves for the PC; LeavesLessThanHost is the
	// warning (not a block) when that is below the host's p95.
	LeavesGiB          float64
	LeavesLessThanHost bool
	// WSLPool, WSLCapGiB and WSLCapSet describe the WSL2 memory cap: the pool
	// runs in WSL2, the cap `.wslconfig` declares, and whether it sets one.
	WSLPool    bool
	WSLCapGiB  float64
	WSLCapSet  bool
	CapAbovePC bool
}

// Recommended is one recommended pool size.
type Recommended struct {
	MemGiB int
	CPUs   int
}

// Recommend sizes the pool at a share of the room: the memory is that share
// of roomGiB rounded down to whole GiB, at least the floor; the CPUs are the
// same share of the CPU budget, at most memory / GiBPerCPU, at least the floor.
func Recommend(roomGiB float64, cpuBudget int, share float64) Recommended {
	mem := int(math.Floor(roomGiB*share + 1e-9))
	if mem < MinGiB {
		mem = MinGiB
	}
	cpus := int(math.Floor(float64(cpuBudget)*share + 1e-9))
	if byMem := int(math.Floor(float64(mem)/GiBPerCPU + 1e-9)); cpus > byMem {
		cpus = byMem
	}
	if cpus < MinCPUs {
		cpus = MinCPUs
	}
	return Recommended{MemGiB: mem, CPUs: cpus}
}

func gib(b int64) float64 { return float64(b) / float64(GiB) }

func trim(f float64) string {
	if f == math.Trunc(f) {
		return fmt.Sprintf("%d", int64(f))
	}
	return fmt.Sprintf("%.1f", f)
}

func roundUp(f float64) float64 { return math.Ceil(f*2-1e-9) / 2 }

// Check decides whether the pool size fits (R5.58). Order of the checks: the
// floors, the CPU budget, 1.5 GiB per CPU, the memory budget, the Docker
// ceiling. A refusal names the budget that is short and the nearest sizes that
// pass. A size that passes but is below the current one is held until
// ConfirmShrink is true; a shrink is never a side effect.
func Check(in Inputs, r Request) Result {
	memPct, cpuPct := in.BudgetMemPct, in.BudgetCPUPct
	if memPct <= 0 {
		memPct = DefaultMemPc
	}
	if cpuPct <= 0 {
		cpuPct = DefaultCPUPc
	}
	res := Result{
		TotalGiB:         gib(in.TotalBytes),
		UsedOutsideGiB:   gib(in.UsedOutsideBytes),
		DockerCeilingGiB: gib(in.DockerCeilingBytes),
	}
	res.MemBudgetGiB = res.TotalGiB * float64(memPct) / 100
	// The room is the budget less what is used outside WSL and the guests.
	// The WSL VM itself is what the pool becomes, so it is not subtracted.
	res.RoomGiB = res.MemBudgetGiB - res.UsedOutsideGiB - gib(in.GuestBytes)
	if res.RoomGiB < 0 {
		res.RoomGiB = 0
	}
	res.CPUBudget = in.TotalCPUs * cpuPct / 100
	res.Basis, res.HostSpanHours = BasisMeasuring, in.HostSpanHours
	res.PresetRoomGiB = res.RoomGiB
	if in.HostReady {
		res.Basis = BasisMeasured
		res.HostP95GiB = gib(in.HostP95Bytes)
		res.MeasuredRoomGiB = MeasuredRoom(res.TotalGiB, memPct, res.HostP95GiB)
		if res.MeasuredRoomGiB < res.PresetRoomGiB {
			res.PresetRoomGiB = res.MeasuredRoomGiB
		}
		res.LeavesGiB = res.TotalGiB - r.MemGiB
		res.LeavesLessThanHost = r.MemGiB > 0 && res.LeavesGiB < res.HostP95GiB
		res.CapAbovePC = in.DockerCeilingBytes > 0 && res.DockerCeilingGiB > res.TotalGiB-res.HostP95GiB
	}
	res.WSLPool, res.WSLCapSet, res.WSLCapGiB = in.WSLPool, in.DockerCeilingBytes > 0, res.DockerCeilingGiB
	res.Half = Recommend(res.PresetRoomGiB, res.CPUBudget, 0.5)
	res.ThreeQuarters = Recommend(res.PresetRoomGiB, res.CPUBudget, 0.75)
	maxCPUsForMem := int(math.Floor(r.MemGiB/GiBPerCPU + 1e-9))
	refuse := func(short, decided, reason string, nearest ...string) Result {
		res.Short, res.Decided, res.Reason, res.Nearest = short, decided, reason, nearest
		return res
	}
	switch {
	case r.MemGiB < MinGiB || r.CPUs < MinCPUs:
		return refuse("floor", "floor",
			fmt.Sprintf("the pool needs at least %d GiB and %d CPUs", MinGiB, MinCPUs),
			fmt.Sprintf("%d GiB with %d CPUs is the smallest pool", MinGiB, MinCPUs))
	case r.CPUs > res.CPUBudget:
		return refuse("cpu", "CPU budget",
			fmt.Sprintf("%d CPUs is above the CPU budget: %d%% of %d CPUs is %d", r.CPUs, cpuPct, in.TotalCPUs, res.CPUBudget),
			fmt.Sprintf("%d CPUs fits the CPU budget", res.CPUBudget))
	case r.MemGiB > res.RoomGiB:
		near := fmt.Sprintf("%s GiB fits", trim(math.Floor(res.RoomGiB*2)/2))
		if res.RoomGiB >= MinGiB {
			c := int(math.Floor(res.RoomGiB / GiBPerCPU))
			if c > res.CPUBudget {
				c = res.CPUBudget
			}
			near = fmt.Sprintf("%s GiB fits %d CPUs", trim(math.Floor(res.RoomGiB*2)/2), c)
		}
		return refuse("memory", "memory budget",
			fmt.Sprintf("%s GiB is above the memory room: %d%% of %s GiB is %s GiB, less %s GiB used outside WSL and guests leaves %s GiB",
				trim(r.MemGiB), memPct, trim(res.TotalGiB), trim(res.MemBudgetGiB), trim(res.UsedOutsideGiB+gib(in.GuestBytes)), trim(res.RoomGiB)),
			near)
	case float64(r.CPUs)*GiBPerCPU > r.MemGiB+1e-9:
		need := roundUp(float64(r.CPUs) * GiBPerCPU)
		reason := fmt.Sprintf("%s GiB fits %d CPUs; %d CPUs needs %s GiB", trim(r.MemGiB), maxCPUsForMem, r.CPUs, trim(need))
		return refuse("ratio", "1.5 GiB per CPU", reason,
			fmt.Sprintf("%s GiB fits %d CPUs", trim(r.MemGiB), maxCPUsForMem),
			fmt.Sprintf("%d CPUs needs %s GiB", r.CPUs, trim(need)))
	case in.DockerCeilingBytes > 0 && r.MemGiB > res.DockerCeilingGiB:
		return refuse("docker", "Docker ceiling",
			fmt.Sprintf("%s GiB is above what Docker can be given: .wslconfig allows %s GiB", trim(r.MemGiB), trim(res.DockerCeilingGiB)),
			fmt.Sprintf("%s GiB fits under .wslconfig", trim(res.DockerCeilingGiB)))
	}
	res.Decided = "fits"
	shrinks := (r.CurMemGiB > 0 && r.MemGiB < r.CurMemGiB) || (r.CurCPUs > 0 && r.CPUs < r.CurCPUs)
	if shrinks && !r.ConfirmShrink {
		res.NeedsShrinkConfirm = true
		res.Reason = fmt.Sprintf("this lowers the pool from %s GiB and %d CPUs to %s GiB and %d CPUs; confirm to shrink it",
			trim(r.CurMemGiB), r.CurCPUs, trim(r.MemGiB), r.CPUs)
		return res
	}
	res.OK = true
	return res
}
