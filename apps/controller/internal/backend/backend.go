// Package backend is the runner isolation backend plugin seam (decision 0003;
// runners R5.8 to R5.12, R5.24, R6.1 to R6.4).
//
// Every backend implements Backend. The agent calls backends only through it
// (R5.8). Milestone 1 builds the persistent backend for Windows, Linux and
// macOS, each in a sub-package behind build tags and built only into the
// binary for its OS (R5.24); later milestones add container, VM, Tart and
// microVM backends behind the same interface.
//
// Rules every backend keeps: no privileged container, no host PID or network
// namespace, no Docker or Podman socket in a job, KVM only as a device
// (R6.1); no token, JIT config or password is written anywhere (R6.2);
// CleanUp is called after Stop, also after an error or timeout, and is safe
// to call twice (R5.10).
package backend

import (
	"context"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runnercache"
)

// Name names a backend, as reported in Support and allowed per machine
// (machines R5.11, R5.22).
type Name string

// Backend names of the product. Persistent is M1.
const (
	Persistent  Name = "persistent"
	Docker      Name = "docker"
	Podman      Name = "podman"
	Nerdctl     Name = "nerdctl"
	WSL2        Name = "wsl2"
	HyperV      Name = "hyperv"
	KVM         Name = "kvm"
	Tart        Name = "tart"
	Firecracker Name = "firecracker"
)

// Isolation is what the console names for each runner (runners R5.7).
type Isolation string

// Isolation kinds.
const (
	IsolationMicroVM    Isolation = "microVM"
	IsolationVM         Isolation = "VM"
	IsolationContainer  Isolation = "container"
	IsolationPersistent Isolation = "persistent"
)

// Support is the result of Detect (machines R5.11, runners R5.9).
type Support struct {
	Name      Name
	Present   bool
	Usable    bool
	Version   string
	Reason    string // why it is not usable, such as "no /dev/kvm"
	Rootless  bool
	Isolation Isolation
	// OS lists the runner OSes this backend can start here.
	OS []string
}

// ImageSpec names the image a runner needs (M2 onward; the persistent backend
// returns an empty ImageRef).
type ImageSpec struct {
	OS     string
	Arch   string
	Layers []string
	// Extras are the extra distribution packages of the pool (runners R5.51);
	// they are part of the image key.
	Extras []string
}

// ImageRef is a prepared image on this machine.
type ImageRef struct {
	ID     string
	SizeMB int64
}

// IDSource gives a runner its persistent uid/gid from the reserved range
// (runners R5.72). *runnerid.Allocator is the real one.
type IDSource interface {
	ID(ctx context.Context, name string) (int, error)
}

// RunnerSpec is the runner to start (runners R5.2, R5.13 to R5.23, R5.36).
type RunnerSpec struct {
	ID        string
	Name      string
	OS        string
	Arch      string
	Labels    []string // generic labels only
	GitHubURL string   // https://github.com/<owner>[/<repo>]
	Group     string
	// Pinned runner version and its SHA-256 (runners R5.37).
	RunnerVersion string
	RunnerSHA256  string
	Image         ImageRef
	// WindowsSymlinkRight grants "create symbolic links" to the runner account
	// (owner decision for M1, runners R5.14).
	WindowsSymlinkRight bool
	// CacheQuota is the declared cache sizing of this runner. A zero value
	// takes the defaults. The runner exports CI_TOOLS_DIR and the quotas to
	// its jobs (runners R5.39).
	CacheQuota runnercache.Quota
	// SharedTools asks for the shared tool folders of the runner's repository,
	// with group access for every runner user of it and an own build-output
	// folder for this runner (caches R5.57). The zero value keeps the runner's
	// own folders.
	SharedTools runnercache.SharedTools
	// MaxParallel is the runner's CPU count, exported to its jobs as
	// CI_MAX_PARALLEL (runners R5.53, machines R5.54). 0 exports none.
	MaxParallel int
	// PoolMemoryMB and PoolCPUs are the pool size of the machine; the
	// container backend turns them into the limits of the repository's pool
	// container (runners R5.27). 0 means none is set.
	PoolMemoryMB int
	PoolCPUs     float64
	// ExtraPackages are the extra distribution packages of the repository's
	// pool (runners R5.51); the image is built with them.
	ExtraPackages []string
	// KeepDependencies is the persistent runner's setting Keep downloaded
	// dependencies (runners R5.90). The backends write it to the runner folder;
	// the clean-up hooks read it. A runner made before the setting keeps.
	KeepDependencies bool
	// Container asks for a persistent runner in the long-lived Linux pool
	// container of a Windows machine (runners R5.93). It is set with a
	// registration token, never with a JIT config.
	Container bool
	// Pool is what the repository's pool settings ask for (runners R5.50,
	// R5.54). The zero value asks for the base image only.
	Pool PoolSetup
	// NeedsDocker asks for Docker inside the job (runners R5.66). The job then
	// runs in a per-job clone of a VM base disk with its own engine, never with
	// the host's Docker socket, and a machine with no VM backend refuses it.
	NeedsDocker bool
}

// PoolSetup is the part of a repository's pool settings the container backend
// turns into the pool start: the image layers, the lane of its caches and the
// database it declares.
type PoolSetup struct {
	// Layers are the image layers the pool has turned on (runners R5.50).
	Layers []string
	// RepoID names the cache lane of the repository (caches R5.43). Empty
	// means the pool takes no lane.
	RepoID string
	// Database is set when the repository declares a database (runners R5.54).
	Database *PoolDatabase
	// Isolation is the container isolation of the runner definition: empty or
	// "process" (default) or "hyperv" (runners R5.65).
	Isolation string
}

// PoolDatabase is what a repository declares for its Postgres login.
type PoolDatabase struct {
	CreateDB bool     // repository setting db_createdb
	Roles    []string // declared NOLOGIN roles
}

// StartConfig carries the one-time credential to start a runner. Registration
// or JITConfig is set. It is used once and dropped; it is never logged (R6.2).
type StartConfig struct {
	// Registration is a registration token for a persistent runner.
	Registration string
	// JITConfig is an encoded just-in-time config for an ephemeral runner (M2).
	JITConfig string
	// Removal is a remove token. It is set only to register a runner again in
	// place: the old registration is removed with it first (runners R5.19).
	Removal string
}

// Handle names a started runner for later calls.
type Handle struct {
	RunnerID string
	// Ref is backend-specific: a service name, unit, container or VM id.
	Ref string
}

// LogLine is one setup or runner log line, already redacted (R5.11).
type LogLine struct {
	At      time.Time
	Level   string
	Message string
	Step    string // prepare image, register, start, stop, clean up, deregister
}

// Usage is what ReportUsage gives per runner and job (R5.12).
type Usage struct {
	CPU          time.Duration
	MemoryPeak   int64
	MemoryMedian int64
	DiskUsed     int64
	Wall         time.Duration
	// StoppedBy says why the job or runner ended when the cause is known, such
	// as "stopped by the memory cap (exit 137)" (runners R5.85).
	StoppedBy string
}

// Backend is one isolation backend (decision 0003, runners R5.8).
type Backend interface {
	// Name is the backend's stable name.
	Name() Name
	// Detect says whether the backend is present and usable here, and why not.
	Detect(ctx context.Context) (Support, error)
	// PrepareImage builds or finds the image (M2 onward).
	PrepareImage(ctx context.Context, spec ImageSpec) (ImageRef, error)
	// Start registers and starts a runner with a JIT config or a registration.
	Start(ctx context.Context, r RunnerSpec, cfg StartConfig) (Handle, error)
	// StreamLogs sends log lines to out until ctx ends or the runner stops.
	StreamLogs(ctx context.Context, h Handle, out chan<- LogLine) error
	// Stop stops the runner. Callers drain first and check for an idle runner
	// right before (R6.4).
	Stop(ctx context.Context, h Handle) error
	// CleanUp removes what the runner left; idempotent (R5.10).
	CleanUp(ctx context.Context, h Handle) error
	// ReportUsage gives the runner's usage so far.
	ReportUsage(ctx context.Context, h Handle) (Usage, error)
}

// Registry is the set of backends built into this binary for this OS.
type Registry interface {
	// All returns every backend compiled in, best first per the decision 0003 table.
	All() []Backend
	// Get returns the backend with the name, or false.
	Get(name Name) (Backend, bool)
}
