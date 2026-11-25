package depsetup

import (
	"strings"
	"testing"

	agentv1 "github.com/luis-mc/ci-runners-shared-pool/internal/gen/agent/v1"
)

func testList() List {
	return List{Release: "t", Items: []Pinned{
		{ID: ItemWSL2, Name: "WSL2", Version: "inbox", OS: "windows", Source: "learn.microsoft.com", DiskBytes: 300 << 20, DownloadBytes: 100 << 20, TypicalMinutes: 5, NeedsAdmin: true, RestartLikely: true, Undo: "undo wsl", ManualSteps: []string{"wsl --install --no-distribution"}},
		{ID: ItemDockerWSL2, Name: "Docker Engine in WSL2", Version: "apt", OS: "windows", Source: "download.docker.com", DiskBytes: 1 << 30, TypicalMinutes: 8, Undo: "undo dw", ManualSteps: []string{"step dw"}},
		{ID: ItemDockerDesktop, Name: "Docker Desktop", Version: "4", OS: "windows", Source: "desktop.docker.com", DiskBytes: 3 << 30, NeedsAdmin: true, RestartLikely: true, LicenceNote: "licence", Undo: "undo dd", ManualSteps: []string{"step dd"}},
		{ID: ItemDockerEngine, Name: "Docker Engine", Version: "apt", OS: "linux", Source: "download.docker.com", DiskBytes: 900 << 20, NeedsAdmin: true, Undo: "undo de", ManualSteps: []string{"sudo apt-get install docker-ce"}},
		{ID: ItemDockerDesktop, Name: "Docker Desktop", Version: "4", OS: "darwin", Source: "desktop.docker.com", DiskBytes: 3 << 30, NeedsAdmin: true, LicenceNote: "licence", Undo: "undo mac", ManualSteps: []string{"step mac"}},
	}}
}

func rep(os string, admin bool, items ...string) *agentv1.DepsReport {
	r := &agentv1.DepsReport{Os: os, Admin: admin, DiskFreeBytes: 100 << 30}
	for i := 0; i+1 < len(items); i += 2 {
		r.Items = append(r.Items, &agentv1.DepsItem{Id: items[i], State: items[i+1]})
	}
	return r
}

func TestOfferWindowsEverythingMissing(t *testing.T) {
	c := BuildOffer(rep("windows", true), RunnerLinux, "persistent", testList())
	if c.Ready || c.StartOnly || c.BlockedReason != "" {
		t.Fatalf("%+v", c)
	}
	if len(c.Items) != 2 || c.Items[0].ID != ItemWSL2 || c.Items[1].ID != ItemDockerWSL2 || !strings.Contains(c.Items[1].Why, "containers need Docker") {
		t.Fatalf("items %+v", c.Items)
	}
	if len(c.Choices) != 2 {
		t.Fatalf("choices %+v", c.Choices)
	}
	a, b := c.Choices[0], c.Choices[1]
	if !a.Recommended || b.Recommended || a.ID != ItemDockerWSL2 || b.ID != ItemDockerDesktop || a.RequiresLicenceTick || !b.RequiresLicenceTick {
		t.Fatalf("a=%+v b=%+v", a, b)
	}
	if strings.Join(a.ItemIDs, ",") != "wsl2,docker-in-wsl2" || len(a.Facts) != 2 || a.Facts[0].Source != "learn.microsoft.com" || !a.Facts[0].NeedsAdmin || !a.Facts[0].RestartLikely || a.Facts[1].Undo != "undo dw" {
		t.Fatalf("facts %+v", a)
	}
	if len(a.ManualSteps) != 2 || b.Facts[1].LicenceNote != "licence" {
		t.Fatalf("manual %+v", a.ManualSteps)
	}
}

func TestOfferWindowsWSLPresent(t *testing.T) {
	c := BuildOffer(rep("windows", false, ItemWSL2, StateOK), RunnerLinux, "ephemeral", testList())
	// Docker in WSL2 needs no admin once WSL2 is there; Docker Desktop does.
	if len(c.Choices) != 2 || strings.Join(c.Choices[0].ItemIDs, ",") != "docker-in-wsl2" || c.ChoiceBlock(ItemDockerWSL2) != "" || c.ChoiceBlock(ItemDockerDesktop) == "" || c.BlockedReason != "" {
		t.Fatalf("%+v", c)
	}
}

func TestOfferReadyStoppedAndNoNeed(t *testing.T) {
	l := testList()
	if c := BuildOffer(rep("windows", true, ItemDockerWSL2, StateOK), RunnerLinux, "persistent", l); !c.Ready || len(c.Choices) != 0 {
		t.Fatalf("engine ok: %+v", c)
	}
	if c := BuildOffer(rep("windows", true, ItemDockerDesktop, StateOK), RunnerLinux, "persistent", l); !c.Ready {
		t.Fatalf("desktop ok without wsl item: %+v", c)
	}
	c := BuildOffer(rep("windows", true, ItemWSL2, StateOK, ItemDockerWSL2, StateStopped), RunnerLinux, "persistent", l)
	if c.Ready || !c.StartOnly || len(c.Choices) != 0 {
		t.Fatalf("stopped: %+v", c)
	}
	// DEP16: no install when the runner choice needs nothing.
	for _, tc := range []struct{ mos, ros string }{{"windows", RunnerWindows}, {"darwin", RunnerMacOS}} {
		if c := BuildOffer(rep(tc.mos, true), tc.ros, "persistent", l); !c.Ready || len(c.Choices) != 0 || len(c.Items) != 0 {
			t.Fatalf("%s runner on %s: %+v", tc.ros, tc.mos, c)
		}
	}
}

func TestOfferLinuxAndMac(t *testing.T) {
	l := testList()
	c := BuildOffer(rep("linux", true), RunnerLinux, "ephemeral", l)
	if len(c.Choices) != 1 || c.Choices[0].ID != ItemDockerEngine || !c.Choices[0].Recommended || c.Choices[0].RequiresLicenceTick {
		t.Fatalf("linux %+v", c)
	}
	m := BuildOffer(rep("darwin", true), RunnerLinux, "ephemeral", l)
	if len(m.Choices) != 1 || m.Choices[0].ID != ItemDockerDesktop || !m.Choices[0].Recommended || !m.Choices[0].RequiresLicenceTick {
		t.Fatalf("mac %+v", m)
	}
}

func TestOfferBlocked(t *testing.T) {
	l := testList()
	c := BuildOffer(rep("linux", false), RunnerLinux, "persistent", l)
	if !strings.Contains(c.BlockedReason, "administrator") || len(c.Choices) != 1 || len(c.Choices[0].ManualSteps) == 0 {
		t.Fatalf("admin: %+v", c)
	}
	r := rep("linux", true)
	r.DiskFreeBytes = 1 << 30 // needs 900 MiB + 1 GiB margin
	if c := BuildOffer(r, RunnerLinux, "persistent", l); !strings.Contains(c.BlockedReason, "disk") {
		t.Fatalf("disk: %+v", c)
	}
	v := rep("windows", true)
	v.VirtualizationOff = true
	if c := BuildOffer(v, RunnerLinux, "persistent", l); !strings.Contains(c.BlockedReason, "Virtualization") {
		t.Fatalf("virt: %+v", c)
	}
}
