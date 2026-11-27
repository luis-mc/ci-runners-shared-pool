package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/machines"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/update"
)

// releasePackages is the machines.WindowsPackageSource of an SSH join to Windows: the
// GitHub release asset of the controller's own version, with its line from
// SHA256SUMS (CRSP123). It is the fallback when the controller's own package
// endpoint (controllerPackages) is not reachable.
type releasePackages struct{ version, apiBase string }

func (r releasePackages) WindowsPackage(ctx context.Context, arch string) (machines.PackageRef, error) {
	rel, err := update.GitHubSource{Base: r.apiBase, Version: r.version}.Latest(ctx)
	if err != nil {
		return machines.PackageRef{}, err
	}
	asset := update.PickAsset(rel, update.AssetName("windows", arch))
	url, ok := rel.Assets[asset]
	if !ok {
		return machines.PackageRef{}, fmt.Errorf("release %s has no %s", rel.Version, asset)
	}
	sumsURL, ok := rel.Assets["SHA256SUMS"]
	if !ok {
		return machines.PackageRef{}, fmt.Errorf("release %s has no SHA256SUMS", rel.Version)
	}
	hr, err := http.NewRequestWithContext(ctx, http.MethodGet, sumsURL, nil)
	if err != nil {
		return machines.PackageRef{}, err
	}
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(hr)
	if err != nil {
		return machines.PackageRef{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return machines.PackageRef{}, fmt.Errorf("SHA256SUMS answered %d", resp.StatusCode)
	}
	sums, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return machines.PackageRef{}, err
	}
	sum, ok := update.SumFor(sums, asset)
	if !ok {
		return machines.PackageRef{}, fmt.Errorf("%s is not listed in SHA256SUMS", asset)
	}
	return machines.PackageRef{Name: asset, URL: url, SHA256: sum}, nil
}

// controllerPackages is the machines.WindowsPackageSource that points the
// machine at the controller's own package endpoint (CRSP122, machines R5.65):
// the same SHA256SUMS and the same files as a downloaded installer.
type controllerPackages struct {
	sums    machines.PackageSource
	console string
}

func (c controllerPackages) WindowsPackage(ctx context.Context, arch string) (machines.PackageRef, error) {
	sums, version, err := c.sums.Sums(ctx)
	if err != nil {
		return machines.PackageRef{}, err
	}
	asset := update.PickFromSums(sums, update.AssetName("windows", arch), version)
	sum, ok := update.SumFor(sums, asset)
	if !ok {
		return machines.PackageRef{}, fmt.Errorf("%s is not listed in SHA256SUMS", asset)
	}
	return machines.PackageRef{Name: asset, URL: strings.TrimRight(c.console, "/") + "/api/v1/packages/" + asset, SHA256: sum, SelfSigned: true}, nil
}
