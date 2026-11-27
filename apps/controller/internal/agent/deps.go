package agent

import (
	"strings"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/agent/install"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/depsetup"
	agentv1 "github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/gen/agent/v1"
)

// DepsSys is the optional part of Sys that reads admin rights. A Sys that does
// not have it is asked through Elevated.
type DepsSys interface {
	IsAdmin() bool
}

const dockerDesktopExe = `C:\Program Files\Docker\Docker\Docker Desktop.exe`

func isAdmin(sys Sys) bool {
	if d, ok := sys.(DepsSys); ok {
		return d.IsAdmin()
	}
	return Elevated()
}

// virtualizationOff reads, read-only, whether Windows firmware has
// virtualization off. When a hypervisor already runs, virtualization is on.
// Unknown is false: crsp never claims a cause it did not read.
func virtualizationOff(sys Sys) bool {
	if sys.GOOS() != "windows" {
		return false
	}
	ps := func(expr string) (string, bool) {
		res, ok := sys.Run([]string{"powershell", "-NoProfile", "-Command", expr})
		if !ok || res.Code != 0 {
			return "", false
		}
		return strings.ToLower(trim(res.Out)), true
	}
	if v, ok := ps("(Get-CimInstance Win32_ComputerSystem).HypervisorPresent"); ok && v == "true" {
		return false
	}
	v, ok := ps("(Get-CimInstance Win32_Processor | Select-Object -First 1).VirtualizationFirmwareEnabled")
	return ok && v == "false"
}

// depsEngine reads the engine for Verify. On Windows it also looks in the WSL
// distro crsp owns, which is not the user's default distro.
func depsEngine(sys Sys) install.EngineInfo {
	e := DetectEngine(sys)
	if e.Usable {
		info := install.EngineInfo{Usable: true, Version: e.Version, ContainerOS: e.ContainerOS}
		if e.Kind == EngineWSLDirect {
			info.DockerPrefix = []string{"wsl", "docker"}
		}
		return info
	}
	if sys.GOOS() == "windows" {
		pre := []string{"-d", install.DistroName, "-u", "root", "--", "docker"}
		if v, _ := engineVersion(sys, "wsl", pre...); v != "" {
			return install.EngineInfo{Usable: true, Version: v, ContainerOS: "linux",
				DockerPrefix: append([]string{"wsl"}, pre...)}
		}
	}
	return install.EngineInfo{Reason: e.Reason}
}

// DetectDeps answers DepsCheck (DEP1): what the runner choice needs (spec 3.3)
// and what the machine has. It reads only. Windows and macOS runners on their
// own OS need nothing beyond crsp: the items are empty.
func DetectDeps(sys Sys, runnerOS, _ string) *agentv1.DepsReport {
	rep := &agentv1.DepsReport{Os: sys.GOOS(), Arch: sys.Arch(), Admin: isAdmin(sys)}
	path := "/"
	if sys.GOOS() == "windows" {
		path = `C:\`
	}
	if _, free, err := sys.Disk(path); err == nil {
		rep.DiskFreeBytes = free
	}
	if sys.GOOS() == "linux" {
		if txt, err := sys.ReadFile("/etc/os-release"); err == nil {
			rep.Distro = install.ParseOSRelease(txt).ID
		}
	}
	if sys.GOOS() == "windows" {
		rep.VirtualizationOff = virtualizationOff(sys)
	}
	if runnerOS != "linux" {
		return rep
	}
	switch sys.GOOS() {
	case "linux":
		rep.Items = append(rep.Items, linuxEngineItem(sys))
	case "windows":
		rep.Items = append(rep.Items, windowsItems(sys)...)
	case "darwin":
		rep.Items = append(rep.Items, desktopItem(sys, depsetup.ItemDockerDesktop))
	}
	return rep
}

func item(id, state, version, detail string) *agentv1.DepsItem {
	return &agentv1.DepsItem{Id: id, State: state, Version: version, Detail: detail}
}

func linuxEngineItem(sys Sys) *agentv1.DepsItem {
	id := depsetup.ItemDockerEngine
	snap := sys.Exists("/snap/bin/docker")
	if !snap {
		if res, ok := sys.Run([]string{"snap", "list", "docker"}); ok && res.Code == 0 {
			snap = true
		}
	}
	if snap {
		return item(id, depsetup.StateWrongKind, "", "Docker is installed as a snap package. crsp does not support it; use Docker's own packages.")
	}
	e := DetectEngine(sys)
	switch {
	case e.Usable && e.ContainerOS == "windows":
		return item(id, depsetup.StateWrongKind, e.Version, "The engine runs Windows containers; Linux containers are needed.")
	case e.Usable:
		return item(id, depsetup.StateOK, e.Version, "")
	case e.Kind == EngineNative:
		return item(id, depsetup.StateStopped, "", "Docker is installed but not running.")
	}
	if p := detectPodman(sys); p.Usable {
		return item(id, depsetup.StateOK, p.Version, "Podman is used as the container engine.")
	}
	return item(id, depsetup.StateMissing, "", "No container engine is installed.")
}

func desktopItem(sys Sys, id string) *agentv1.DepsItem {
	e := DetectEngine(sys)
	switch {
	case e.Usable && e.ContainerOS == "windows":
		return item(id, depsetup.StateWrongKind, e.Version, "The engine runs Windows containers; Linux containers are needed.")
	case e.Usable:
		return item(id, depsetup.StateOK, e.Version, "")
	case e.Kind != EngineNone:
		return item(id, depsetup.StateStopped, "", "Docker is installed but not running.")
	}
	return item(id, depsetup.StateMissing, "", "No container engine is installed.")
}

func windowsItems(sys Sys) []*agentv1.DepsItem {
	var out []*agentv1.DepsItem
	w := detectWSL2(sys)
	switch {
	case w.Usable:
		out = append(out, item(depsetup.ItemWSL2, depsetup.StateOK, w.Version, ""))
	case w.Present:
		out = append(out, item(depsetup.ItemWSL2, depsetup.StateMissing, "", "WSL is installed but not ready."))
	default:
		out = append(out, item(depsetup.ItemWSL2, depsetup.StateMissing, "", "WSL2 is not installed."))
	}
	e := DetectEngine(sys)
	eng := depsEngine(sys)
	switch {
	case eng.Usable && eng.ContainerOS == "windows":
		out = append(out, item(depsetup.ItemDockerDesktop, depsetup.StateWrongKind, eng.Version, "The engine runs Windows containers; Linux containers are needed."))
	case eng.Usable:
		id := depsetup.ItemDockerWSL2
		if e.Kind == EngineDesktop {
			id = depsetup.ItemDockerDesktop
		}
		out = append(out, item(id, depsetup.StateOK, eng.Version, ""))
	case e.Kind == EngineWSLViaCLI:
		id := depsetup.ItemDockerWSL2
		if sys.Exists(dockerDesktopExe) {
			id = depsetup.ItemDockerDesktop
		}
		out = append(out, item(id, depsetup.StateStopped, "", "Docker is installed but not running."))
	default:
		out = append(out, item(depsetup.ItemDockerWSL2, depsetup.StateMissing, "", "No container engine is installed."))
	}
	return out
}

// depsItemState reads the state of one item now, for the installer's skip check.
func depsItemState(sys Sys, runnerOS, runnerKind, id string) string {
	for _, it := range DetectDeps(sys, runnerOS, runnerKind).GetItems() {
		if it.GetId() == id {
			return it.GetState()
		}
	}
	// An item the runner choice does not list: judge by the engine alone.
	switch id {
	case depsetup.ItemWSL2:
		if detectWSL2(sys).Usable {
			return depsetup.StateOK
		}
	default:
		if DetectEngine(sys).Usable {
			return depsetup.StateOK
		}
	}
	return depsetup.StateMissing
}
