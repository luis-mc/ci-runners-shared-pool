package cpucap_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/cpucap"
)

func TestCPUMax(t *testing.T) {
	cases := []struct {
		pct, ncpu int
		want      string
	}{
		{100, 8, "max 100000"},
		{50, 8, "400000 100000"},
		{25, 4, "100000 100000"},
		{75, 2, "150000 100000"},
		{25, 1, "25000 100000"},
		{50, 0, "max 100000"},
	}
	for _, c := range cases {
		if got := cpucap.CPUMax(c.pct, c.ncpu); got != c.want {
			t.Errorf("CPUMax(%d, %d) = %q, want %q", c.pct, c.ncpu, got, c.want)
		}
	}
}

func fakeCgroupRoot(t *testing.T, services ...string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "cgroup.controllers"), []byte("cpu memory\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, s := range services {
		if err := os.MkdirAll(filepath.Join(root, "system.slice", s), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// R5.64: Linux sets cpu.max in the cgroup of each runner service, and only there.
func TestApplyCgroupsWritesCPUMaxOfEachRunnerService(t *testing.T) {
	root := fakeCgroupRoot(t, "actions.runner.acme.one.service", "actions.runner.acme.two.service", "ssh.service")
	res := cpucap.ApplyCgroups(root, 8, 50)
	if res.State != cpucap.Applied || res.Targets != 2 {
		t.Fatalf("result = %+v, want applied on 2 services", res)
	}
	for _, s := range []string{"actions.runner.acme.one.service", "actions.runner.acme.two.service"} {
		b, err := os.ReadFile(filepath.Join(root, "system.slice", s, "cpu.max"))
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.TrimSpace(string(b)); got != "400000 100000" {
			t.Errorf("%s cpu.max = %q", s, got)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "system.slice", "ssh.service", "cpu.max")); err == nil {
		t.Error("a service that is not a runner was capped")
	}
	// 100 lifts the cap again.
	if res := cpucap.ApplyCgroups(root, 8, 100); res.State != cpucap.Applied {
		t.Fatalf("lifting the cap: %+v", res)
	}
	b, _ := os.ReadFile(filepath.Join(root, "system.slice", "actions.runner.acme.one.service", "cpu.max"))
	if strings.TrimSpace(string(b)) != "max 100000" {
		t.Errorf("cap not lifted: %q", b)
	}
}

func TestApplyCgroupsWithoutCgroupV2(t *testing.T) {
	res := cpucap.ApplyCgroups(t.TempDir(), 4, 50)
	if res.State != cpucap.NotSupported || res.Detail == "" {
		t.Fatalf("result = %+v, want not_supported with a reason", res)
	}
}

func TestApplyCgroupsWithNoRunnerYet(t *testing.T) {
	res := cpucap.ApplyCgroups(fakeCgroupRoot(t), 4, 50)
	if res.State != cpucap.Applied || res.Targets != 0 || res.Detail == "" {
		t.Fatalf("result = %+v, want applied on 0 services with a note", res)
	}
}

func TestApplyCgroupsReportsAFolderItCannotWrite(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("a read-only folder only stops a non-root user on a Unix OS")
	}
	root := fakeCgroupRoot(t, "actions.runner.acme.one.service")
	if err := os.Chmod(filepath.Join(root, "system.slice", "actions.runner.acme.one.service"), 0o500); err != nil {
		t.Fatal(err)
	}
	res := cpucap.ApplyCgroups(root, 4, 50)
	if res.State != cpucap.Failed || res.Detail == "" {
		t.Fatalf("result = %+v, want failed with a reason", res)
	}
}

func TestValid(t *testing.T) {
	for _, p := range []int{25, 50, 75, 100} {
		if !cpucap.Valid(p) {
			t.Errorf("%d should be valid", p)
		}
	}
	for _, p := range []int{0, 10, 60, 101} {
		if cpucap.Valid(p) {
			t.Errorf("%d should not be valid", p)
		}
	}
}
