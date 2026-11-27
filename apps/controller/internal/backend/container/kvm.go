package container

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrPrivileged is returned for run arguments that make a container privileged
// or give it the host's namespaces (runners R6.1).
var ErrPrivileged = errors.New("a job container is never privileged and never shares a host namespace")

// KVMCgroupRule allows the container to use the KVM character device 10:232.
// The device is never mapped with --device: a mapping to a file that is missing
// at Docker's boot fails the container start, and the in-container wait never
// runs (handover lesson 28). The pool's kvm-wait.sh creates /dev/kvm with
// mknod once /proc/misc lists kvm.
const KVMCgroupRule = "c 10:232 rwm"

// KVMArgs returns the `docker run` arguments that allow /dev/kvm. gid is the
// group id that owns the device on the host (0 means unknown and adds no group).
func KVMArgs(gid int) []string {
	args := []string{"--device-cgroup-rule", KVMCgroupRule}
	if gid > 0 {
		args = append(args, "--group-add", fmt.Sprint(gid))
	}
	return args
}

// KVM states of a pool (machines R5.53, doctor R5.32).
const (
	ReasonKVMNone       = "no /dev/kvm in the container engine"
	ReasonKVMNotUsable  = "/dev/kvm exists but the runner cannot open it"
	ReasonKVMWaitedOut  = "/dev/kvm did not appear in time; the pool started without KVM"
	ReasonKVMLoaderGone = "the kvm loader service is not running"
)

// KVMDecision is what the pool does about KVM (runners R5.41, R5.36).
type KVMDecision struct {
	// Device is true when /dev/kvm is passed to the runner containers.
	Device bool
	// Label is true when the runner carries the kvm label. It needs a usable device.
	Label bool
	// Reason says why there is no label; empty when there is one.
	Reason string
}

// DecideKVM turns what was seen into the decision: the device and the label
// only when /dev/kvm exists and the runner can open it. After a bounded wait
// that found nothing, the pool starts without KVM and drops the label.
func DecideKVM(exists, usable, waitedOut bool) KVMDecision {
	switch {
	case exists && usable:
		return KVMDecision{Device: true, Label: true}
	case exists:
		return KVMDecision{Reason: ReasonKVMNotUsable}
	case waitedOut:
		return KVMDecision{Reason: ReasonKVMWaitedOut}
	}
	return KVMDecision{Reason: ReasonKVMNone}
}

// KVMWait is the bounded wait for /dev/kvm after a Docker or WSL restart
// (handover lesson 25).
type KVMWait struct {
	// Exists reports whether /dev/kvm is there now.
	Exists func() bool
	// Sleep waits; nil uses a timer that ends with ctx.
	Sleep func(ctx context.Context, d time.Duration)
	// Interval between looks and the bound; zero values use 2 s and 60 s.
	Interval, Bound time.Duration
	// Log gets one line when the wait gives up.
	Log func(msg string)
}

// Wait looks for /dev/kvm until it is there or the bound is used up. It
// returns true when the device appeared. On false the pool starts without KVM
// (the caller drops the label with DecideKVM(false, false, true)).
func (w KVMWait) Wait(ctx context.Context) bool {
	interval, bound := w.Interval, w.Bound
	if interval <= 0 {
		interval = 2 * time.Second
	}
	if bound <= 0 {
		bound = 60 * time.Second
	}
	sleep := w.Sleep
	if sleep == nil {
		sleep = func(ctx context.Context, d time.Duration) {
			t := time.NewTimer(d)
			defer t.Stop()
			select {
			case <-ctx.Done():
			case <-t.C:
			}
		}
	}
	for waited := time.Duration(0); ; waited += interval {
		if w.Exists() {
			return true
		}
		if waited+interval > bound || ctx.Err() != nil {
			if w.Log != nil {
				w.Log(fmt.Sprintf("kvm: /dev/kvm did not appear within %s; starting without KVM", bound))
			}
			return false
		}
		sleep(ctx, interval)
	}
}

// LoaderModule picks the KVM module for a CPU from /proc/cpuinfo text: kvm_intel
// for Intel, kvm_amd for AMD, empty for anything else (machines R5.53).
func LoaderModule(cpuinfo string) string {
	for _, line := range strings.Split(cpuinfo, "\n") {
		if !strings.HasPrefix(line, "vendor_id") {
			continue
		}
		switch {
		case strings.Contains(line, "GenuineIntel"):
			return "kvm_intel"
		case strings.Contains(line, "AuthenticAMD"):
			return "kvm_amd"
		}
	}
	return ""
}
