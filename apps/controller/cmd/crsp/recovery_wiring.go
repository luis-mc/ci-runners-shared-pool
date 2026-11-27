package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/agent"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/machines"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/recovery"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/tlsca"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/update"
)

// minAgentProtocol is the lowest agent link protocol a Session accepts
// (operations R5.44): version 2 carries the runner file swap.
const minAgentProtocol = 2

// mountRecovery serves the frozen recovery call on the agent link: a signed
// update target for a machine that needs recovery and that the owner armed.
func mountRecovery(svc *machines.Service, ca *tlsca.Authority, apiBase, controllerVersion string) {
	h := &recovery.Handler{
		Authenticate: func(ctx context.Context, key, id string) (string, error) {
			m, err := svc.Authenticate(ctx, key, id)
			return m.ID, err
		},
		Armed:  func(ctx context.Context, id, _ string) bool { return svc.RecoveryArmed(ctx, id) },
		Signer: ca,
		Target: releaseTarget(apiBase, controllerVersion),
		Served: func(ctx context.Context, id string, t recovery.Target) { svc.RecoveryServed(ctx, id, t.Version) },
	}
	svc.SetRecoveryHandler(h)
}

// releaseTarget finds the controller's own release for the agent's OS and arch
// and reads the SHA-256 of its asset from SHA256SUMS.
func releaseTarget(apiBase, version string) func(ctx context.Context, req recovery.Request) (recovery.Target, error) {
	return func(ctx context.Context, req recovery.Request) (recovery.Target, error) {
		rel, err := update.GitHubSource{Base: apiBase, Version: version}.Latest(ctx)
		if err != nil {
			return recovery.Target{}, err
		}
		sumsURL, ok := rel.Assets["SHA256SUMS"]
		if !ok {
			return recovery.Target{}, fmt.Errorf("release %s has no SHA256SUMS", rel.Version)
		}
		hr, err := http.NewRequestWithContext(ctx, http.MethodGet, sumsURL, nil)
		if err != nil {
			return recovery.Target{}, err
		}
		resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(hr)
		if err != nil {
			return recovery.Target{}, err
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			return recovery.Target{}, fmt.Errorf("SHA256SUMS answered %d", resp.StatusCode)
		}
		sums, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		if err != nil {
			return recovery.Target{}, err
		}
		asset := update.PickFromSums(sums, update.AssetName(req.OS, req.Arch), rel.Version)
		sum, ok := update.SumFor(sums, asset)
		if !ok {
			return recovery.Target{}, fmt.Errorf("%s is not listed in SHA256SUMS", asset)
		}
		return recovery.Target{Version: rel.Version, Asset: asset, URL: rel.Assets[asset], SHA256: sum}, nil
	}
}

// agentRecoverer installs a signed recovery target through the agent's update
// manager, which downloads, checks SHA-256 and attestation, drains and applies.
// The manager exists once the agent's gate does (agent.Config.UpdaterFor).
type agentRecoverer struct {
	mu sync.Mutex
	up agent.Updater
}

func (r *agentRecoverer) set(u agent.Updater) { r.mu.Lock(); r.up = u; r.mu.Unlock() }

func (r *agentRecoverer) Recover(ctx context.Context, t recovery.Target) error {
	r.mu.Lock()
	up := r.up
	r.mu.Unlock()
	if up == nil {
		return errors.New("the update manager of this agent is not ready")
	}
	_, err := up.Follow(ctx, t.Version)
	if errors.Is(err, update.ErrNoUpdate) {
		return nil
	}
	return err
}
