package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/addresses"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/agentinstallers"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/depsetup"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/depstore"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runners"
)

// depFetchMax is the largest file the controller keeps (Docker Desktop is about 600 MB).
const depFetchMax = 2 << 30

// newDepStore makes the controller's dependency store (dependency-setup DEP15):
// what a machine without internet installs from, fetched, checked and kept here.
func newDepStore(dataDir string, addr addresses.Set, agents *agentinstallers.Store) *depstore.Service {
	pinned := depsetup.MustLoad()
	allowed := map[string]bool{
		"download.docker.com": true, "desktop.docker.com": true,
		"github.com": true, "objects.githubusercontent.com": true, "release-assets.githubusercontent.com": true,
	}
	// A mirror of the runner package (CRSP_RUNNER_DOWNLOAD_URL) is the owner's choice too.
	if u, err := url.Parse(addr.RunnerDownloadURL); err == nil && u.Hostname() != "" {
		allowed[strings.ToLower(u.Hostname())] = true
	}
	st := &depstore.Store{
		Dir: filepath.Join(dataDir, "dependencies"),
		Catalog: func(context.Context) []depstore.Spec {
			return depCatalog(pinned, addr)
		},
		Fetch: depFetch,
		AllowURL: func(raw string) bool {
			u, err := url.Parse(raw)
			return err == nil && u.Scheme == "https" && allowed[strings.ToLower(u.Hostname())]
		},
	}
	svc := &depstore.Service{Store: st}
	if agents != nil {
		svc.Agents = agentPackages{s: agents}
	}
	return svc
}

// depCatalog lists what this release may keep: the pinned downloads and the runner packages.
func depCatalog(pinned depsetup.List, addr addresses.Set) []depstore.Spec {
	var out []depstore.Spec
	for _, p := range pinned.Items {
		if p.Method != depsetup.MethodDownload {
			continue
		}
		out = append(out, depstore.Spec{
			ID: p.ID + "-" + p.OS, Name: p.Name, Kind: depstore.KindInstaller, OS: p.OS, Version: p.Version,
			Source: p.Source, URL: p.URL, SHA256: strings.ToLower(p.SHA256), Bytes: p.DownloadBytes,
		})
	}
	for _, p := range runners.AllPins() {
		out = append(out, depstore.Spec{
			ID: "runner-" + p.OS + "-" + p.Arch, Name: "GitHub Actions runner", Kind: depstore.KindRunner, OS: p.OS, Arch: p.Arch,
			Version: p.Version, Source: "github.com", URL: p.DownloadURL(addr), SHA256: strings.ToLower(p.SHA256),
		})
	}
	return out
}

// depFetch downloads url to dst over https; a redirect must stay on https.
func depFetch(ctx context.Context, rawURL, dst string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "crsp-controller")
	client := &http.Client{CheckRedirect: func(r *http.Request, _ []*http.Request) error {
		if r.URL.Scheme != "https" {
			return errors.New("a redirect to a page that is not https was refused")
		}
		return nil
	}}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("the server answered %d", resp.StatusCode)
	}
	f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600) //nolint:gosec // the controller's data folder
	if err != nil {
		return err
	}
	n, err := io.Copy(f, io.LimitReader(resp.Body, depFetchMax+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && n > depFetchMax {
		err = errors.New("the download is larger than 2 GiB")
	}
	return err
}

// agentPackages adapts the agent installer store to depstore.AgentPackages.
type agentPackages struct{ s *agentinstallers.Store }

func (a agentPackages) List(ctx context.Context) ([]depstore.AgentInstaller, error) {
	list, err := a.s.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]depstore.AgentInstaller, 0, len(list))
	for _, i := range list {
		out = append(out, depstore.AgentInstaller{Name: i.Name, SHA256: i.SHA256, OS: i.OS, Arch: i.Arch, Cached: i.Cached})
	}
	return out, nil
}

func (a agentPackages) Fetch(ctx context.Context, name string) error {
	f, _, err := a.s.Open(ctx, name)
	if err != nil {
		return err
	}
	return f.Close()
}

func (a agentPackages) Import(ctx context.Context, name string, r io.Reader) error {
	return a.s.Import(ctx, name, r)
}
