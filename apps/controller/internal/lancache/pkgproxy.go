package lancache

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/addresses"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// The package proxy (caches R5.74 to R5.76, R5.80) answers npm and PyPI
// clients of one repository on the controller's own listener:
//
//	/pkg/ping
//	/pkg/npm/<repository>/<token>/...
//	/pkg/pypi/<repository>/<token>/...
//	/pkg/crates/<repository>/<token>/...
//	/pkg/apt/<repository>/<token>/<host>/...
//
// It keeps what it fetched in the content store, namespace packages, scoped by
// the repository, so it counts against the hub's one budget (R5.73).

// ProxyPrefix is where the package proxy lives on the controller listener.
const ProxyPrefix = "/pkg/"

// Upstreams of the proxy. Only these hosts are fetched.
const (
	DefaultNpmUpstream   = addresses.DefaultNpmUpstream
	DefaultPypiUpstream  = addresses.DefaultPypiUpstream
	DefaultFilesUpstream = addresses.DefaultFilesUpstream
	// PublicPypiIndex is the fallback index a runner keeps (R5.76).
	PublicPypiIndex = DefaultPypiUpstream + "/simple"
)

// Freshness of a stored package document or index (R5.74, R5.75).
const docFresh = 5 * time.Minute

// docLimit is the largest package document or index the proxy reads.
const docLimit = 128 << 20

var repoKeyRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,100}$`)

// ValidRepoKey says whether a repository key is one plain path segment.
func ValidRepoKey(k string) bool { return repoKeyRe.MatchString(k) && k != "." && k != ".." }

// RepoKey turns owner/repo into the key a runner and the store use:
// lower case, with the slash written as two underscores.
func RepoKey(ownerRepo string) (string, bool) {
	owner, repo, ok := strings.Cut(strings.ToLower(strings.TrimSpace(ownerRepo)), "/")
	if !ok || owner == "" || repo == "" || strings.Contains(repo, "/") {
		return "", false
	}
	k := owner + "__" + repo
	return k, ValidRepoKey(k)
}

// Token is the proxy token of one repository: an HMAC of the key with the
// controller's secret (section 7.5).
func Token(secret []byte, repoKey string) string {
	m := hmac.New(sha256.New, secret)
	_, _ = io.WriteString(m, "pkg\n"+repoKey)
	return hex.EncodeToString(m.Sum(nil))[:32]
}

// LoadSecret reads the proxy secret from path, or makes one and keeps it with
// mode 0600 when there is none, so tokens stay valid across restarts.
func LoadSecret(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if err == nil {
		s, derr := hex.DecodeString(strings.TrimSpace(string(b)))
		if derr != nil || len(s) < 32 {
			return nil, errors.New("the package proxy secret file is damaged")
		}
		return s, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read the package proxy secret: %w", err)
	}
	s := make([]byte, 32)
	if _, err := rand.Read(s); err != nil {
		return nil, fmt.Errorf("make the package proxy secret: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("make the package proxy folder: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(hex.EncodeToString(s)), 0o600); err != nil {
		return nil, fmt.Errorf("keep the package proxy secret: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return nil, fmt.Errorf("keep the package proxy secret: %w", err)
	}
	return s, nil
}

// PrivateRemote says whether a request comes from a private, link-local or
// loopback address (R5.80).
func PrivateRemote(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip, err := netip.ParseAddr(strings.Trim(host, "[]"))
	if err != nil {
		return false
	}
	ip = ip.Unmap()
	return ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast()
}

// Proxy is the npm and PyPI proxy. Set Store and Secret; the rest has
// defaults.
type Proxy struct {
	Store  *Store
	Secret []byte
	// On says whether the package_proxy setting is on (R5.77). nil means on.
	On func() bool
	// AfterPut runs after each stored package, to run the budget pass.
	AfterPut func()

	NpmUpstream   string
	PypiUpstream  string
	FilesUpstream string
	// CratesIndexUpstream and CratesFilesUpstream are the crates.io sparse
	// index and the crate download host (R5.81).
	CratesIndexUpstream string
	CratesFilesUpstream string
	// AptUpstreams maps an allowed apt host to its upstream base address
	// (R5.82); nil means AptHosts over https.
	AptUpstreams map[string]string
	HTTP         *http.Client
	Now          func() time.Time
	// Remote decides whether a caller's address may use the proxy; nil
	// allows private addresses only.
	Remote func(remoteAddr string) bool

	mu      sync.Mutex
	fetched map[string]time.Time
	stats   Stats
}

func (p *Proxy) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

func (p *Proxy) client() *http.Client {
	if p.HTTP != nil {
		return p.HTTP
	}
	return &http.Client{Timeout: 10 * time.Minute}
}

func (p *Proxy) on() bool { return p.On == nil || p.On() }

func or(v, d string) string {
	if v == "" {
		return d
	}
	return strings.TrimRight(v, "/")
}

func (p *Proxy) npmUp() string  { return or(p.NpmUpstream, DefaultNpmUpstream) }
func (p *Proxy) pypiUp() string { return or(p.PypiUpstream, DefaultPypiUpstream) }
func (p *Proxy) cratesIndexUp() string {
	return or(p.CratesIndexUpstream, addresses.DefaultCratesIndexUpstream)
}
func (p *Proxy) cratesFilesUp() string {
	return or(p.CratesFilesUpstream, addresses.DefaultCratesFilesUpstream)
}
func (p *Proxy) filesUp() string { return or(p.FilesUpstream, DefaultFilesUpstream) }

// Stats are the proxy's hits, misses and bytes served per repository key.
func (p *Proxy) Stats() *Stats { return &p.stats }

// RepoStats lists the proxy numbers per repository key.
func (p *Proxy) RepoStats() []RepoStats {
	p.stats.mu.Lock()
	defer p.stats.mu.Unlock()
	out := make([]RepoStats, 0, len(p.stats.m))
	for r, c := range p.stats.m {
		out = append(out, RepoStats{Repo: r, Hits: c.hits, Misses: c.misses, BytesServed: c.bytes, LastHit: c.lastHit})
	}
	return out
}

// Handler serves ProxyPrefix.
func (p *Proxy) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		allow := p.Remote
		if allow == nil {
			allow = PrivateRemote
		}
		if !allow(r.RemoteAddr) {
			http.Error(w, "the package proxy answers the local network only", http.StatusForbidden)
			return
		}
		if !p.on() {
			http.Error(w, "the package proxy is off", http.StatusNotFound)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		rest := strings.TrimPrefix(r.URL.EscapedPath(), ProxyPrefix)
		if rest == "ping" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, "{}\n")
			return
		}
		parts := strings.SplitN(rest, "/", 4)
		if len(parts) < 3 || (parts[0] != "npm" && parts[0] != "pypi" && parts[0] != "crates" && parts[0] != "apt") {
			http.NotFound(w, r)
			return
		}
		kind, repo, token := parts[0], parts[1], parts[2]
		if !ValidRepoKey(repo) || !hmac.Equal([]byte(token), []byte(Token(p.Secret, repo))) {
			http.Error(w, "this address is not the package proxy of a repository", http.StatusUnauthorized)
			return
		}
		sub := ""
		if len(parts) == 4 {
			sub = parts[3]
		}
		base := scheme(r) + "://" + r.Host + ProxyPrefix + kind + "/" + repo + "/" + token
		switch kind {
		case "npm":
			p.npm(w, r, repo, base, sub)
		case "crates":
			p.crates(w, r, repo, base, sub)
		case "apt":
			p.apt(w, r, repo, sub)
		default:
			p.pypi(w, r, repo, base, sub)
		}
	})
}

func scheme(r *http.Request) string {
	if r.TLS != nil {
		return "https"
	}
	return "http"
}

// errNotFound is an upstream 404: the package does not exist.
var errNotFound = errors.New("not found upstream")

// errUpstream is an upstream that could not be reached or failed.
var errUpstream = errors.New("the upstream registry could not be reached")

// fetch GETs an upstream address with an Accept header and returns the body,
// read up to limit bytes.
func (p *Proxy) fetch(ctx context.Context, u, accept string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	resp, err := p.client().Do(req)
	if err != nil {
		return nil, errUpstream
	}
	defer func() { _ = resp.Body.Close() }()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, errNotFound
	case resp.StatusCode != http.StatusOK:
		return nil, errUpstream
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, errUpstream
	}
	if int64(len(b)) > limit {
		return nil, errUpstream
	}
	return b, nil
}

// document returns a package document or index: fresh from the store, else
// from the upstream (then stored), else the stored one when the upstream
// cannot be reached (stale-serve).
func (p *Proxy) document(ctx context.Context, repo, name, u, accept string, count bool) ([]byte, error) {
	key, err := Name(NSPackages, repo, name)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	at, seen := p.fetched[key]
	p.mu.Unlock()
	if seen && p.now().Sub(at) < docFresh {
		if b, ok := p.read(key); ok {
			if count {
				p.stats.Hit(repo, p.now())
			}
			return b, nil
		}
	}
	b, err := p.fetch(ctx, u, accept, docLimit)
	if err != nil {
		if errors.Is(err, errUpstream) {
			if old, ok := p.read(key); ok {
				if count {
					p.stats.Hit(repo, p.now())
				}
				return old, nil
			}
		}
		if count {
			p.stats.Miss(repo)
		}
		return nil, err
	}
	if count {
		p.stats.Miss(repo)
	}
	if _, perr := p.Store.Put(key, "", strings.NewReader(string(b))); perr == nil {
		p.mu.Lock()
		if p.fetched == nil {
			p.fetched = map[string]time.Time{}
		}
		p.fetched[key] = p.now()
		p.mu.Unlock()
		p.afterPut()
	}
	return b, nil
}

func (p *Proxy) afterPut() {
	if p.AfterPut != nil {
		p.AfterPut()
	}
}

// read returns a stored entry's bytes (checked by the store).
func (p *Proxy) read(key string) ([]byte, bool) {
	f, _, err := p.Store.Get(key)
	if err != nil {
		return nil, false
	}
	defer func() { _ = f.Close() }()
	b, err := io.ReadAll(f)
	return b, err == nil
}

// serveStored sends a stored file and counts it.
func (p *Proxy) serveStored(w http.ResponseWriter, r *http.Request, repo, key, ctype string) bool {
	f, e, err := p.Store.Get(key)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	if ctype != "" {
		w.Header().Set("Content-Type", ctype)
	}
	if r.Method == http.MethodGet {
		p.stats.Served(repo, e.Size)
	}
	http.ServeContent(w, r, "", time.Time{}, f)
	return true
}

// storeFile downloads an upstream file into the store under key. The store
// checks the SHA-256 (want) and, when extra is set, a second hash.
func (p *Proxy) storeFile(ctx context.Context, key, u, want string, check func() (hashCheck, bool)) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	resp, err := p.client().Do(req)
	if err != nil {
		return errUpstream
	}
	defer func() { _ = resp.Body.Close() }()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return errNotFound
	case resp.StatusCode != http.StatusOK:
		return errUpstream
	}
	if check != nil {
		hc, ok := check()
		if !ok {
			return ErrDigestMismatch
		}
		_, err = p.Store.PutChecked(key, want, resp.Body, hc.h, hc.want)
	} else {
		_, err = p.Store.Put(key, want, resp.Body)
	}
	if err != nil {
		return err
	}
	p.afterPut()
	return nil
}

// fail answers an upstream or hash failure.
func fail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errNotFound):
		http.Error(w, "not found", http.StatusNotFound)
	case errors.Is(err, ErrDigestMismatch):
		http.Error(w, "the file does not match its published hash; it was not kept", http.StatusBadGateway)
	case errors.Is(err, ErrTooLarge):
		http.Error(w, "the file is larger than the cache budget", http.StatusBadGateway)
	case errors.Is(err, ErrBadName):
		http.Error(w, "bad package name", http.StatusBadRequest)
	default:
		http.Error(w, "the upstream registry could not be reached", http.StatusBadGateway)
	}
}
