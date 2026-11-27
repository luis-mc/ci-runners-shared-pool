package main

import (
	"context"
	"errors"
	"fmt"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/addresses"
	"io"
	"net/http"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/agent"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/agentlink"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend/registry"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/machines"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runners"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runnerswap"
)

// agentRunnerFiles lets an agent replace the files of the persistent runners of
// its own machine when the controller sends UpdateRunnerFiles (runners R5.38).
// The folder is found from the OS service list, never from the message.
func agentRunnerFiles() *agent.RunnerFiles {
	reg := registry.Platform(realDeps(func(string, string) {}, secretRedactor.Redact, secretRedactor.Add))
	b, ok := reg.Get(backend.Persistent)
	if !ok {
		return nil
	}
	f, ok := b.(backend.Finder)
	if !ok {
		return nil
	}
	return &agent.RunnerFiles{
		Folder: func(ctx context.Context, name, service string) (string, error) {
			found, err := f.Find(ctx)
			if err != nil {
				return "", err
			}
			for _, r := range found {
				if r.Managed && r.Folder != "" && r.ServiceName == service && r.Name == name {
					return r.Folder, nil
				}
			}
			return "", fmt.Errorf("no runner %s made by CRSP with service %s on this machine", name, service)
		},
		Swapper: &runnerswap.Swapper{Host: runnerswap.ServiceHost{}},
	}
}

// pinBundles fetches the official runner archive of the pin for a machine's OS
// and arch. The updater checks its SHA-256 before it sends it.
type pinBundles struct {
	addr   addresses.Set
	svc    *machines.Service
	client *http.Client
}

var _ agentlink.BundleSource = pinBundles{}

func (p pinBundles) Bundle(ctx context.Context, machineID string, req runners.UpdateRequest) ([]byte, error) {
	v, err := p.svc.Get(ctx, machineID)
	if err != nil {
		return nil, err
	}
	pin, err := runners.PinFor(v.Machine.OS, v.Machine.Arch)
	if err != nil {
		return nil, err
	}
	if pin.Version != req.Version || pin.SHA256 != req.SHA256 {
		return nil, errors.New("the update asks for a runner version that is not the pin of this machine")
	}
	hr, err := http.NewRequestWithContext(ctx, http.MethodGet, pin.DownloadURL(p.addr), nil)
	if err != nil {
		return nil, err
	}
	c := p.client
	if c == nil {
		c = &http.Client{Timeout: 10 * time.Minute}
	}
	resp, err := c.Do(hr) //nolint:gosec // the URL is built from the pin table of this binary
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("the runner download answered %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, agentlink.MaxBundle+1))
	if err != nil {
		return nil, err
	}
	return data, nil
}
