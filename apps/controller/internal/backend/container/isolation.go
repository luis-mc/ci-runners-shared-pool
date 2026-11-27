package container

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend/sys"
)

// Isolation is how the engine isolates a Windows container from the host
// (runners R5.65). Process isolation is the default and shares the host
// kernel; Hyper-V isolation runs the container in a small utility VM.
type Isolation string

// Isolation modes of a pool container.
const (
	IsolationProcess Isolation = "process"
	IsolationHyperV  Isolation = "hyperv"
)

// ErrIsolation is returned for a mode that is not known or not usable here.
var ErrIsolation = errors.New("container isolation refused")

// Reasons Hyper-V isolation is refused (runners R5.65). The probe only reads;
// it never edits a host setting or enables a Windows feature.
const (
	ReasonHyperVNotWindows   = "Hyper-V isolation needs a Windows container engine"
	ReasonHyperVNoHypervisor = "Hyper-V is not available on this machine: the Hyper-V feature is not enabled or the hardware cannot run it. crsp does not enable Windows features; enable Hyper-V yourself or use process isolation"
	ReasonHyperVUnreadable   = "crsp could not tell whether Hyper-V is available"
)

// ParseIsolation reads the runner-definition field. Empty means the default,
// process isolation.
func ParseIsolation(s string) (Isolation, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", string(IsolationProcess):
		return IsolationProcess, nil
	case string(IsolationHyperV):
		return IsolationHyperV, nil
	}
	return "", fmt.Errorf("%w: unknown isolation %q (use process or hyperv)", ErrIsolation, s)
}

// IsolationArgs returns the `docker run` arguments of the mode. Process
// isolation is the engine default and adds no flag.
func IsolationArgs(i Isolation) []string {
	if i == IsolationHyperV {
		return []string{"--isolation=hyperv"}
	}
	return nil
}

// hypervProbeScript reads whether a hypervisor is running. It changes nothing.
const hypervProbeScript = "(Get-CimInstance -ClassName Win32_ComputerSystem).HypervisorPresent"

// ProbeHyperV checks that the engine runs Windows containers and that the host
// has Hyper-V, and returns a clear refusal when not. It only reads.
func (b *Backend) ProbeHyperV(ctx context.Context) error {
	res, err := b.eng.Run(ctx, "info", "--format", "{{.OSType}}")
	if err != nil {
		return fmt.Errorf("%w: %s: %v", ErrIsolation, ReasonHyperVUnreadable, err)
	}
	if res.ExitCode != 0 || strings.ToLower(strings.TrimSpace(res.Stdout)) != "windows" {
		return fmt.Errorf("%w: %s", ErrIsolation, ReasonHyperVNotWindows)
	}
	res, err = b.d.Cmd.Run(ctx, sys.Command{Name: "powershell", Args: []string{"-NoProfile", "-NonInteractive", "-Command", hypervProbeScript}})
	if err != nil || res.ExitCode != 0 {
		return fmt.Errorf("%w: %s", ErrIsolation, ReasonHyperVUnreadable)
	}
	if !strings.EqualFold(strings.TrimSpace(res.Stdout), "true") {
		return fmt.Errorf("%w: %s", ErrIsolation, ReasonHyperVNoHypervisor)
	}
	return nil
}
