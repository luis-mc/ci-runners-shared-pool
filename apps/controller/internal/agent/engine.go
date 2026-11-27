package agent

import (
	"strings"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/load"
)

// EngineKind says which container engine a PC uses (runners R5.47, machines
// R5.18). The agent reads it; it never changes an engine setting.
type EngineKind string

// Engine kinds.
const (
	EngineNone EngineKind = "none"
	// EngineWSLViaCLI is Docker in WSL2, reached through the Windows docker command.
	EngineWSLViaCLI EngineKind = "docker-wsl2-windows-cli"
	// EngineWSLDirect is Docker in WSL2, reached through `wsl docker` because
	// the Windows command cannot run.
	EngineWSLDirect EngineKind = "docker-wsl2-wsl"
	// EngineDesktop is Docker Desktop.
	EngineDesktop EngineKind = "docker-desktop"
	// EngineNative is Docker on Linux or macOS.
	EngineNative EngineKind = "docker-native"
)

// EngineReport is what the agent found of the container engine.
type EngineReport struct {
	Kind    EngineKind
	Usable  bool
	Version string // server version
	Reason  string // why it is not usable
	// ContainerOS is the operating system the engine's containers run, "linux"
	// or "windows", read from `docker info`. Empty when the engine does not
	// answer. Docker Desktop on WSL2 answers "linux" on Windows.
	ContainerOS string
}

const engineVersionFormat = "{{.Server.Version}}"

// DetectEngine finds the container engine and tells Docker Desktop from Docker
// in WSL2 by the operating system the engine reports (Docker Desktop answers
// "Docker Desktop"). On Windows the Windows `docker` command comes first and
// `wsl docker` only when that command cannot answer.
func DetectEngine(sys Sys) EngineReport {
	if sys.GOOS() != "windows" {
		v, ran := engineVersion(sys, "docker")
		switch {
		case v != "":
			return EngineReport{Kind: EngineNative, Usable: true, Version: v, ContainerOS: engineOSType(sys, "docker")}
		case ran:
			return EngineReport{Kind: EngineNative, Reason: "the docker command is installed but the engine does not answer"}
		}
		return EngineReport{Kind: EngineNone, Reason: "the docker command is not installed"}
	}
	v, ran := engineVersion(sys, "docker")
	if v != "" {
		kind := EngineWSLViaCLI
		if res, ok := sys.Run([]string{"docker", "info", "--format", "{{.OperatingSystem}}"}); ok && res.Code == 0 && IsDockerDesktop(res.Out) {
			kind = EngineDesktop
		}
		return EngineReport{Kind: kind, Usable: true, Version: v, ContainerOS: engineOSType(sys, "docker")}
	}
	if w, wran := engineVersion(sys, "wsl", "docker"); w != "" {
		// A docker engine inside a WSL2 distro runs Linux containers.
		return EngineReport{Kind: EngineWSLDirect, Usable: true, Version: w, ContainerOS: "linux"}
	} else if wran && !ran {
		return EngineReport{Kind: EngineNone, Reason: "WSL2 is there but has no docker engine, and the Windows docker command is not installed"}
	}
	if ran {
		return EngineReport{Kind: EngineWSLViaCLI, Reason: "the Windows docker command is installed but the engine does not answer (Docker Desktop is stopped, or the engine in WSL2 is down)"}
	}
	return EngineReport{Kind: EngineNone, Reason: "no docker engine: the Windows docker command cannot run and WSL2 has no docker"}
}

// engineOSType reads `{{.OSType}}` of `docker info`: the OS the containers of
// the engine run. Anything but linux or windows is "" (CRLF-safe).
func engineOSType(sys load.Probe, prog string, pre ...string) string {
	argv := append(append([]string{prog}, pre...), "info", "--format", "{{.OSType}}")
	res, ok := sys.Run(argv)
	if !ok || res.Code != 0 {
		return ""
	}
	switch v := strings.ToLower(trim(res.Out)); v {
	case "linux", "windows":
		return v
	}
	return ""
}

// IsDockerDesktop reads the `{{.OperatingSystem}}` of `docker info`.
func IsDockerDesktop(out string) bool {
	return strings.Contains(strings.ToLower(trim(out)), "docker desktop")
}

// engineVersion runs `<prog> [pre...] version` and returns the server version
// when the engine answers, and whether the program ran at all.
func engineVersion(sys load.Probe, prog string, pre ...string) (version string, ran bool) {
	argv := append(append([]string{prog}, pre...), "version", "--format", engineVersionFormat)
	res, ok := sys.Run(argv)
	if !ok {
		return "", false
	}
	if res.Code == 0 {
		return trim(res.Out), true
	}
	return "", true
}

// WSLKVM is what the WSL2 VM shows of KVM (machines R5.53).
type WSLKVM struct {
	Known         bool // the WSL VM could be asked
	DevicePresent bool // /dev/kvm exists in the WSL VM
	ModuleLoaded  bool // kvm_intel or kvm_amd is loaded
	Module        string
}

// WSLKVMScript prints `device=1` or `device=0`, then one `module=<name>` line
// per loaded kvm_intel or kvm_amd. It reads only.
const WSLKVMScript = `if [ -e /dev/kvm ]; then echo device=1; else echo device=0; fi; ` +
	`grep -E '^kvm_(intel|amd) ' /proc/modules | cut -d' ' -f1 | sed 's/^/module=/'`

// ParseWSLKVM reads the output of WSLKVMScript. Output with no `device=` line
// (wsl.exe failed, or printed UTF-16 noise) is not Known.
func ParseWSLKVM(out string) WSLKVM {
	var k WSLKVM
	for _, ln := range strings.Split(strings.ReplaceAll(trimKeepNL(out), "\r", ""), "\n") {
		key, val, ok := strings.Cut(strings.TrimSpace(ln), "=")
		if !ok {
			continue
		}
		switch key {
		case "device":
			k.Known, k.DevicePresent = true, val == "1"
		case "module":
			k.ModuleLoaded, k.Module = true, val
		}
	}
	return k
}

func trimKeepNL(s string) string { return strings.ReplaceAll(s, "\x00", "") }

// ReadWSLKVM asks the WSL2 VM about KVM. Off Windows, or when wsl cannot be
// run, the result is not Known. It never loads a module.
func ReadWSLKVM(sys Sys) WSLKVM {
	if sys.GOOS() != "windows" {
		return WSLKVM{}
	}
	res, ok := sys.Run([]string{"wsl", "-e", "sh", "-c", WSLKVMScript})
	if !ok || res.Code != 0 {
		return WSLKVM{}
	}
	return ParseWSLKVM(res.Out)
}
