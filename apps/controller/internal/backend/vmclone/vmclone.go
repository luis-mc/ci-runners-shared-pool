// Package vmclone runs a job that needs Docker inside a per-job copy-on-write
// clone of the VM base disk (runners R5.66). The clone has its own Docker
// engine; the host's Docker socket is never mounted into a job (R5.27, R6.1).
// The clone is deleted when the job ends, also after an error, and an orphan
// sweep at start deletes every clone no runner owns.
//
// Disk, VM and process work run behind the Disks and Booter seams, so tests
// use fakes and never start a VM or Docker.
package vmclone

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/vmcheck"
)

// CloneSuffix ends every clone file this package makes, so a sweep knows its own.
const CloneSuffix = ".crsp-clone.qcow2"

// DockerPackage is the package a base disk must list in its manifest (HND18)
// to run Docker in a job.
const DockerPackage = "docker"

// ErrNoVMBackend is wrapped by the refusal when the machine has no usable VM backend.
var ErrNoVMBackend = errors.New("no VM backend")

// Disks is the disk work of a clone.
type Disks interface {
	// Clone makes a copy-on-write disk on top of base.
	Clone(ctx context.Context, base, clone string) error
	// Remove deletes a clone; a missing clone is no error.
	Remove(ctx context.Context, clone string) error
	// List returns the clones in dir that this package made.
	List(ctx context.Context, dir string) ([]string, error)
}

// Mount is a host path shared into the VM.
type Mount struct {
	HostPath, GuestPath string
	ReadOnly            bool
}

// BootSpec is what the Booter starts. Mounts holds the tool trees only.
type BootSpec struct {
	Backend backend.Name
	JobID   string
	Disk    string
	Mounts  []Mount
}

// VM is a running job VM.
type VM interface {
	// Stop stops the VM; safe to call twice.
	Stop(ctx context.Context) error
}

// Booter starts a VM on a disk.
type Booter interface {
	Boot(ctx context.Context, s BootSpec) (VM, error)
}

// Request is one job that needs Docker.
type Request struct {
	JobID string
	// Found is what Detect says for each backend on this machine.
	Found []backend.Support
	// Allowed is the backends the machine allows (machines R5.22).
	Allowed map[backend.Name]bool
	// Base is the base disk, built only from the default branch (R5.66).
	Base string
	// CloneDir holds the clones.
	CloneDir string
	// Tools are the read-only tool trees to attach.
	Tools []backend.ToolMount
}

// Runner makes a clone per job.
type Runner struct {
	Disks  Disks
	Booter Booter
}

// PickVM picks a usable, allowed VM backend, or says why none is. It never
// falls back to a container: Docker in a job needs a VM (R5.66).
func PickVM(found []backend.Support, allowed map[backend.Name]bool) (backend.Support, error) {
	var why []string
	for _, s := range found {
		if s.Isolation != backend.IsolationVM && s.Isolation != backend.IsolationMicroVM {
			continue
		}
		switch {
		case !s.Present:
			why = append(why, string(s.Name)+" is not installed here")
		case !allowed[s.Name]:
			why = append(why, string(s.Name)+" is not allowed on this machine")
		case !s.Usable:
			why = append(why, string(s.Name)+" is not usable: "+s.Reason)
		default:
			return s, nil
		}
	}
	msg := "a job that needs Docker needs a VM backend, and this machine has none"
	if len(why) > 0 {
		msg += ": " + strings.Join(why, "; ")
	}
	return backend.Support{}, fmt.Errorf("%w: %s", ErrNoVMBackend, msg)
}

// CloneFile is the clone path of a job.
func CloneFile(dir, jobID string) string {
	return filepath.Join(dir, SafeName(jobID)+CloneSuffix)
}

// SafeName keeps a job id usable as a file name.
func SafeName(id string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		}
		return '-'
	}, id)
}

// checkMounts refuses any share of a container engine socket and any
// read-write tool tree.
func checkMounts(tools []backend.ToolMount) ([]Mount, error) {
	var out []Mount
	for _, t := range tools {
		p := strings.ToLower(filepath.ToSlash(t.HostPath))
		if strings.Contains(p, "docker.sock") || strings.Contains(p, "podman.sock") ||
			strings.Contains(p, "containerd.sock") || strings.Contains(p, "docker_engine") {
			return nil, fmt.Errorf("refusing to share %q: the host's container socket never goes into a job", t.HostPath)
		}
		if !t.ReadOnly {
			return nil, fmt.Errorf("refusing to share %q read-write", t.HostPath)
		}
		out = append(out, Mount{HostPath: t.HostPath, GuestPath: t.GuestPath, ReadOnly: true})
	}
	return out, nil
}

// Run makes a clone of the base, boots a VM on it, calls fn, then stops the VM
// and deletes the clone, also when fn or the boot fails. It refuses before any
// disk work when the machine has no VM backend or the base has no Docker.
func (r *Runner) Run(ctx context.Context, req Request, fn func(ctx context.Context, vm VM) error) (err error) {
	sup, err := PickVM(req.Found, req.Allowed)
	if err != nil {
		return err
	}
	if req.JobID == "" || req.Base == "" || req.CloneDir == "" {
		return errors.New("vmclone: job id, base disk and clone folder are required")
	}
	pkgs, err := vmcheck.ReadManifest(req.Base)
	if err != nil {
		return fmt.Errorf("read the base disk manifest: %w", err)
	}
	if !has(pkgs, DockerPackage) {
		return fmt.Errorf("the base disk %s has no %s package in its manifest; rebuild the base from the default branch (crsp vm doctor)", filepath.Base(req.Base), DockerPackage)
	}
	mounts, err := checkMounts(req.Tools)
	if err != nil {
		return err
	}
	clone := CloneFile(req.CloneDir, req.JobID)
	// Remove first: the clone must exist only after Clone succeeds, and a
	// failed Clone may still leave a file.
	defer func() {
		// The job's context may be cancelled; the delete must still run.
		if rerr := r.Disks.Remove(context.WithoutCancel(ctx), clone); rerr != nil {
			err = errors.Join(err, fmt.Errorf("remove the job clone: %w", rerr))
		}
	}()
	if err := r.Disks.Clone(ctx, req.Base, clone); err != nil {
		return fmt.Errorf("clone the base disk: %w", err)
	}
	vm, err := r.Booter.Boot(ctx, BootSpec{Backend: sup.Name, JobID: req.JobID, Disk: clone, Mounts: mounts})
	if err != nil {
		return fmt.Errorf("boot the job VM: %w", err)
	}
	defer func() {
		if serr := vm.Stop(context.WithoutCancel(ctx)); serr != nil {
			err = errors.Join(err, fmt.Errorf("stop the job VM: %w", serr))
		}
	}()
	return fn(ctx, vm)
}

// Sweep deletes every clone in dir that no runner owns (R5.66). owned holds
// the job ids that are running. It returns the clones it deleted.
func (r *Runner) Sweep(ctx context.Context, dir string, owned map[string]bool) ([]string, error) {
	all, err := r.Disks.List(ctx, dir)
	if err != nil {
		return nil, err
	}
	keep := map[string]bool{}
	for id, ok := range owned {
		if ok {
			keep[CloneFile(dir, id)] = true
		}
	}
	var gone []string
	var errs []error
	for _, c := range all {
		if keep[c] {
			continue
		}
		if err := r.Disks.Remove(ctx, c); err != nil {
			errs = append(errs, err)
			continue
		}
		gone = append(gone, c)
	}
	return gone, errors.Join(errs...)
}

func has(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
