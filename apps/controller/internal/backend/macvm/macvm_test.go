package macvm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend/vmclone"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/vmcheck"
)

type fakeVM struct {
	fw      *fakeFW
	stopErr error
}

func (v *fakeVM) Stop(context.Context) error {
	v.fw.mu.Lock()
	defer v.fw.mu.Unlock()
	v.fw.running--
	return v.stopErr
}

type fakeFW struct {
	mu       sync.Mutex
	host     Host
	foreign  int // VMs the agent did not start
	running  int // VMs the agent started
	peak     int
	bootErr  error
	visErr   error
	bootedOn []string
}

func (f *fakeFW) Host(context.Context) (Host, error) { return f.host, nil }
func (f *fakeFW) Visible(context.Context) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.foreign + f.running, f.visErr
}
func (f *fakeFW) Boot(_ context.Context, s vmclone.BootSpec) (vmclone.VM, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.bootErr != nil {
		return nil, f.bootErr
	}
	f.running++
	if f.foreign+f.running > f.peak {
		f.peak = f.foreign + f.running
	}
	f.bootedOn = append(f.bootedOn, s.Disk)
	return &fakeVM{fw: f}, nil
}

var m2 = Host{OS: "darwin", Arch: "arm64", MacOS: "14.5"}

func TestCheckRefusesIntelAndOldMacOS(t *testing.T) {
	cases := []struct {
		h      Host
		usable bool
		want   string
	}{
		{m2, true, ""},
		{Host{OS: "darwin", Arch: "amd64", MacOS: "14.5"}, false, "Intel"},
		{Host{OS: "darwin", Arch: "arm64", MacOS: "12.7"}, false, "macOS 13 or newer"},
		{Host{OS: "darwin", Arch: "arm64", MacOS: "x"}, false, "unknown"},
		{Host{OS: "linux", Arch: "arm64"}, false, "only on a Mac"},
	}
	for _, c := range cases {
		s := Check(c.h)
		if s.Usable != c.usable || !strings.Contains(s.Reason, c.want) {
			t.Errorf("%+v: usable=%v reason=%q", c.h, s.Usable, s.Reason)
		}
	}
}

func TestAtMostTwoVms(t *testing.T) {
	fw := &fakeFW{host: m2}
	lim := &Limiter{FW: fw}
	var rel []func()
	for i := 0; i < 2; i++ {
		r, b, err := lim.TryAcquire(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		b()
		rel = append(rel, r)
	}
	_, _, err := lim.TryAcquire(context.Background())
	if !errors.Is(err, ErrAtLimit) || !strings.Contains(err.Error(), "waits for a free VM") {
		t.Fatalf("third VM: %v", err)
	}
	rel[0]()
	rel[0]() // twice is safe
	if _, _, err := lim.TryAcquire(context.Background()); err != nil {
		t.Fatalf("after release: %v", err)
	}
}

func TestForeignVmsCount(t *testing.T) {
	fw := &fakeFW{host: m2, foreign: 1}
	lim := &Limiter{FW: fw}
	if _, _, err := lim.TryAcquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, _, err := lim.TryAcquire(context.Background())
	if !errors.Is(err, ErrAtLimit) || !strings.Contains(err.Error(), "1 by others") {
		t.Fatalf("one foreign + one ours must stop the next: %v", err)
	}
	fw.foreign = 2
	c, err := lim.Count(context.Background())
	if err != nil || c.Running != 3 || c.Ours != 1 || c.Limit != 2 {
		t.Fatalf("count %+v %v", c, err)
	}
}

func TestCountDoesNotDoubleOurVms(t *testing.T) {
	fw := &fakeFW{host: m2}
	lim := &Limiter{FW: fw}
	b := &Booter{Limiter: lim}
	vm, err := b.Boot(context.Background(), vmclone.BootSpec{JobID: "j1", Disk: "d1"})
	if err != nil {
		t.Fatal(err)
	}
	c, _ := lim.Count(context.Background())
	if c.Running != 1 || c.Ours != 1 {
		t.Fatalf("booted VM seen by the host must count once: %+v", c)
	}
	if err := vm.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	c, _ = lim.Count(context.Background())
	if c.Running != 0 || c.Ours != 0 {
		t.Fatalf("after stop: %+v", c)
	}
}

func TestCountErrorRefusesToStart(t *testing.T) {
	fw := &fakeFW{host: m2, visErr: errors.New("pgrep broke")}
	if _, _, err := (&Limiter{FW: fw}).TryAcquire(context.Background()); err == nil || errors.Is(err, ErrAtLimit) {
		t.Fatalf("an unknown count must not start a VM: %v", err)
	}
}

func TestThirdJobWaitsAndSaysWhy(t *testing.T) {
	fw := &fakeFW{host: m2}
	lim := &Limiter{FW: fw}
	var said []string
	var smu sync.Mutex
	heard := make(chan struct{}, 1)
	b := &Booter{Limiter: lim, Poll: time.Hour, Say: func(j, r string) {
		smu.Lock()
		said = append(said, j+": "+r)
		smu.Unlock()
		select {
		case heard <- struct{}{}:
		default:
		}
	}}
	ctx := context.Background()
	v1, _ := b.Boot(ctx, vmclone.BootSpec{JobID: "a"})
	_, _ = b.Boot(ctx, vmclone.BootSpec{JobID: "b"})
	done := make(chan error, 1)
	go func() { _, err := b.Boot(ctx, vmclone.BootSpec{JobID: "c"}); done <- err }()
	select {
	case <-heard:
	case <-time.After(5 * time.Second):
		t.Fatal("job c never said why it waits")
	}
	select {
	case err := <-done:
		t.Fatalf("third job started: %v", err)
	default:
	}
	if err := v1.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if fw.peak > MaxVMs {
		t.Fatalf("peak %d VMs", fw.peak)
	}
	if !strings.HasPrefix(said[0], "c: ") || !strings.Contains(said[0], "Apple's licence") {
		t.Fatalf("reason %q", said)
	}
}

func TestWaitStopsWithTheJob(t *testing.T) {
	fw := &fakeFW{host: m2, foreign: 2}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := (&Booter{Limiter: &Limiter{FW: fw}, Poll: 5 * time.Millisecond}).Boot(ctx, vmclone.BootSpec{JobID: "x"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("%v", err)
	}
}

func TestFailedBootGivesSlotBack(t *testing.T) {
	fw := &fakeFW{host: m2, bootErr: ErrNoBridge}
	lim := &Limiter{FW: fw}
	b := &Booter{Limiter: lim}
	for i := 0; i < 3; i++ {
		if _, err := b.Boot(context.Background(), vmclone.BootSpec{JobID: "j"}); !errors.Is(err, ErrNoBridge) {
			t.Fatalf("%v", err)
		}
	}
	if c, _ := lim.Count(context.Background()); c.Ours != 0 {
		t.Fatalf("%+v", c)
	}
}

type fakeFS struct{ cloned, removed []string }

func (f *fakeFS) CloneFile(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	f.cloned = append(f.cloned, dst)
	return os.WriteFile(dst, b, 0o600)
}
func (f *fakeFS) Remove(p string) error           { f.removed = append(f.removed, p); return os.Remove(p) }
func (f *fakeFS) List(d string) ([]string, error) { return OSFS{}.List(d) }

func TestCloneMadePerJobAndRemovedAfter(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "base.img")
	if err := os.WriteFile(base, []byte("disk"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(vmcheck.ManifestFile(base), []byte("docker\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fs := &fakeFS{}
	fw := &fakeFW{host: m2}
	r := &vmclone.Runner{Disks: Disks{FS: fs}, Booter: &Booter{Limiter: &Limiter{FW: fw}}}
	sup := Check(m2)
	req := vmclone.Request{
		JobID: "job/1", Found: []backend.Support{sup}, Allowed: map[backend.Name]bool{Name: true},
		Base: base, CloneDir: filepath.Join(dir, "clones"),
	}
	if err := os.MkdirAll(req.CloneDir, 0o700); err != nil {
		t.Fatal(err)
	}
	ran := false
	err := r.Run(context.Background(), req, func(context.Context, vmclone.VM) error {
		ran = true
		if c, _ := (&Limiter{FW: fw}).Count(context.Background()); c.Running != 1 {
			t.Errorf("one VM must run in the job: %+v", c)
		}
		return errors.New("job failed")
	})
	if !ran || err == nil || !strings.Contains(err.Error(), "job failed") {
		t.Fatalf("ran=%v err=%v", ran, err)
	}
	want := vmclone.CloneFile(req.CloneDir, "job/1")
	if len(fs.cloned) != 1 || fs.cloned[0] != want || len(fs.removed) != 1 || fs.removed[0] != want {
		t.Fatalf("cloned %v removed %v", fs.cloned, fs.removed)
	}
	if _, err := os.Stat(want); !os.IsNotExist(err) {
		t.Fatalf("clone still there: %v", err)
	}
	if fw.running != 0 || fw.bootedOn[0] != want {
		t.Fatalf("VM running=%d on %v", fw.running, fw.bootedOn)
	}
	if err := (Disks{FS: fs}).Remove(context.Background(), want); err != nil {
		t.Fatalf("removing a missing clone must be fine: %v", err)
	}
}

func TestIntelMacRefusedBeforeAnyDisk(t *testing.T) {
	fs := &fakeFS{}
	r := &vmclone.Runner{Disks: Disks{FS: fs}, Booter: &Booter{Limiter: &Limiter{FW: &fakeFW{}}}}
	sup := Check(Host{OS: "darwin", Arch: "amd64", MacOS: "14.0"})
	err := r.Run(context.Background(), vmclone.Request{
		JobID: "j", Found: []backend.Support{sup}, Allowed: map[backend.Name]bool{Name: true},
		Base: "b", CloneDir: "c",
	}, func(context.Context, vmclone.VM) error { return nil })
	if !errors.Is(err, vmclone.ErrNoVMBackend) || !strings.Contains(err.Error(), "Intel") || len(fs.cloned) != 0 {
		t.Fatalf("%v cloned=%v", err, fs.cloned)
	}
}

func TestSweepDeletesOnlyOrphanClones(t *testing.T) {
	dir := t.TempDir()
	keep := vmclone.CloneFile(dir, "live")
	orphan := vmclone.CloneFile(dir, "dead")
	other := filepath.Join(dir, "base.img")
	for _, p := range []string{keep, orphan, other} {
		if err := os.WriteFile(p, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	r := &vmclone.Runner{Disks: Disks{FS: &fakeFS{}}}
	gone, err := r.Sweep(context.Background(), dir, map[string]bool{"live": true})
	if err != nil || len(gone) != 1 || gone[0] != orphan {
		t.Fatalf("%v %v", gone, err)
	}
	if _, err := os.Stat(other); err != nil {
		t.Fatal("the base disk must stay")
	}
}

func TestMajorVersion(t *testing.T) {
	if n, err := MajorVersion("15.1.1\n"); err != nil || n != 15 {
		t.Fatal(n, err)
	}
	if _, err := MajorVersion(""); err == nil {
		t.Fatal("empty")
	}
}

func TestStubFrameworkOffAMac(t *testing.T) {
	fw := NewFramework()
	if _, err := fw.Boot(context.Background(), vmclone.BootSpec{}); !errors.Is(err, ErrNoBridge) {
		t.Fatal(err)
	}
}
