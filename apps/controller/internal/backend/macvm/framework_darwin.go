//go:build darwin

package macvm

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend/vmclone"
)

// vmProcess is the process Virtualization.framework runs for each VM.
const vmProcess = "com.apple.Virtualization.VirtualMachine"

// NewFramework is the real Framework on a Mac.
//
// BUILT IN PART: Host and Visible are built. Boot is NOT YET IMPLEMENTED: the
// Objective-C bridge (VZVirtualMachineConfiguration, VZMacOSVirtualMachine,
// start and stop, restore image install) is the one part that needs a real
// Mac to write and verify, and the only place cgo may be added. Until then
// Boot refuses with ErrNoBridge and no job starts in a VM.
func NewFramework() Framework { return macFramework{} }

type macFramework struct{}

func run(ctx context.Context, name string, args ...string) (string, error) {
	var out bytes.Buffer
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout = &out
	err := cmd.Run()
	return strings.TrimSpace(out.String()), err
}

func (macFramework) Host(ctx context.Context) (Host, error) {
	v, err := run(ctx, "sw_vers", "-productVersion")
	if err != nil {
		return Host{}, fmt.Errorf("sw_vers: %w", err)
	}
	arch := runtime.GOARCH
	// A translated (Rosetta) agent reports amd64 on Apple silicon.
	if t, err := run(ctx, "sysctl", "-n", "sysctl.proc_translated"); err == nil && t == "1" {
		arch = "arm64"
	}
	return Host{OS: "darwin", Arch: arch, MacOS: v}, nil
}

func (macFramework) Visible(ctx context.Context) (int, error) {
	// pgrep exits 1 when nothing matches: that is zero VMs, not an error.
	out, err := run(ctx, "pgrep", "-f", vmProcess)
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 1 {
			return 0, nil
		}
		return 0, fmt.Errorf("pgrep: %w", err)
	}
	n := 0
	for _, l := range strings.Split(out, "\n") {
		if strings.TrimSpace(l) != "" {
			n++
		}
	}
	return n, nil
}

func (macFramework) Boot(context.Context, vmclone.BootSpec) (vmclone.VM, error) {
	return nil, ErrNoBridge
}
