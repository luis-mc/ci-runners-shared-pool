package runners

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestPackageNamesAllowedAndRefused(t *testing.T) {
	for _, ok := range []string{"git", "libssl-dev", "g++", "python3.12", "Microsoft.VisualStudioCode"} {
		if err := ValidatePackageNames([]string{ok}); err != nil {
			t.Errorf("%q refused: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "-y", "a b", "a;rm", "$(x)", "a/b", "a=1", "né", strings.Repeat("a", MaxPackageName+1)} {
		if err := ValidatePackageNames([]string{bad}); !errors.Is(err, ErrPackageName) {
			t.Errorf("%q allowed", bad)
		}
	}
	if _, err := InstallArgv(PkgApt, []string{"git", "--force"}); err == nil {
		t.Error("flag-like name reached argv")
	}
	if _, err := InstallArgv("pacman", []string{"git"}); err == nil {
		t.Error("unknown manager accepted")
	}
}

func TestPackageSetHashIgnoresOrderKeysEnvAndManager(t *testing.T) {
	a := PackageSetHash(PkgApt, "img", []string{"b", "a", "a"})
	if a != PackageSetHash(PkgApt, "img", []string{"a", "b"}) {
		t.Error("order or repeats change the hash")
	}
	if a == PackageSetHash(PkgDnf, "img", []string{"a", "b"}) || a == PackageSetHash(PkgApt, "other", []string{"a", "b"}) {
		t.Error("manager and env are not in the hash")
	}
}

func TestInstallCommandsPerManager(t *testing.T) {
	c, err := InstallCommands(PkgWinget, []string{"B.Id", "A.Id"})
	if err != nil || len(c) != 2 || c[0][3] != "A.Id" {
		t.Fatalf("winget %v %v", c, err)
	}
	c, err = InstallCommands(PkgApt, []string{"git", "curl"})
	if err != nil || len(c) != 1 || c[0][len(c[0])-1] != "git" || c[0][5] != "curl" {
		t.Fatalf("apt %v %v", c, err)
	}
}

func TestPackageInstallCacheHit(t *testing.T) {
	var runs int
	p := &PackageInstaller{Dir: t.TempDir(), Exec: func(context.Context, []string) (string, error) { runs++; return "", nil }}
	hit, err := p.Ensure(context.Background(), PkgApt, "img", []string{"git", "curl"})
	if err != nil || hit || runs != 1 {
		t.Fatalf("first: hit=%v err=%v runs=%d", hit, err, runs)
	}
	hit, err = p.Ensure(context.Background(), PkgApt, "img", []string{"curl", "git"})
	if err != nil || !hit || runs != 1 {
		t.Fatalf("second: hit=%v err=%v runs=%d", hit, err, runs)
	}
	if _, err := p.Ensure(context.Background(), PkgApt, "img", []string{"curl", "git", "jq"}); err != nil || runs != 2 {
		t.Fatalf("changed set did not run: %v runs=%d", err, runs)
	}
}

func TestPackageInstallFailureReportedThenCleared(t *testing.T) {
	fail := true
	p := &PackageInstaller{Dir: t.TempDir(), Redact: func(s string) string { return strings.ReplaceAll(s, "hunter2", "***") },
		Exec: func(context.Context, []string) (string, error) {
			if fail {
				return "E: unable to locate package nope hunter2", errors.New("exit 100")
			}
			return "", nil
		}}
	if _, err := p.Ensure(context.Background(), PkgApt, "img", []string{"nope"}); err == nil {
		t.Fatal("failure not returned")
	}
	f := p.Failures()
	if len(f) != 1 || f[0].Names[0] != "nope" || !strings.Contains(f[0].Error, "unable to locate") || strings.Contains(f[0].Error, "hunter2") {
		t.Fatalf("failures %+v", f)
	}
	if _, err := p.Ensure(context.Background(), PkgApt, "img", []string{"nope"}); err == nil {
		t.Fatal("a failed set was cached")
	}
	fail = false
	if _, err := p.Ensure(context.Background(), PkgApt, "img", []string{"nope"}); err != nil || len(p.Failures()) != 0 {
		t.Fatalf("not cleared: %v %v", err, p.Failures())
	}
}

func TestPackageInstallRefusesBadNameBeforeExec(t *testing.T) {
	p := &PackageInstaller{Dir: t.TempDir(), Exec: func(context.Context, []string) (string, error) { t.Fatal("ran"); return "", nil }}
	if _, err := p.Ensure(context.Background(), PkgApt, "img", []string{"git; reboot"}); !errors.Is(err, ErrPackageName) {
		t.Fatalf("err %v", err)
	}
}
