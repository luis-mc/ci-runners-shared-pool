package install_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/agent/install"
	"github.com/luis-mc/ci-runners-shared-pool/internal/depsetup"
	agentv1 "github.com/luis-mc/ci-runners-shared-pool/internal/gen/agent/v1"
)

const fedora = "ID=fedora\nVERSION_ID=41\n"
const rocky = "ID=\"rocky\"\nID_LIKE=\"rhel centos fedora\"\n"

// build is a Runner with the parts a test of step one changes.
func build(sys *fakeSys, osrel string, states map[string]string, mod func(*install.Config)) (*install.Runner, *rec) {
	rc := &rec{}
	tick := time.Unix(1000, 0)
	c := install.Config{
		Sys: sys, Pinned: depsetup.MustLoad(), Emit: rc.emit,
		State: func(_ context.Context, id string) string {
			if s, ok := states[id]; ok {
				return s
			}
			return depsetup.StateMissing
		},
		Engine:    func(context.Context) install.EngineInfo { return install.EngineInfo{Usable: true, Version: "27.0.1", ContainerOS: "linux"} },
		OSRelease: func() string { return osrel },
		Now:       func() time.Time { tick = tick.Add(time.Second); return tick },
		Wait:      func(context.Context, time.Duration) {},
	}
	if mod != nil {
		mod(&c)
	}
	return install.New(c), rc
}

// DEP2 and DEP3 on Linux: Fedora and the Red Hat family get Docker's own rpm repository.
func TestDockerEngineOnFedoraUsesDnfAndTheRepoFile(t *testing.T) {
	for _, tc := range []struct{ name, osrel, family string }{{"fedora", fedora, "fedora"}, {"rocky", rocky, "centos"}} {
		t.Run(tc.name, func(t *testing.T) {
			sys := newFake("linux")
			sys.runFn = func(argv []string, line func(string, string)) int {
				if argv[0] == "systemctl" && argv[1] == "is-enabled" {
					return 1
				}
				return 0
			}
			r, rc := build(sys, tc.osrel, nil, nil)
			r.Install(context.Background(), req("docker-engine"))
			f := rc.last()
			if f.GetOutcome() != "done" {
				t.Fatalf("final = %v", f)
			}
			want := "https://download.docker.com/linux/" + tc.family + "/docker-ce.repo"
			if len(sys.dls) != 1 || sys.dls[0] != want {
				t.Errorf("downloads = %v, want %s", sys.dls, want)
			}
			if !sys.ran("dnf", "-y", "install", "docker-ce") || sys.ran("apt-get") {
				t.Errorf("runs = %v", sys.runs)
			}
			kinds := map[string]bool{}
			for _, a := range f.GetAdded() {
				kinds[a.GetKind()] = true
			}
			if !kinds["rpm-repo-file"] || !kinds["rpm-package"] || !kinds["systemd-enable"] {
				t.Errorf("undo record = %v", f.GetAdded())
			}
		})
	}
}

// A conflicting unofficial package is named and not removed without a yes.
func TestDockerEngineOnFedoraRefusesConflictsWithoutYes(t *testing.T) {
	sys := newFake("linux")
	sys.runFn = func(argv []string, line func(string, string)) int {
		if argv[0] == "rpm" {
			line("stdout", "podman-docker installed")
		}
		return 0
	}
	r, rc := build(sys, fedora, nil, nil)
	r.Install(context.Background(), req("docker-engine"))
	f := rc.last()
	if f.GetOutcome() != "failed" || !strings.Contains(f.GetError(), "podman-docker") || sys.ran("dnf") || len(sys.dls) != 0 {
		t.Fatalf("final = %v runs = %v", f, sys.runs)
	}
}

func TestOtherLinuxFamilyGetsTheManualSteps(t *testing.T) {
	sys := newFake("linux")
	r, rc := build(sys, "ID=arch\n", nil, nil)
	r.Install(context.Background(), req("docker-engine"))
	f := rc.last()
	if f.GetOutcome() != "failed" || !strings.Contains(f.GetError(), "Do it myself") || len(sys.runs) != 0 {
		t.Fatalf("final = %v runs = %v", f, sys.runs)
	}
}

func TestUndoRemovesOnlyTheRecordedDnfPackages(t *testing.T) {
	sys := newFake("linux")
	sys.files[install.RPMRepoPath] = "x"
	r, rc := build(sys, fedora, nil, nil)
	r.Undo(context.Background(), &agentv1.DepsUndo{JobId: "j2", Added: []*agentv1.DepsAdded{
		{ItemId: "docker-engine", Kind: "rpm-package", Ref: "docker-ce"},
		{ItemId: "docker-engine", Kind: "rpm-repo-file", Ref: install.RPMRepoPath},
		{ItemId: "docker-engine", Kind: "rpm-package", Ref: "evil; rm -rf /"},
	}})
	if !sys.ran("dnf", "-y", "remove", "docker-ce") || sys.ran("dnf", "-y", "remove", "docker-ce", "evil; rm -rf /") {
		t.Errorf("runs = %v", sys.runs)
	}
	if rc.last().GetOutcome() != "failed" || len(rc.last().GetAdded()) != 1 {
		t.Errorf("final = %v", rc.last())
	}
	if len(sys.removed) != 1 || sys.removed[0] != install.RPMRepoPath {
		t.Errorf("removed = %v", sys.removed)
	}
}

// DEP2, DEP3 on macOS: Homebrew's cask, a person approves one prompt, crsp waits.
func TestMacInstallsThroughHomebrewAndWaitsForTheApp(t *testing.T) {
	sys := newFake("darwin")
	sys.files["/opt/homebrew/bin/brew"] = "x"
	calls, waits := 0, 0
	r, rc := build(sys, "", nil, func(c *install.Config) {
		c.Engine = func(context.Context) install.EngineInfo {
			calls++
			if calls < 3 {
				return install.EngineInfo{Reason: "the engine does not answer"}
			}
			return install.EngineInfo{Usable: true, Version: "27.0.1", ContainerOS: "linux"}
		}
		c.Wait = func(context.Context, time.Duration) { waits++ }
	})
	rq := req("docker-desktop")
	rq.AcceptLicence = true
	r.Install(context.Background(), rq)
	f := rc.last()
	if f.GetOutcome() != "done" || waits != 2 {
		t.Fatalf("final = %v waits = %d", f, waits)
	}
	if !sys.ran("/opt/homebrew/bin/brew", "install", "--cask", "docker-desktop") || !sys.ran("open", "-a", "Docker") {
		t.Errorf("runs = %v", sys.runs)
	}
	if len(f.GetAdded()) != 1 || f.GetAdded()[0].GetKind() != "brew-cask" {
		t.Errorf("undo record = %v", f.GetAdded())
	}
	if !strings.Contains(rc.all(), "approve it") {
		t.Error("the person is not told to approve the prompt")
	}
}

func TestMacWithoutHomebrewOrLicenceInstallsNothing(t *testing.T) {
	sys := newFake("darwin")
	r, rc := build(sys, "", nil, nil)
	r.Install(context.Background(), req("docker-desktop"))
	if rc.last().GetOutcome() != "failed" || !strings.Contains(rc.last().GetError(), "right to use") || len(sys.runs) != 0 {
		t.Fatalf("no licence: %v", rc.last())
	}
	sys2 := newFake("darwin")
	r2, rc2 := build(sys2, "", nil, nil)
	rq := req("docker-desktop")
	rq.AcceptLicence = true
	r2.Install(context.Background(), rq)
	if rc2.last().GetOutcome() != "failed" || !strings.Contains(rc2.last().GetError(), "Homebrew") || len(sys2.runs) != 0 {
		t.Fatalf("no brew: %v", rc2.last())
	}
}

func TestMacEngineNeverAnswersSaysApproveAndTryAgain(t *testing.T) {
	sys := newFake("darwin")
	sys.files["/usr/local/bin/brew"] = "x"
	r, rc := build(sys, "", nil, func(c *install.Config) {
		c.Engine = func(context.Context) install.EngineInfo { return install.EngineInfo{Reason: "no answer"} }
	})
	rq := req("docker-desktop")
	rq.AcceptLicence = true
	r.Install(context.Background(), rq)
	f := rc.last()
	if f.GetOutcome() != "failed" || !strings.Contains(f.GetError(), "permission") {
		t.Fatalf("final = %v", f)
	}
}

// A stopped Docker Desktop on a Mac is opened, not installed (DEP16), and needs no licence tick.
func TestMacStoppedEngineIsOnlyOpened(t *testing.T) {
	sys := newFake("darwin")
	r, rc := build(sys, "", map[string]string{"docker-desktop": depsetup.StateStopped}, nil)
	r.Install(context.Background(), req("docker-desktop"))
	if rc.last().GetOutcome() != "done" || !sys.ran("open", "-a", "Docker") || sys.ran("/opt/homebrew/bin/brew") {
		t.Fatalf("final = %v runs = %v", rc.last(), sys.runs)
	}
}

// DEP9: prepare runs after verify, once; its failure fails the job with words.
func TestPrepareRunsAfterVerify(t *testing.T) {
	sys := newFake("linux")
	var order []string
	r, rc := build(sys, ubuntu, map[string]string{"docker-engine": depsetup.StateOK}, func(c *install.Config) {
		c.Prepare = func(context.Context) error { order = append(order, "prepare"); return nil }
	})
	sys.runFn = func(argv []string, line func(string, string)) int {
		order = append(order, argv[0])
		return 0
	}
	r.Install(context.Background(), req("docker-engine"))
	if rc.last().GetOutcome() != "done" || strings.Join(order, ",") != "docker,prepare" {
		t.Fatalf("final = %v order = %v", rc.last(), order)
	}
	if !strings.Contains(rc.all(), "Making the runner image") {
		t.Error("the step is not named")
	}
}

func TestPrepareFailureFailsTheJob(t *testing.T) {
	sys := newFake("linux")
	r, rc := build(sys, ubuntu, map[string]string{"docker-engine": depsetup.StateOK}, func(c *install.Config) {
		c.Prepare = func(context.Context) error { return errors.New("the registry did not answer") }
	})
	r.Install(context.Background(), req("docker-engine"))
	f := rc.last()
	if f.GetOutcome() != "failed" || f.GetPhase() != "prepare" || !strings.Contains(f.GetError(), "registry did not answer") {
		t.Fatalf("final = %v", f)
	}
}

// DEP16: a machine that already meets the need, or a runner that needs nothing, installs nothing.
func TestNoInstallWhenNothingIsNeeded(t *testing.T) {
	sys := newFake("linux")
	r, rc := build(sys, ubuntu, map[string]string{"docker-engine": depsetup.StateOK}, nil)
	r.Install(context.Background(), req("docker-engine"))
	if rc.last().GetOutcome() != "done" || sys.ran("apt-get") || sys.ran("dnf") || len(sys.dls) != 0 || len(rc.last().GetAdded()) != 0 {
		t.Fatalf("final = %v runs = %v", rc.last(), sys.runs)
	}
	// an item the pinned list does not know for this OS is refused, never guessed
	sys2 := newFake("darwin")
	r2, rc2 := build(sys2, "", nil, nil)
	r2.Install(context.Background(), req("docker-engine"))
	if rc2.last().GetOutcome() != "failed" || len(sys2.runs) != 0 {
		t.Fatalf("unknown item: %v", rc2.last())
	}
}
