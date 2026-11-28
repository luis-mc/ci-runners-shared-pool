package main

import (
	"context"
	"path/filepath"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/agentinstallers"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/doctor"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/machines"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/update"
)

// agentInstallerSource is the machines.AgentInstallerSource of an SSH push
// (CRSP304, operations R5.130). With a console address the machine downloads
// from the controller's own LAN link (served from the cache, so it works with no
// network once cached); without one it downloads from the release.
type agentInstallerSource struct {
	store   *agentinstallers.Store
	console string
}

func (a agentInstallerSource) AgentInstaller(ctx context.Context, goos, goarch string) (machines.PackageRef, error) {
	r, err := a.store.For(ctx, goos, goarch, a.console)
	if err != nil {
		return machines.PackageRef{}, err
	}
	return machines.PackageRef{Name: r.Name, URL: r.URL, SHA256: r.SHA256, SelfSigned: r.SelfSigned}, nil
}

// newAgentInstallers makes the store of a stable release build (nil otherwise).
func newAgentInstallers(dataDir, version, githubAPI string) *agentinstallers.Store {
	if _, ok := update.ParseVersion(version); !ok {
		return nil
	}
	return &agentinstallers.Store{
		Version: version,
		Source:  update.GitHubSource{Base: githubAPI, Version: version},
		Dir:     filepath.Join(dataDir, "agent-installers"),
	}
}

// agentCoverage is the doctor.AgentPackageSource of the release's SHA256SUMS (CRSP348): which agent archives and
// native packages it lists. A nil store (a development build) leaves the check skipped.
type agentCoverage struct{ store *agentinstallers.Store }

func agentPackageStatus(s *agentinstallers.Store) doctor.AgentPackageSource {
	if s == nil {
		return nil
	}
	return agentCoverage{store: s}
}

func (a agentCoverage) AgentPackages(ctx context.Context) (doctor.AgentPackageStatus, error) {
	sums, version, err := a.store.Sums(ctx)
	if err != nil {
		return doctor.AgentPackageStatus{}, err
	}
	c := agentinstallers.CoverageOf(sums)
	return doctor.AgentPackageStatus{Version: version, MissingArchives: c.MissingArchives, MissingNative: c.MissingNative}, nil
}
