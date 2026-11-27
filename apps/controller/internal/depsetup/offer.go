package depsetup

import (
	"fmt"
	"strings"

	agentv1 "github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/gen/agent/v1"
)

// Check is the answer of a dependency check: what the runner choice needs,
// what the machine has, and the offer (DEP1 to DEP3, DEP16). It is the shape of
// the API's DependencyCheck.
type Check struct {
	MachineID         string
	Ready             bool
	OS                string
	Arch              string
	Admin             bool
	DiskFreeBytes     int64
	VirtualizationOff bool
	Items             []Item
	Choices           []Choice
	BlockedReason     string
	StartOnly         bool

	// blocks holds, per choice id, why that choice cannot be installed.
	blocks map[string]string
}

// ChoiceBlock says why one choice cannot be installed now; empty when it can.
func (c Check) ChoiceBlock(id string) string { return c.blocks[id] }

// ChoiceByID returns an offered choice.
func (c Check) ChoiceByID(id string) (Choice, bool) {
	for _, ch := range c.Choices {
		if ch.ID == id {
			return ch, true
		}
	}
	return Choice{}, false
}

// Item is one needed dependency and its state.
type Item struct {
	ID, Name, State, Version, Why, Detail string
}

// Facts are the facts of one item a choice installs (DEP3).
type Facts struct {
	Name, Version, Source     string
	DownloadBytes, DiskBytes  int64
	TypicalMinutes            int
	NeedsAdmin, RestartLikely bool
	LicenceNote, Undo         string
}

// Choice is one way to fix what is missing.
type Choice struct {
	ID                  string
	Title               string
	ItemIDs             []string
	Recommended         bool
	Facts               []Facts
	RequiresLicenceTick bool
	ManualSteps         []string
}

// DiskMargin is the free space kept beyond the sum of the items' disk use.
const DiskMargin int64 = 1 << 30

// Runner OS and kind values of a request.
const (
	RunnerLinux   = "linux"
	RunnerWindows = "windows"
	RunnerMacOS   = "macos"
)

// needs says what a runner OS needs on a machine OS (spec 3.3): whether WSL2,
// and which engine items can serve (first is preferred).
func needs(machineOS, runnerOS string) (wsl bool, engines []string) {
	if runnerOS != RunnerLinux {
		return false, nil // Windows runner on Windows, macOS runner on macOS: nothing beyond crsp
	}
	switch machineOS {
	case "windows":
		return true, []string{ItemDockerWSL2, ItemDockerDesktop}
	case "linux":
		return false, []string{ItemDockerEngine}
	case "darwin":
		return false, []string{ItemDockerDesktop}
	}
	return false, nil
}

func why(id, machineOS string) string {
	switch id {
	case ItemWSL2:
		return "Linux runners on Windows run in a container, and containers need WSL2, the part of Windows that runs Linux."
	case ItemDockerWSL2, ItemDockerEngine:
		if machineOS == "windows" {
			return "Linux runners on Windows run in a container, and containers need Docker."
		}
		return "Linux runners run in a container, and containers need Docker."
	case ItemDockerDesktop:
		if machineOS == "darwin" {
			return "Linux runners on a Mac run in a container inside a Linux VM, and that needs Docker Desktop."
		}
		return "Linux runners on Windows run in a container, and containers need Docker."
	}
	return "A runner of this kind needs it."
}

func gib(b int64) string { return fmt.Sprintf("%.1f GiB", float64(b)/float64(1<<30)) }

// BuildOffer turns the agent's report into the check and the offer. Nothing is
// offered when no runner choice needs anything or every need is met (DEP16).
func BuildOffer(rep *agentv1.DepsReport, runnerOS, runnerKind string, list List) Check {
	_ = runnerKind // the need depends on the runner OS; the kind is kept for the job record
	goos := rep.GetOs()
	out := Check{OS: goos, Arch: rep.GetArch(), Admin: rep.GetAdmin(), DiskFreeBytes: rep.GetDiskFreeBytes(),
		VirtualizationOff: rep.GetVirtualizationOff(), Items: []Item{}, Choices: []Choice{}, blocks: map[string]string{}}
	needWSL, engines := needs(goos, runnerOS)
	if len(engines) == 0 {
		out.Ready = true
		return out
	}
	state := func(id string) (string, string, string) {
		for _, it := range rep.GetItems() {
			if it.GetId() == id {
				return it.GetState(), it.GetVersion(), it.GetDetail()
			}
		}
		return StateMissing, "", ""
	}
	name := func(id string) string {
		if p, ok := list.Find(id, goos); ok {
			return p.Name
		}
		return id
	}
	mkItem := func(id string) Item {
		st, ver, det := state(id)
		return Item{ID: id, Name: name(id), State: st, Version: ver, Why: why(id, goos), Detail: det}
	}
	// The engine item shown: one that answers, else a stopped one, else a wrong kind, else the preferred one.
	engine := mkItem(engines[0])
	for _, want := range []string{StateOK, StateStopped, StateWrongKind} {
		found := false
		for _, id := range engines {
			if it := mkItem(id); it.State == want {
				engine, found = it, true
				break
			}
		}
		if found {
			break
		}
	}
	wslItem := Item{}
	if needWSL {
		wslItem = mkItem(ItemWSL2)
	}
	if engine.State == StateOK {
		out.Items = append(out.Items, engine)
		out.Ready = true
		return out
	}
	if needWSL {
		out.Items = append(out.Items, wslItem)
	}
	out.Items = append(out.Items, engine)
	wslOK := !needWSL || wslItem.State == StateOK
	if engine.State == StateStopped && wslOK {
		out.StartOnly = true
		return out
	}

	// Choices: only what is missing is installed (DEP16); an engine the user installed is never replaced
	// except a wrong-kind one, for which another engine is offered, never an upgrade.
	var ids [][]string
	var titles []string
	var licence []bool
	var id []string
	switch goos {
	case "windows":
		pre := []string{}
		if !wslOK {
			pre = []string{ItemWSL2}
		}
		ids = [][]string{append(append([]string{}, pre...), ItemDockerWSL2), append(append([]string{}, pre...), ItemDockerDesktop)}
		id = []string{ItemDockerWSL2, ItemDockerDesktop}
		titles = []string{"Install Docker in WSL2", "Install Docker Desktop"}
		if !wslOK {
			titles = []string{"Install WSL2 and Docker in WSL2", "Install WSL2 and Docker Desktop"}
		}
		licence = []bool{false, true}
	case "linux":
		ids, id, titles, licence = [][]string{{ItemDockerEngine}}, []string{ItemDockerEngine}, []string{"Install Docker Engine"}, []bool{false}
	default:
		ids, id, titles, licence = [][]string{{ItemDockerDesktop}}, []string{ItemDockerDesktop}, []string{"Install Docker Desktop"}, []bool{true}
	}
	for i, set := range ids {
		ch := Choice{ID: id[i], Title: titles[i], ItemIDs: set, RequiresLicenceTick: licence[i], ManualSteps: []string{}, Facts: []Facts{}}
		var disk int64
		admin := false
		for _, iid := range set {
			p, ok := list.Find(iid, goos)
			if !ok {
				continue
			}
			ch.Facts = append(ch.Facts, Facts{Name: p.Name, Version: p.Version, Source: p.Source, DownloadBytes: p.DownloadBytes,
				DiskBytes: p.DiskBytes, TypicalMinutes: p.TypicalMinutes, NeedsAdmin: p.NeedsAdmin, RestartLikely: p.RestartLikely,
				LicenceNote: p.LicenceNote, Undo: p.Undo})
			ch.ManualSteps = append(ch.ManualSteps, p.ManualSteps...)
			disk += p.DiskBytes
			admin = admin || p.NeedsAdmin
		}
		if len(ch.Facts) != len(set) {
			continue // an item the pinned list does not know for this OS: not offered
		}
		switch need := disk + DiskMargin; {
		case !out.Admin && admin:
			out.blocks[ch.ID] = "This install needs an administrator, and crsp does not have those rights on this machine. Use the steps to do it yourself."
		case goos == "windows" && out.VirtualizationOff && !wslOK:
			out.blocks[ch.ID] = "Virtualization is off in the firmware (BIOS or UEFI). Turn it on there; crsp cannot change it."
		case out.DiskFreeBytes > 0 && out.DiskFreeBytes < need:
			out.blocks[ch.ID] = fmt.Sprintf("Not enough free disk space: this needs about %s and the machine has %s free.", gib(need), gib(out.DiskFreeBytes))
		}
		out.Choices = append(out.Choices, ch)
	}
	if len(out.Choices) > 0 {
		out.Choices[0].Recommended = true
		all := true
		for _, ch := range out.Choices {
			if out.blocks[ch.ID] == "" {
				all = false
			}
		}
		if all {
			out.BlockedReason = out.blocks[out.Choices[0].ID]
		}
	}
	return out
}

// ItemsOf lists the ids of an offer's items, for messages.
func ItemsOf(c Choice) string { return strings.Join(c.ItemIDs, ", ") }
