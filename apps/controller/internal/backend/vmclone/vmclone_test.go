package vmclone

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/vmcheck"
)

type fakeDisks struct {
	live    map[string]bool
	cloned  []string
	removed []string
	failRm  bool
}

func (d *fakeDisks) Clone(_ context.Context, base, clone string) error {
	d.live[clone] = true
	d.cloned = append(d.cloned, base+">"+clone)
	return nil
}

func (d *fakeDisks) Remove(_ context.Context, clone string) error {
	if d.failRm {
		return errors.New("disk busy")
	}
	delete(d.live, clone)
	d.removed = append(d.removed, clone)
	return nil
}

func (d *fakeDisks) List(context.Context, string) ([]string, error) {
	var l []string
	for c := range d.live {
		l = append(l, c)
	}
	return l, nil
}

type fakeVM struct{ stopped int }

func (v *fakeVM) Stop(context.Context) error { v.stopped++; return nil }

type fakeBooter struct {
	specs []BootSpec
	vm    *fakeVM
	err   error
}

func (b *fakeBooter) Boot(_ context.Context, s BootSpec) (VM, error) {
	b.specs = append(b.specs, s)
	if b.err != nil {
		return nil, b.err
	}
	b.vm = &fakeVM{}
	return b.vm, nil
}

func setup(t *testing.T, pkgs []string) (*Runner, *fakeDisks, *fakeBooter, Request) {
	t.Helper()
	dir := t.TempDir()
	base := filepath.Join(dir, "bases", "fedora-abc.qcow2")
	if err := mkdir(filepath.Dir(base)); err != nil {
		t.Fatal(err)
	}
	if err := vmcheck.WriteManifest(base, pkgs); err != nil {
		t.Fatal(err)
	}
	d := &fakeDisks{live: map[string]bool{}}
	b := &fakeBooter{}
	req := Request{
		JobID:    "job 1",
		Found:    []backend.Support{{Name: backend.KVM, Present: true, Usable: true, Isolation: backend.IsolationVM}},
		Allowed:  map[backend.Name]bool{backend.KVM: true},
		Base:     base,
		CloneDir: filepath.Join(dir, "clones"),
	}
	return &Runner{Disks: d, Booter: b}, d, b, req
}

func TestCloneMadePerJobAndRemovedAfter(t *testing.T) {
	r, d, b, req := setup(t, []string{"docker", "git"})
	for _, id := range []string{"job-1", "job-2"} {
		req.JobID = id
		err := r.Run(context.Background(), req, func(context.Context, VM) error {
			if !d.live[CloneFile(req.CloneDir, id)] {
				t.Errorf("clone of %s missing during the job", id)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(d.cloned) != 2 || len(d.removed) != 2 || len(d.live) != 0 {
		t.Fatalf("cloned %v removed %v live %v", d.cloned, d.removed, d.live)
	}
	if d.cloned[0] == d.cloned[1] {
		t.Fatal("two jobs shared one clone")
	}
	if b.vm.stopped != 1 {
		t.Fatalf("vm stopped %d times", b.vm.stopped)
	}
}

func TestCloneRemovedAfterJobError(t *testing.T) {
	r, d, _, req := setup(t, []string{"docker"})
	boom := errors.New("job failed")
	err := r.Run(context.Background(), req, func(context.Context, VM) error { return boom })
	if !errors.Is(err, boom) || len(d.live) != 0 {
		t.Fatalf("err %v live %v", err, d.live)
	}
}

func TestCloneRemovedAfterBootError(t *testing.T) {
	r, d, b, req := setup(t, []string{"docker"})
	b.err = errors.New("no boot")
	if err := r.Run(context.Background(), req, func(context.Context, VM) error { return nil }); err == nil {
		t.Fatal("want boot error")
	}
	if len(d.live) != 0 {
		t.Fatalf("clone left: %v", d.live)
	}
}

func TestRemoveFailureIsReported(t *testing.T) {
	r, d, _, req := setup(t, []string{"docker"})
	d.failRm = true
	err := r.Run(context.Background(), req, func(context.Context, VM) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "remove the job clone") {
		t.Fatalf("err %v", err)
	}
}

func TestHostSocketNeverMounted(t *testing.T) {
	r, _, b, req := setup(t, []string{"docker"})
	req.Tools = []backend.ToolMount{{HostPath: "/opt/tools", GuestPath: backend.ToolGuestPath, ReadOnly: true}}
	if err := r.Run(context.Background(), req, func(context.Context, VM) error { return nil }); err != nil {
		t.Fatal(err)
	}
	for _, m := range b.specs[0].Mounts {
		if strings.Contains(m.HostPath, "sock") || !m.ReadOnly {
			t.Fatalf("bad mount %+v", m)
		}
	}
	for _, p := range []string{"/var/run/docker.sock", `\\.\pipe\docker_engine`, "/run/podman/podman.sock"} {
		req.Tools = []backend.ToolMount{{HostPath: p, ReadOnly: true}}
		b.specs = nil
		if err := r.Run(context.Background(), req, func(context.Context, VM) error { return nil }); err == nil {
			t.Fatalf("%s was accepted", p)
		}
		if len(b.specs) != 0 {
			t.Fatalf("VM booted with %s", p)
		}
	}
}

func TestRefusedWithoutVMBackend(t *testing.T) {
	r, d, b, req := setup(t, []string{"docker"})
	req.Found = []backend.Support{
		{Name: backend.Docker, Present: true, Usable: true, Isolation: backend.IsolationContainer},
		{Name: backend.KVM, Present: true, Usable: false, Reason: "no /dev/kvm", Isolation: backend.IsolationVM},
	}
	err := r.Run(context.Background(), req, func(context.Context, VM) error { t.Fatal("job ran"); return nil })
	if !errors.Is(err, ErrNoVMBackend) || !strings.Contains(err.Error(), "no /dev/kvm") {
		t.Fatalf("err %v", err)
	}
	if len(d.cloned) != 0 || len(b.specs) != 0 {
		t.Fatal("work done before the refusal")
	}
	req.Found = nil
	if _, err := PickVM(req.Found, req.Allowed); !errors.Is(err, ErrNoVMBackend) {
		t.Fatalf("err %v", err)
	}
	req.Found = []backend.Support{{Name: backend.HyperV, Present: true, Usable: true, Isolation: backend.IsolationVM}}
	if _, err := PickVM(req.Found, req.Allowed); err == nil || !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("err %v", err)
	}
}

func TestBaseWithoutDockerRefused(t *testing.T) {
	r, d, _, req := setup(t, []string{"git"})
	if err := r.Run(context.Background(), req, func(context.Context, VM) error { return nil }); err == nil || len(d.cloned) != 0 {
		t.Fatalf("err %v cloned %v", err, d.cloned)
	}
}

func TestSweepDeletesOrphansOnly(t *testing.T) {
	r, d, _, req := setup(t, []string{"docker"})
	mine := CloneFile(req.CloneDir, "live")
	d.live[mine] = true
	d.live[CloneFile(req.CloneDir, "dead")] = true
	gone, err := r.Sweep(context.Background(), req.CloneDir, map[string]bool{"live": true})
	if err != nil || len(gone) != 1 || !d.live[mine] || len(d.live) != 1 {
		t.Fatalf("gone %v err %v live %v", gone, err, d.live)
	}
}

func mkdir(p string) error { return os.MkdirAll(p, 0o750) }
