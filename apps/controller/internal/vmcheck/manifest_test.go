package vmcheck_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/vmcheck"
)

func qemuFake(calls *int) vmcheck.Deps {
	return vmcheck.Deps{Run: func(context.Context, string, ...string) error { *calls++; return nil }}
}

func TestManifestRoundTripSortedAndMissingReadsEmpty(t *testing.T) {
	base := filepath.Join(t.TempDir(), "d-1.qcow2")
	if got, err := vmcheck.ReadManifest(base); err != nil || len(got) != 0 {
		t.Fatalf("no file: %v %v", got, err)
	}
	if err := vmcheck.WriteManifest(base, []string{"make", " git ", "", "git"}); err != nil {
		t.Fatal(err)
	}
	got, err := vmcheck.ReadManifest(base)
	if err != nil || !reflect.DeepEqual(got, []string{"git", "make"}) {
		t.Fatalf("%v %v", got, err)
	}
	if !strings.HasSuffix(vmcheck.ManifestFile(base), "d-1.packages") {
		t.Fatal(vmcheck.ManifestFile(base))
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(base), "*.tmp-*")); len(left) != 0 {
		t.Fatalf("left %v", left)
	}
}

func TestMissingPackages(t *testing.T) {
	got := vmcheck.MissingPackages([]string{"git"}, []string{"zip", "git", "curl", "zip"})
	if !reflect.DeepEqual(got, []string{"curl", "zip"}) {
		t.Fatalf("%v", got)
	}
	if got := vmcheck.MissingPackages(nil, nil); len(got) != 0 {
		t.Fatalf("%v", got)
	}
}

func TestEnsureBaseWritesAnEmptyManifestAndFillsOneForAnOldBase(t *testing.T) {
	dir := t.TempDir()
	var n int
	img := filepath.Join(dir, "images", "t-1-aaaaaaaaaaaaaaaa.qcow2")
	base, err := vmcheck.EnsureBase(context.Background(), qemuFake(&n), dir, "t-1", img, strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(vmcheck.ManifestFile(base)); err != nil {
		t.Fatalf("no manifest: %v", err)
	}
	// A base made before manifests gets one at the next use; the disk is not rebuilt.
	if err := os.Remove(vmcheck.ManifestFile(base)); err != nil {
		t.Fatal(err)
	}
	if _, err := vmcheck.EnsureBase(context.Background(), qemuFake(&n), dir, "t-1", img, strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(vmcheck.ManifestFile(base)); err != nil || n != 1 {
		t.Fatalf("manifest %v, qemu-img calls %d", err, n)
	}
}

func TestEnsureBasePackagesInstallsThenListsThem(t *testing.T) {
	dir := t.TempDir()
	var n int
	img := filepath.Join(dir, "images", "t-1-aaaaaaaaaaaaaaaa.qcow2")
	var gotPkgs []string
	install := func(_ context.Context, disk string, pkgs []string) error {
		if _, err := os.Stat(disk); err != nil {
			t.Errorf("the disk is not made when the installer runs: %v", err)
		}
		gotPkgs = pkgs
		return nil
	}
	base, err := vmcheck.EnsureBasePackages(context.Background(), qemuFake(&n), dir, "t-1", img, strings.Repeat("a", 64), []string{"make", "git"}, install)
	if err != nil {
		t.Fatal(err)
	}
	have, err := vmcheck.ReadManifest(base)
	if err != nil || !reflect.DeepEqual(have, []string{"git", "make"}) || !reflect.DeepEqual(gotPkgs, have) {
		t.Fatalf("manifest %v installed %v %v", have, gotPkgs, err)
	}
	// The package list is part of the key: another list is another disk.
	if base == vmcheck.BaseFile(dir, "t-1", vmcheck.BaseKey(strings.Repeat("a", 64), nil)) {
		t.Fatal("the key ignores the packages")
	}
}

func TestEnsureBasePackagesRefusesWithoutInstallerAndLeavesNothingOnFailure(t *testing.T) {
	dir := t.TempDir()
	var n int
	img := filepath.Join(dir, "images", "t-1-aaaaaaaaaaaaaaaa.qcow2")
	if _, err := vmcheck.EnsureBasePackages(context.Background(), qemuFake(&n), dir, "t-1", img, "d", []string{"git"}, nil); !errors.Is(err, vmcheck.ErrNoInstaller) || n != 0 {
		t.Fatalf("%v, calls %d", err, n)
	}
	failing := func(context.Context, string, []string) error { return errors.New("apt failed") }
	if _, err := vmcheck.EnsureBasePackages(context.Background(), qemuFake(&n), dir, "t-1", img, "d", []string{"git"}, failing); err == nil {
		t.Fatal("no error")
	}
	if left, _ := filepath.Glob(filepath.Join(dir, "bases", "*")); len(left) != 0 {
		t.Fatalf("left %v", left)
	}
}

func TestPruneRemovesTheManifestWithItsBaseAndCountsOnlyDisks(t *testing.T) {
	dir := t.TempDir()
	var n int
	cat := catOf(t, "t-1")
	img := filepath.Join(dir, "images", "t-1-aaaaaaaaaaaaaaaa.qcow2")
	old, err := vmcheck.EnsureBase(context.Background(), qemuFake(&n), dir, "t-1", img, strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	newer, err := vmcheck.EnsureBase(context.Background(), qemuFake(&n), dir, "t-1", img, strings.Repeat("b", 64))
	if err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-48 * time.Hour)
	for _, p := range []string{old, vmcheck.ManifestFile(old)} {
		if err := os.Chtimes(p, past, past); err != nil {
			t.Fatal(err)
		}
	}
	res, err := vmcheck.Prune(context.Background(), dir, cat, vmcheck.PruneSpec{Keep: 1}, func(int) bool { return false })
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Removed) != 1 || res.Removed[0] != old {
		t.Fatalf("removed %v", res.Removed)
	}
	if _, err := os.Stat(vmcheck.ManifestFile(old)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("manifest of the removed base stays: %v", err)
	}
	if _, err := os.Stat(vmcheck.ManifestFile(newer)); err != nil {
		t.Fatalf("manifest of the kept base: %v", err)
	}
}
