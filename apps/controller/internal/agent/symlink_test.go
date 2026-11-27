package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	agentv1 "github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/gen/agent/v1"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/load"
)

type symSys struct {
	OSSys
	goos string
	reg  load.Result
	ok   bool
}

func (s symSys) GOOS() string                     { return s.goos }
func (s symSys) Run([]string) (load.Result, bool) { return s.reg, s.ok }
func (symSys) Hostname() (string, error)          { return "h", nil }
func (symSys) Disk(string) (int64, int64, error)  { return 0, 0, nil }
func (symSys) ReadWritable(string) bool           { return false }
func (symSys) Arch() string                       { return "x64" }

const devOn = "\r\nHKEY_LOCAL_MACHINE\\SOFTWARE\\Microsoft\\Windows\\CurrentVersion\\AppModelUnlock\r\n    AllowDevelopmentWithoutDevLicense    REG_DWORD    0x1\r\n"

func TestProbeSymlinkNonWindowsAlwaysYes(t *testing.T) {
	for _, goos := range []string{"linux", "darwin"} {
		r := ProbeSymlink(context.Background(), symSys{goos: goos})
		if !r.Known || !r.CanLink || r.Error != "" {
			t.Fatalf("%s: %+v", goos, r)
		}
	}
}

func TestProbeSymlinkWindows(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	t.Setenv("TMP", os.Getenv("TMPDIR"))
	t.Setenv("TEMP", os.Getenv("TMPDIR"))
	old := makeSymlink
	t.Cleanup(func() { makeSymlink = old })
	cases := []struct {
		name    string
		refuse  bool
		reg     load.Result
		ok      bool
		can, dm bool
	}{
		{"link works, dev mode off", false, load.Result{Code: 1}, true, true, false},
		{"link works, dev mode on", false, load.Result{Out: devOn}, true, true, true},
		{"link refused", true, load.Result{Code: 1}, true, false, false},
		{"reg missing", false, load.Result{}, false, true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// The probe's logic is tested with a fake link: the real one needs a
			// privilege or Developer Mode that a normal developer PC may lack.
			makeSymlink = func(string, string) error { return nil }
			if c.refuse {
				makeSymlink = func(string, string) error { return errors.New("A required privilege is not held by the client") }
			}
			r := ProbeSymlink(context.Background(), symSys{goos: "windows", reg: c.reg, ok: c.ok})
			if !r.Known || r.CanLink != c.can || r.DeveloperMode != c.dm || r.Error != "" {
				t.Fatalf("%+v", r)
			}
		})
	}
}

func TestProbeSymlinkLeavesNoFolderBehind(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	t.Setenv("TMP", tmp)
	t.Setenv("TEMP", tmp)
	ProbeSymlink(context.Background(), symSys{goos: "windows", reg: load.Result{Code: 1}, ok: true})
	left, err := filepath.Glob(filepath.Join(tmp, "crsp-symlink-*"))
	if err != nil || len(left) != 0 {
		t.Fatalf("left %v %v", left, err)
	}
}

func TestMachineReportCarriesSymlink(t *testing.T) {
	fake := func(context.Context, Sys) *agentv1.SymlinkReport {
		return &agentv1.SymlinkReport{Known: true, CanLink: true, DeveloperMode: true}
	}
	rep := buildMachineReportWith(context.Background(), OSSys{}, &agentv1.Facts{}, nil, nil, fake, time.Unix(1, 0))
	if s := rep.GetSymlink(); !s.GetKnown() || !s.GetCanLink() || !s.GetDeveloperMode() {
		t.Fatalf("%+v", s)
	}
	if buildMachineReportWith(context.Background(), OSSys{}, &agentv1.Facts{}, nil, nil, nil, time.Unix(1, 0)).GetSymlink() != nil {
		t.Fatal("no prober must report none")
	}
	if buildMachineReport(context.Background(), OSSys{}, &agentv1.Facts{}, nil, nil, time.Unix(1, 0)).GetSymlink() == nil {
		t.Fatal("default must probe")
	}
}
