package install_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/agent/install"
	agentv1 "github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/gen/agent/v1"
)

// controllerRunner is a Runner on Windows whose FromController is the given func.
func controllerRunner(sys *fakeSys, sha string, from func(ctx context.Context, name, dest string, on func(b, t int64)) error) (*install.Runner, *rec) {
	rc := &rec{}
	tick := time.Unix(1000, 0)
	r := install.New(install.Config{
		Sys: sys, Pinned: desktopList(sha), Emit: rc.emit,
		State: func(context.Context, string) string { return "missing" },
		Engine: func(context.Context) install.EngineInfo {
			return install.EngineInfo{Usable: true, Version: "27.0.1", ContainerOS: "linux"}
		},
		OSRelease:      func() string { return ubuntu },
		Now:            func() time.Time { tick = tick.Add(time.Second); return tick },
		FromController: from,
	})
	return r, rc
}

func desktopReq() *agentv1.DepsInstall {
	rq := req("docker-desktop")
	rq.AcceptLicence = true
	return rq
}

// DEP15: the controller's copy is used first and the vendor is never asked.
func TestControllerCopyIsUsedFirst(t *testing.T) {
	sys := newFake("windows")
	var asked string
	r, rc := controllerRunner(sys, "good", func(_ context.Context, name, dest string, on func(b, t int64)) error {
		asked = name
		sys.mu.Lock()
		sys.files[dest] = "good"
		sys.mu.Unlock()
		on(10, 10)
		return nil
	})
	r.Install(context.Background(), desktopReq())
	if asked != "docker-desktop-windows" {
		t.Fatalf("asked the controller for %q", asked)
	}
	if len(sys.dls) != 0 {
		t.Fatalf("the vendor was asked too: %v", sys.dls)
	}
	if len(sys.runs) == 0 || sys.runs[0][1] != "install" {
		t.Fatalf("the installer did not run: %v", sys.runs)
	}
	if !strings.Contains(rc.all(), "from the crsp controller") {
		t.Errorf("the log does not say where the file came from:\n%s", rc.all())
	}
}

// DEP15: no copy at the controller, so the machine goes to the vendor.
func TestNoControllerCopyFallsBackToVendor(t *testing.T) {
	sys := newFake("windows")
	sys.dlHash, sys.dlTotal = "good", 100
	r, _ := controllerRunner(sys, "good", func(context.Context, string, string, func(b, t int64)) error {
		return errors.New("the server answered 404")
	})
	r.Install(context.Background(), desktopReq())
	if len(sys.dls) != 1 || !strings.HasPrefix(sys.dls[0], "https://desktop.docker.com/") {
		t.Fatalf("vendor downloads = %v", sys.dls)
	}
	if len(sys.runs) == 0 {
		t.Fatal("the installer did not run")
	}
}

// DEP5 + DEP15: a controller copy with a wrong hash is thrown away and never run.
func TestWrongControllerCopyIsNeverRun(t *testing.T) {
	sys := newFake("windows")
	sys.dlHash, sys.dlTotal = "good", 100
	r, _ := controllerRunner(sys, "good", func(_ context.Context, _, dest string, _ func(b, t int64)) error {
		sys.mu.Lock()
		sys.files[dest] = "evil"
		sys.mu.Unlock()
		return nil
	})
	r.Install(context.Background(), desktopReq())
	if len(sys.dls) != 1 {
		t.Fatalf("expected the vendor as the fallback, got %v", sys.dls)
	}
	if len(sys.removed) == 0 {
		t.Error("the controller's bad file was not removed")
	}
	for _, c := range sys.runs {
		if strings.Contains(strings.Join(c, " "), "evil") {
			t.Fatalf("ran %v", c)
		}
	}
}

// DEP15: no internet and no controller copy says what to do in the console.
func TestOfflineMachineWithoutCopyPointsToTheConsole(t *testing.T) {
	sys := newFake("windows")
	rc := &rec{}
	// the vendor is unreachable
	sys2 := &offlineSys{fakeSys: sys}
	r2 := install.New(install.Config{
		Sys: sys2, Pinned: desktopList("good"), Emit: rc.emit,
		State:     func(context.Context, string) string { return "missing" },
		Engine:    func(context.Context) install.EngineInfo { return install.EngineInfo{Usable: true} },
		OSRelease: func() string { return ubuntu },
		FromController: func(context.Context, string, string, func(b, t int64)) error {
			return errors.New("the server answered 404")
		},
	})
	r2.Install(context.Background(), desktopReq())
	f := rc.last()
	if f.GetOutcome() != "failed" || !strings.Contains(f.GetError(), "Dependencies") {
		t.Fatalf("final = %v", f)
	}
	if len(sys.runs) != 0 {
		t.Fatalf("a command ran: %v", sys.runs)
	}
}

type offlineSys struct{ *fakeSys }

func (offlineSys) Download(context.Context, string, string, func(b, t int64)) error {
	return errors.New("no route to host")
}
