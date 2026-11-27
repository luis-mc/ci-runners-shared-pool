package load

import (
	"fmt"
	"testing"
	"time"
)

const g = int64(1) << 30

func gibs(x float64) int64 { return int64(x * float64(g)) }

func psOut(ws, priv int64) Result { return Result{Out: fmt.Sprintf("%d\n%d\n", ws, priv)} }

func winProbe(cmds ...fakeCmd) *fakeProbe {
	return &fakeProbe{os: "windows", cmds: cmds, cpus: 20, now: time.Unix(1_000_000, 0)}
}

// R5.17 ports test_dl177_pool_measure.py VmReadTests.
func TestPrivateBytesBeatATrimmedWorkingSet(t *testing.T) {
	ws, priv := gibs(5.7), gibs(9.8)
	p := winProbe(fakeCmd{[]string{"powershell"}, psOut(ws, priv), true})
	if a, b := WSLVMParts(p); a != ws || b != priv {
		t.Fatalf("parts = %d %d", a, b)
	}
	if got := WSLVM(p); got != priv {
		t.Fatalf("vm = %d, want private bytes %d", got, priv)
	}
}

func TestWorkingSetWinsWhenLarger(t *testing.T) {
	ws, priv := gibs(11.9), gibs(9.4)
	if got := WSLVM(winProbe(fakeCmd{[]string{"powershell"}, psOut(ws, priv), true})); got != ws {
		t.Fatalf("vm = %d, want working set %d", got, ws)
	}
}

func TestNoVMReadsZero(t *testing.T) {
	if got := WSLVM(winProbe(fakeCmd{[]string{"powershell"}, Result{}, true})); got != 0 {
		t.Fatalf("empty output: %d", got)
	}
	if got := WSLVM(winProbe()); got != 0 {
		t.Fatalf("cannot run: %d", got)
	}
}

func TestLinuxHasNoVM(t *testing.T) {
	p := linux(nil)
	if WSLVM(p) != 0 || len(p.calls) != 0 {
		t.Fatalf("Linux must not look for a VM; calls = %v", p.calls)
	}
}

// R5.17: the VM is never less than what its containers use, and the
// memory figure decides on the VM, not on the trimmed working set. Ports
// HostTests.test_vm_is_never_less_than_container_use.
func TestVMIsNeverLessThanContainerUse(t *testing.T) {
	ws, priv := gibs(5.7), int64(0)
	if got := wslVM(ws, priv, 9*g); got != 9*g {
		t.Fatalf("vm = %d, want containers 9 GiB", got)
	}
	// Host used 19 of 31.7 GiB (59.9%) with a trimmed 5.7 GiB working set; the
	// VM really holds 9.8 GiB, so real use is 19 - 5.7 + 9.8 = 23.1 GiB.
	total := gibs(31.7)
	p := winProbe(fakeCmd{[]string{"powershell"}, psOut(ws, gibs(9.8)), true})
	p.winMem, p.winTotal = []float64{59.9}, total
	p.winTimes = []Counters{{100, 1000}}
	s := NewSampler(p)
	s.Refresh()
	r := s.Sample()
	if !r.MemoryKnown || r.MemoryPct < 72.5 || r.MemoryPct > 73.5 {
		t.Fatalf("memory = %v, want about 72.9 (VM corrected)", r.MemoryPct)
	}
	if r.WSLVMBytes != gibs(9.8) {
		t.Fatalf("WSLVMBytes = %d", r.WSLVMBytes)
	}
}

func TestWindowsCPUUnknownOnFirstSampleThenDelta(t *testing.T) {
	p := winProbe()
	p.winMem, p.winTotal = []float64{50}, 16*g
	p.winTimes = []Counters{{100, 1000}, {400, 1500}}
	s := NewSampler(p)
	if r := s.Sample(); r.CPUKnown {
		t.Fatal("Windows has no CPU figure for the first sample (spec section 8)")
	}
	p.tick(5 * time.Second)
	if r := s.Sample(); !r.CPUKnown || r.CPUPct != 60.0 {
		t.Fatalf("second = %+v, want CPU 60", r)
	}
}
