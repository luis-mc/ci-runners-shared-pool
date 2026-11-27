// Package macvm runs a job in a macOS VM on Apple's Virtualization.framework
// with crsp's own code, no external VM tool (runners R5.29, owner decision
// D54). At most two macOS VMs run on one Mac, counting VMs the agent did not
// start (Apple's licence); the limit cannot be raised (runners R6.6).
//
// All framework work sits behind Framework, which one darwin-only file
// implements. Everything else here is pure Go and is tested with fakes. The
// package plugs into vmclone: Disks clones the base disk per job, Booter
// starts a VM under the limit, and vmclone removes both after the job.
package macvm

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend/vmclone"
)

// Name is this backend's name in Support.
const Name backend.Name = "macvm"

// MaxVMs is the hard limit of macOS VMs per Mac (runners R5.29, R6.6). It is
// a constant: no setting raises it.
const MaxVMs = 2

// MinMacOSMajor is the oldest macOS host that boots a macOS guest through
// Virtualization.framework with the restore and clone calls this backend uses.
const MinMacOSMajor = 13

// ErrAtLimit is wrapped by the reason a job waits for a free VM.
var ErrAtLimit = errors.New("macOS VM limit reached")

// ErrNoBridge is returned by a Framework that cannot start a VM.
var ErrNoBridge = errors.New("the Virtualization.framework bridge is not available")

// Host is what the agent knows about the Mac.
type Host struct {
	OS   string // darwin
	Arch string // arm64 or amd64
	// MacOS is the product version, such as "14.5".
	MacOS string
}

// Framework is every call into Virtualization.framework and the host.
type Framework interface {
	// Host reads the OS, CPU and macOS version.
	Host(ctx context.Context) (Host, error)
	// Visible counts every macOS VM running on this Mac, also VMs this agent
	// did not start.
	Visible(ctx context.Context) (int, error)
	// Boot starts a macOS VM on the disk.
	Boot(ctx context.Context, s vmclone.BootSpec) (vmclone.VM, error)
}

// MajorVersion reads the major number of a macOS version such as "14.5".
func MajorVersion(v string) (int, error) {
	head, _, _ := strings.Cut(strings.TrimSpace(v), ".")
	n, err := strconv.Atoi(head)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("macOS version %q is not a version number", v)
	}
	return n, nil
}

// Check says whether this Mac can run macOS VMs, and why not in plain words.
func Check(h Host) backend.Support {
	s := backend.Support{Name: Name, Isolation: backend.IsolationVM, OS: []string{"macos"}, Version: h.MacOS}
	switch {
	case h.OS != "darwin":
		s.Reason = "macOS VMs run only on a Mac"
		return s
	case h.Arch != "arm64":
		s.Present = true
		s.Reason = "macOS VMs need a Mac with Apple silicon; an Intel Mac is not supported"
		return s
	}
	s.Present = true
	major, err := MajorVersion(h.MacOS)
	switch {
	case err != nil:
		s.Reason = "the macOS version is unknown: " + err.Error()
	case major < MinMacOSMajor:
		s.Reason = fmt.Sprintf("macOS VMs need macOS %d or newer; this Mac has %s", MinMacOSMajor, h.MacOS)
	default:
		s.Usable = true
	}
	return s
}

// Detect reads the host and checks it.
func Detect(ctx context.Context, fw Framework) (backend.Support, error) {
	h, err := fw.Host(ctx)
	if err != nil {
		return backend.Support{}, fmt.Errorf("read the Mac: %w", err)
	}
	return Check(h), nil
}

// Count is what the agent reports (machines R5.14).
type Count struct {
	// Running is every macOS VM on the Mac, the agent's and others.
	Running int
	// Ours is the VMs this agent holds, started or starting.
	Ours  int
	Limit int
}

// Limiter keeps the number of macOS VMs at or under MaxVMs.
type Limiter struct {
	FW Framework

	mu     sync.Mutex
	ours   int // reserved: booting or booted
	booted int // booted and not yet stopped
	freed  chan struct{}
}

// foreignLocked is the VMs on the Mac that this agent did not start.
func (l *Limiter) foreignLocked(ctx context.Context) (int, int, error) {
	vis, err := l.FW.Visible(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("count macOS VMs: %w", err)
	}
	f := vis - l.booted
	if f < 0 {
		f = 0
	}
	return f, vis, nil
}

// Count reports the VMs now.
func (l *Limiter) Count(ctx context.Context) (Count, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	f, _, err := l.foreignLocked(ctx)
	if err != nil {
		return Count{}, err
	}
	return Count{Running: l.ours + f, Ours: l.ours, Limit: MaxVMs}, nil
}

// TryAcquire reserves a slot or returns ErrAtLimit with the reason. The
// returned func gives the slot back; it is safe to call twice. markBooted is
// called once the VM runs so the count does not take it for a foreign VM.
func (l *Limiter) TryAcquire(ctx context.Context) (release func(), markBooted func(), err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	f, _, err := l.foreignLocked(ctx)
	if err != nil {
		return nil, nil, err
	}
	if total := l.ours + f; total >= MaxVMs {
		return nil, nil, fmt.Errorf("%w: %d macOS VMs run on this Mac (%d started by crsp, %d by others) and Apple's licence allows %d; the job waits for a free VM",
			ErrAtLimit, total, l.ours, f, MaxVMs)
	}
	l.ours++
	var once, bonce sync.Once
	booted := false
	markBooted = func() {
		bonce.Do(func() {
			l.mu.Lock()
			defer l.mu.Unlock()
			l.booted++
			booted = true
		})
	}
	release = func() {
		once.Do(func() {
			l.mu.Lock()
			defer l.mu.Unlock()
			l.ours--
			if booted {
				l.booted--
			}
			if l.freed != nil {
				close(l.freed)
				l.freed = nil
			}
		})
	}
	return release, markBooted, nil
}

// waitCh returns a channel that closes when a slot is given back.
func (l *Limiter) waitCh() <-chan struct{} {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.freed == nil {
		l.freed = make(chan struct{})
	}
	return l.freed
}

// Acquire waits for a free slot. say is called once, with the reason, when the
// job has to wait. poll is how often to look again, because a VM the agent
// did not start can end without a signal.
func (l *Limiter) Acquire(ctx context.Context, poll time.Duration, say func(reason string)) (release, markBooted func(), err error) {
	if poll <= 0 {
		poll = 10 * time.Second
	}
	said := false
	for {
		ch := l.waitCh()
		release, markBooted, err = l.TryAcquire(ctx)
		if err == nil {
			return release, markBooted, nil
		}
		if !errors.Is(err, ErrAtLimit) {
			return nil, nil, err
		}
		if !said && say != nil {
			say(err.Error())
			said = true
		}
		t := time.NewTimer(poll)
		select {
		case <-ctx.Done():
			t.Stop()
			return nil, nil, fmt.Errorf("waiting for a free macOS VM: %w", ctx.Err())
		case <-ch:
		case <-t.C:
		}
		t.Stop()
	}
}

// Booter implements vmclone.Booter under the limit of two VMs.
type Booter struct {
	Limiter *Limiter
	// Poll is how often a waiting job looks again; 0 is ten seconds.
	Poll time.Duration
	// Say tells the user why a job waits.
	Say func(jobID, reason string)
}

var _ vmclone.Booter = (*Booter)(nil)

// Boot waits for a slot, starts the VM, and gives the slot back when the VM
// stops or the boot fails.
func (b *Booter) Boot(ctx context.Context, s vmclone.BootSpec) (vmclone.VM, error) {
	release, booted, err := b.Limiter.Acquire(ctx, b.Poll, func(r string) {
		if b.Say != nil {
			b.Say(s.JobID, r)
		}
	})
	if err != nil {
		return nil, err
	}
	vm, err := b.Limiter.FW.Boot(ctx, s)
	if err != nil {
		release()
		return nil, err
	}
	booted()
	return &slotVM{VM: vm, release: release}, nil
}

// slotVM gives its slot back after Stop, also when Stop fails: the VM is then
// treated as gone so a stuck VM cannot hold a slot for ever. The agent's next
// Visible count still sees a VM that is really running.
type slotVM struct {
	vmclone.VM
	release func()
}

func (v *slotVM) Stop(ctx context.Context) error {
	defer v.release()
	return v.VM.Stop(ctx)
}

// Report is the macOS VM state of this Mac for the machine report (machines
// R5.14).
type Report struct {
	// Known is false when the VMs could not be counted.
	Known bool
	Count
	// Unusable says why macOS VMs cannot run here; empty when they can.
	Unusable string
}

// Report reads the support and the count. A Mac that cannot run macOS VMs
// (Intel, macOS older than 13) still reports, with Known true and the reason:
// it has no VM from crsp, but a VM from another tool may still run there.
func (l *Limiter) Report(ctx context.Context) (Report, error) {
	sup, err := Detect(ctx, l.FW)
	if err != nil {
		return Report{}, err
	}
	r := Report{Limit: MaxVMs}
	if !sup.Usable {
		r.Unusable = sup.Reason
	}
	c, err := l.Count(ctx)
	if err != nil {
		return r, err
	}
	r.Known, r.Count = true, c
	return r, nil
}
