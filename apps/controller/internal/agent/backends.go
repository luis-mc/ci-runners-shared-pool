package agent

import (
	"context"
	"strings"
	"time"

	agentv1 "github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/gen/agent/v1"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/load"
)

// DetectBackends reports each isolation backend (machines R5.11): present or
// not, usable or not, its version, and the reason when it is not usable.
// Detected is not allowed (R5.12): the user allows backends per machine. A
// persistent runner is always listed as usable. Order is stable.
func DetectBackends(_ context.Context, sys Sys) []*agentv1.BackendSupport {
	out := []*agentv1.BackendSupport{{Name: "persistent", Present: true, Usable: true}}
	out = append(out, detectDocker(sys), detectPodman(sys), detectNerdctl(sys))
	out = append(out, detectWSL2(sys), detectHyperV(sys), detectKVM(sys), detectTart(sys), detectFirecracker(sys))
	return out
}

func trim(s string) string { return strings.TrimSpace(strings.ReplaceAll(s, "\x00", "")) }

func detectDocker(sys Sys) *agentv1.BackendSupport {
	b := &agentv1.BackendSupport{Name: "docker"}
	res, ok := load.DockerRun(sys, "version", "--format", "{{.Server.Version}}")
	if !ok {
		b.Reason = "docker is not installed"
		return b
	}
	b.Present = true
	if res.Code != 0 {
		// The client runs but the engine does not answer.
		b.Reason = "docker is installed but the engine does not answer; start Docker"
		if v, ok2 := sys.Run([]string{"docker", "--version"}); ok2 && v.Code == 0 {
			b.Version = dockerClientVersion(v.Out)
		}
		return b
	}
	b.Usable, b.Version = true, trim(res.Out)
	// Rootless is only read where docker runs natively; never through wsl.
	if r, ok := sys.Run([]string{"docker", "info", "--format", "{{.Rootless}}"}); ok && r.Code == 0 {
		b.Rootless = strings.Contains(strings.ToLower(trim(r.Out)), "true")
	}
	return b
}

func dockerClientVersion(out string) string {
	f := strings.Fields(trim(out))
	for i, w := range f {
		if strings.EqualFold(w, "version") && i+1 < len(f) {
			return strings.TrimRight(f[i+1], ",")
		}
	}
	return ""
}

func detectPodman(sys Sys) *agentv1.BackendSupport {
	b := &agentv1.BackendSupport{Name: "podman"}
	res, ok := sys.Run([]string{"podman", "version", "--format", "{{.Version}}"})
	if !ok {
		b.Reason = "podman is not installed"
		return b
	}
	b.Present = true
	if res.Code != 0 {
		b.Reason = "podman is installed but does not answer"
		return b
	}
	b.Usable, b.Version = true, trim(res.Out)
	if r, ok := sys.Run([]string{"podman", "info", "--format", "{{.Host.Security.Rootless}}"}); ok && r.Code == 0 {
		b.Rootless = strings.EqualFold(trim(r.Out), "true")
	}
	return b
}

func detectNerdctl(sys Sys) *agentv1.BackendSupport {
	b := &agentv1.BackendSupport{Name: "nerdctl"}
	res, ok := sys.Run([]string{"nerdctl", "version", "--format", "{{.Server.Components}}"})
	if !ok {
		if v, ok2 := sys.Run([]string{"nerdctl", "--version"}); ok2 && v.Code == 0 {
			b.Present, b.Reason, b.Version = true, "nerdctl is installed but containerd does not answer", dockerClientVersion(v.Out)
			return b
		}
		b.Reason = "nerdctl is not installed"
		return b
	}
	b.Present = true
	if res.Code != 0 {
		b.Reason = "nerdctl is installed but containerd does not answer"
		return b
	}
	b.Usable = true
	if v, ok2 := sys.Run([]string{"nerdctl", "--version"}); ok2 && v.Code == 0 {
		b.Version = dockerClientVersion(v.Out)
	}
	return b
}

func detectWSL2(sys Sys) *agentv1.BackendSupport {
	b := &agentv1.BackendSupport{Name: "wsl2"}
	if sys.GOOS() != "windows" {
		b.Reason = "WSL2 exists only on Windows"
		return b
	}
	res, ok := sys.Run([]string{"wsl", "--status"})
	if !ok {
		b.Reason = "wsl.exe is not installed"
		return b
	}
	b.Present = true
	// `wsl --status` can exit non-zero when no distro of the user's own is
	// installed, although Docker Desktop runs on WSL2 in its own distro. A
	// docker engine whose kernel is the WSL2 kernel proves WSL2 works, so that
	// is not reported as "not ready". Nothing is changed to find out.
	if res.Code != 0 && !engineOnWSL2(sys) {
		b.Reason = "WSL is installed but not ready; run wsl --install"
		return b
	}
	b.Usable = true
	if v, ok2 := sys.Run([]string{"wsl", "--version"}); ok2 && v.Code == 0 {
		for _, ln := range strings.Split(strings.ReplaceAll(v.Out, "\x00", ""), "\n") {
			if k, val, found := strings.Cut(ln, ":"); found && strings.Contains(strings.ToLower(k), "wsl") && strings.Contains(strings.ToLower(k), "version") {
				b.Version = strings.TrimSpace(val)
				break
			}
		}
	}
	return b
}

// engineOnWSL2 is true when the docker engine answers and runs on the WSL2
// kernel (Docker Desktop's WSL2 engine, or docker inside a WSL2 distro).
func engineOnWSL2(sys Sys) bool {
	res, ok := sys.Run([]string{"docker", "info", "--format", "{{.KernelVersion}}"})
	return ok && res.Code == 0 && strings.Contains(strings.ToLower(trim(res.Out)), "wsl2")
}

func detectHyperV(sys Sys) *agentv1.BackendSupport {
	b := &agentv1.BackendSupport{Name: "hyperv"}
	if sys.GOOS() != "windows" {
		b.Reason = "Hyper-V exists only on Windows"
		return b
	}
	res, ok := sys.Run([]string{"powershell", "-NoProfile", "-Command",
		"(Get-WindowsOptionalFeature -Online -FeatureName Microsoft-Hyper-V-All -ErrorAction SilentlyContinue).State"})
	if !ok || res.Code != 0 || trim(res.Out) == "" {
		b.Reason = "Hyper-V cannot be read (it may need admin rights or be missing)"
		return b
	}
	if strings.EqualFold(trim(res.Out), "Enabled") {
		b.Present, b.Usable = true, true
		return b
	}
	b.Reason = "Hyper-V is not enabled"
	return b
}

func detectKVM(sys Sys) *agentv1.BackendSupport {
	b := &agentv1.BackendSupport{Name: "kvm"}
	if sys.GOOS() != "linux" {
		b.Reason = "KVM exists only on Linux"
		return b
	}
	if !sys.Exists("/dev/kvm") {
		b.Reason = "no /dev/kvm"
		return b
	}
	b.Present = true
	if !sys.ReadWritable("/dev/kvm") {
		b.Reason = "/dev/kvm is not readable by the agent account; add it to the kvm group"
		return b
	}
	b.Usable = true
	return b
}

func detectTart(sys Sys) *agentv1.BackendSupport {
	b := &agentv1.BackendSupport{Name: "tart"}
	if sys.GOOS() != "darwin" || sys.Arch() != "arm64" {
		b.Reason = "Tart needs a Mac with Apple silicon"
		return b
	}
	res, ok := sys.Run([]string{"tart", "--version"})
	if !ok || res.Code != 0 {
		b.Reason = "tart is not installed"
		return b
	}
	b.Present, b.Usable, b.Version = true, true, trim(res.Out)
	return b
}

func detectFirecracker(sys Sys) *agentv1.BackendSupport {
	b := &agentv1.BackendSupport{Name: "firecracker"}
	if sys.GOOS() != "linux" {
		b.Reason = "Firecracker exists only on Linux"
		return b
	}
	res, ok := sys.Run([]string{"firecracker", "--version"})
	if !ok || res.Code != 0 {
		b.Reason = "firecracker is not installed"
		return b
	}
	b.Present = true
	if f := strings.Fields(trim(res.Out)); len(f) >= 2 {
		b.Version = strings.TrimPrefix(f[1], "v")
	}
	if !sys.Exists("/dev/kvm") || !sys.ReadWritable("/dev/kvm") {
		b.Reason = "Firecracker needs /dev/kvm readable by the agent account"
		return b
	}
	b.Usable = true
	return b
}

// DetectAll builds the Facts message with its backends.
func DetectAll(ctx context.Context, sys Sys, dataDir string, now time.Time) *agentv1.Facts {
	f := DetectFacts(ctx, sys, dataDir, now)
	f.Backends = DetectBackends(ctx, sys)
	return f
}
