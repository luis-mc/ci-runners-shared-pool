package runners

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/github"
)

// JobRunner is the request for one runner for one job (runners R5.25).
type JobRunner struct {
	Name      string
	MachineID string
	Arch      string
	Backend   string
	// OS is the runner's system: linux (the default), windows or macos. A
	// Windows runner is a one-job process; a macOS runner is a one-job VM
	// (runners R5.110).
	OS     string
	Labels []string // generic labels, and the job's routing label (R5.36, R5.103)
	Scope  Scope
	// ExtraPackages are the extra packages of the repository's pool (R5.51).
	ExtraPackages []string
	// JIT, when set, makes the JIT config of this runner instead of the
	// repository-level call. A job that came from a scale set needs a runner
	// of that scale set (github-integration R5.40).
	JIT func(ctx context.Context) (github.JITConfig, error)
	// JobKey, when set, ties every try for one job together (OPS89): a try
	// first removes the failed record an earlier try of the key left, so a job
	// shows one failed row, not one for each try. A name starts with it.
	JobKey string
}

// ClearFailedStarts removes the failed records of the tries for one job. The
// loop calls it when the job ends (OPS89).
func (s *Service) ClearFailedStarts(ctx context.Context, jobKey string) error {
	if jobKey == "" {
		return nil
	}
	list, err := s.Store.ListRunners(ctx, Filter{State: string(Failed)})
	if err != nil {
		return err
	}
	for _, r := range list {
		if r.Kind == "ephemeral" && strings.HasPrefix(r.Name, jobKey) {
			if err := s.Store.DeleteRunner(ctx, r.ID); err != nil && !errors.Is(err, ErrNotFound) {
				return err
			}
		}
	}
	return nil
}

// StartJobRunner makes one ephemeral runner for one job (runners R5.25,
// github-integration R5.34). It asks GitHub for a JIT config, hands it to the
// agent in the same call and drops it. The config is never in the record, the
// store, the audit trail, an error text or a log: the agent link carries it
// and nothing here keeps a copy. The runner takes one job and exits; the
// agent then calls Stop and CleanUp (R5.26), and a record of an ephemeral
// runner is removed by the caller.
func (s *Service) StartJobRunner(ctx context.Context, j JobRunner, actor string) (Record, error) {
	// Every ephemeral runner carries the ephemeral label (R5.92).
	j.Labels = append(append([]string(nil), j.Labels...), EphemeralLabel)
	labels, err := ValidateLabels(j.Labels, s.poolFor(j.Labels), map[string]bool{"docker": true})
	if err != nil {
		return Record{}, err
	}
	j.Labels = labels
	if j.OS == "" {
		j.OS = "linux"
	}
	iso := IsolationFor(j.OS)
	pin, err := PinFor(j.OS, j.Arch)
	if err != nil {
		return Record{}, err
	}
	if j.JIT == nil {
		// A runner whose JIT config comes from a scale set registers where the
		// scale set lives; any other runner follows the pool per organization.
		if j.Scope, err = s.registrationScope(ctx, j.Scope); err != nil {
			return Record{}, fmt.Errorf("resolve scope: %w", err)
		}
	}
	t, err := s.Resolver.Resolve(ctx, j.Scope)
	if err != nil {
		return Record{}, fmt.Errorf("resolve scope: %w", err)
	}
	if err := s.ClearFailedStarts(ctx, j.JobKey); err != nil {
		return Record{}, err
	}
	id := s.newID("r_")
	rec := Record{ID: id, Name: j.Name, MachineID: j.MachineID, OS: j.OS, Arch: j.Arch, Kind: "ephemeral",
		Backend: j.Backend, Isolation: string(iso), Labels: j.Labels,
		Route: RouteForLabels(s.poolFor(j.Labels), j.Labels), SizeClass: DefaultSize, Scope: j.Scope,
		RunnerVersion: pin.Version, State: Creating, Managed: true, CreatedAt: s.now()}
	if err := s.Store.InsertRunner(ctx, rec); err != nil {
		return Record{}, err
	}
	fail := func(err error) (Record, error) {
		_ = s.Store.SetRunnerState(ctx, id, Failed, err.Error())
		s.audit(ctx, actor, "runner.add_failed", j.MachineID, id, err.Error())
		return Record{}, err
	}
	var jit github.JITConfig
	if j.JIT != nil {
		jit, err = j.JIT(ctx)
	} else {
		jit, err = s.GitHub.GenerateJITConfig(ctx, t.InstallationID, t.Scope, github.JITRequest{Name: j.Name, GroupID: t.Scope.GroupID, Labels: j.Labels})
	}
	if err != nil {
		return fail(fmt.Errorf("JIT config: %w", err))
	}
	// A start that fails after GitHub made the registration leaves it behind:
	// the next try would meet 409 "already exists" (OPS89). Delete it, best effort.
	failStart := func(err error) (Record, error) {
		if j.JIT == nil && jit.RunnerID != 0 {
			if derr := s.GitHub.DeleteRunner(ctx, t.InstallationID, t.Scope, jit.RunnerID); derr != nil {
				s.audit(ctx, actor, "runner.jit_delete_failed", j.MachineID, id, derr.Error())
			}
		}
		return fail(err)
	}
	spec := backend.RunnerSpec{ID: id, Name: j.Name, OS: j.OS, Arch: j.Arch, Labels: j.Labels, GitHubURL: t.URL, Group: t.Group,
		RunnerVersion: pin.Version, RunnerSHA256: pin.SHA256, CacheQuota: s.CacheQuota, ExtraPackages: j.ExtraPackages}
	if m, err := s.Machines.GetMachine(ctx, j.MachineID); err == nil {
		// The pool size of the machine caps the pool container (R5.27). A
		// machine with no pool size set leaves the container uncapped.
		if m.PoolCPUs > 0 {
			spec.PoolCPUs = float64(m.PoolCPUs)
		}
		if m.PoolMemoryGiB > 0 {
			spec.PoolMemoryMB = int(m.PoolMemoryGiB * 1024)
		}
		spec.MaxParallel = m.MaxParallel()
	}
	if j.Scope.RepositoryID > 0 {
		spec.Pool.RepoID = strconv.FormatInt(j.Scope.RepositoryID, 10)
	}
	if s.PoolSettings != nil {
		ps, err := s.PoolSettings(ctx, j.Scope)
		if err != nil {
			return failStart(fmt.Errorf("pool settings: %w", err))
		}
		spec.Pool.Layers, spec.Pool.Database = ps.Layers, ps.Database
	}
	h, err := s.Agent.AddRunner(ctx, j.MachineID, spec, backend.StartConfig{JITConfig: jit.Encoded()})
	if err != nil {
		// The agent's error text is its own; the config is not in it by the
		// backend's rule (R6.2), and it is not added here.
		return failStart(err)
	}
	if err := s.Store.SetRunnerService(ctx, id, h.Ref, ""); err != nil {
		return Record{}, err
	}
	if err := s.Store.SetRunnerRegistration(ctx, id, jit.RunnerID, pin.Version); err != nil {
		return Record{}, err
	}
	if err := s.Store.SetRunnerState(ctx, id, Idle, "registered with a JIT config"); err != nil {
		return Record{}, err
	}
	s.audit(ctx, actor, "runner.add", j.MachineID, id, j.Name)
	rec.ServiceName, rec.GitHubRunnerID, rec.State = h.Ref, jit.RunnerID, Idle
	return rec, nil
}

// IsolationFor is how the runner of one job is isolated on each system: a
// container on Linux, a one-job process on Windows and a VM on macOS.
func IsolationFor(os string) backend.Isolation {
	switch os {
	case "windows":
		return backend.IsolationPersistent
	case "macos":
		return backend.IsolationVM
	}
	return backend.IsolationContainer
}

// StartupAllowance is how long a runner of this system takes from the start
// call to a listening runner. The plan counts it, and a runner nobody used is
// deleted after it plus two minutes (runners R5.112). A macOS runner boots a
// VM, which takes 45 to 60 s.
func StartupAllowance(os string) time.Duration {
	switch os {
	case "macos":
		return 60 * time.Second
	case "windows":
		return 30 * time.Second
	}
	return 20 * time.Second
}
