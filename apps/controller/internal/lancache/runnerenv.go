package lancache

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Names of the values a runner gets to use the package proxy (caches R5.76).
const (
	EnvNpmRegistry   = "npm_config_registry"
	EnvPipIndex      = "PIP_INDEX_URL"
	EnvPipExtraIndex = "PIP_EXTRA_INDEX_URL"
	EnvPipCert       = "PIP_CERT"
	EnvNodeExtraCA   = "NODE_EXTRA_CA_CERTS"
	// Crates and apt (R5.81, R5.82). Cargo reads the protocol and the CA from
	// its own variables; its source replacement can only come from a config
	// file, so the job-started hook writes that from EnvCargoIndex. The same
	// hook points apt at EnvAptMirror and trusts the hub CA in EnvHubCA.
	EnvCargoProtocol = "CARGO_REGISTRIES_CRATES_IO_PROTOCOL"
	EnvCargoCAInfo   = "CARGO_HTTP_CAINFO"
	EnvCargoIndex    = "CRSP_HUB_CARGO_INDEX"
	EnvAptMirror     = "CRSP_HUB_APT_MIRROR"
	EnvHubCA         = "CRSP_HUB_CA"
)

// ProxyGrant is what the controller gives a runner of one repository: the
// repository key and its token. The zero value asks for no proxy.
type ProxyGrant struct {
	Repo  string
	Token string
}

// On says whether the grant names a repository and a token.
func (g ProxyGrant) On() bool { return ValidRepoKey(g.Repo) && g.Token != "" }

// RunnerEnv is the environment of a runner that uses the hub at base (like
// https://192.168.1.20:8460). caFile is the controller CA; bundleFile is the
// host's public bundle plus that CA. npm and apt get the hub only; pip gets the hub
// with the public index as its fallback, and cargo gets the sparse index; both
// only when a bundle file exists, since PIP_CERT and CARGO_HTTP_CAINFO replace
// the whole trust list. Without a CA file the runner
// could not trust the hub, so it gets nothing.
func RunnerEnv(base string, g ProxyGrant, caFile, bundleFile string) map[string]string {
	if !g.On() || caFile == "" || base == "" {
		return nil
	}
	root := strings.TrimRight(base, "/") + ProxyPrefix
	env := map[string]string{
		EnvNpmRegistry: root + "npm/" + g.Repo + "/" + g.Token + "/",
		EnvNodeExtraCA: caFile,
		EnvAptMirror:   root + "apt/" + g.Repo + "/" + g.Token,
		EnvHubCA:       caFile,
	}
	if bundleFile != "" {
		env[EnvPipIndex] = root + "pypi/" + g.Repo + "/" + g.Token + "/simple/"
		env[EnvPipExtraIndex] = PublicPypiIndex
		env[EnvPipCert] = bundleFile
		// Cargo's CA setting replaces its whole trust list, like pip's.
		env[EnvCargoProtocol] = "sparse"
		env[EnvCargoCAInfo] = bundleFile
		env[EnvCargoIndex] = root + "crates/" + g.Repo + "/" + g.Token + "/"
	}
	return env
}

// Probe asks the hub whether it answers (GET /pkg/ping). A hub that failed is
// not asked again for 30 s: it is a miss until then (the client rule of
// R5.50). It is safe for concurrent use.
type Probe struct {
	HTTP *http.Client
	Now  func() time.Time

	mu        sync.Mutex
	downUntil map[string]time.Time
}

func (p *Probe) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

// Up reports whether the hub at base answered with 200 now.
func (p *Probe) Up(ctx context.Context, base string) bool {
	p.mu.Lock()
	if p.now().Before(p.downUntil[base]) {
		p.mu.Unlock()
		return false
	}
	p.mu.Unlock()
	ok := p.ask(ctx, base)
	if !ok {
		p.mu.Lock()
		if p.downUntil == nil {
			p.downUntil = map[string]time.Time{}
		}
		p.downUntil[base] = p.now().Add(downFor)
		p.mu.Unlock()
	}
	return ok
}

func (p *Probe) ask(ctx context.Context, base string) bool {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+ProxyPrefix+"ping", nil)
	if err != nil {
		return false
	}
	hc := p.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}
