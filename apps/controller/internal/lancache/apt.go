package lancache

import (
	"net/http"
	"regexp"
	"strings"
	"time"
)

// The apt part of the package proxy (caches R5.82). It is a path mirror, not
// a forward proxy: the hub is TLS only, and apt cannot speak TLS to a proxy.
//
//	/pkg/apt/<repository>/<token>/<host>/<path>
//
// serves <path> of one allowed distribution host. Release and index files
// (under dists/, not by-hash) are mutable: fetched again after 5 minutes,
// served stale when the upstream is down. Everything else (pool/, by-hash/)
// never changes under its name and is kept as it came. apt checks every file
// against the signed Release itself, so the hub does not need a hash.

// AptHosts are the distribution hosts the hub fetches (R5.80).
var AptHosts = []string{
	"archive.ubuntu.com", "security.ubuntu.com", "ports.ubuntu.com",
	"deb.debian.org", "security.debian.org",
}

// aptSegRe is one plain path segment of an apt file name.
var aptSegRe = regexp.MustCompile(`^[A-Za-z0-9._~+:%@=,-]{1,255}$`)

func (p *Proxy) aptUp(host string) (string, bool) {
	if p.AptUpstreams != nil {
		u, ok := p.AptUpstreams[host]
		return strings.TrimRight(u, "/"), ok
	}
	for _, h := range AptHosts {
		if h == host {
			return "https://" + h, true
		}
	}
	return "", false
}

func aptMutable(path string) bool {
	return strings.Contains(path, "/dists/") || strings.HasPrefix(path, "dists/")
}

func (p *Proxy) apt(w http.ResponseWriter, r *http.Request, repo, sub string) {
	host, path, ok := strings.Cut(sub, "/")
	up, allowed := p.aptUp(host)
	if !ok || !allowed {
		http.Error(w, "this host is not one the apt proxy fetches", http.StatusNotFound)
		return
	}
	for _, seg := range strings.Split(path, "/") {
		if !aptSegRe.MatchString(seg) || seg == "." || seg == ".." {
			http.Error(w, "bad apt path", http.StatusBadRequest)
			return
		}
	}
	name := "apt/" + host + "/" + path
	if aptMutable(path) && !strings.Contains(path, "/by-hash/") {
		b, err := p.document(r.Context(), repo, name, up+"/"+path, "", true)
		if err != nil {
			fail(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		http.ServeContent(w, r, "", time.Time{}, strings.NewReader(string(b)))
		return
	}
	key, err := Name(NSPackages, repo, name)
	if err != nil {
		fail(w, err)
		return
	}
	if p.serveStored(w, r, repo, key, "application/octet-stream") {
		p.stats.Hit(repo, p.now())
		return
	}
	p.stats.Miss(repo)
	if err := p.storeFile(r.Context(), key, up+"/"+path, "", nil); err != nil {
		fail(w, err)
		return
	}
	if !p.serveStored(w, r, repo, key, "application/octet-stream") {
		http.Error(w, "the file could not be read back", http.StatusBadGateway)
	}
}

// aptSourceRe matches an upstream address of an allowed host, or an earlier
// hub address, in a sources file.
func aptSourceRe(hubHost string) *regexp.Regexp {
	hosts := make([]string, len(AptHosts))
	for i, h := range AptHosts {
		hosts[i] = regexp.QuoteMeta(h)
	}
	return regexp.MustCompile(`(?:https?://(` + strings.Join(hosts, "|") + `)/|https://` + regexp.QuoteMeta(hubHost) + `/pkg/apt/[A-Za-z0-9._-]+/[0-9a-f]+/(` + strings.Join(hosts, "|") + `)/)`)
}

// AptSources points every upstream address of an allowed host in a sources
// file (a one-line .list or a deb822 .sources) at the hub. mirror is
// https://<hub>/pkg/apt/<repository>/<token> without a trailing slash. The
// result is the same for text that already points at this hub, and an earlier
// token is replaced.
func AptSources(text, hubHost, mirror string) string {
	re := aptSourceRe(hubHost)
	mirror = strings.TrimRight(mirror, "/")
	return re.ReplaceAllStringFunc(text, func(m string) string {
		sm := re.FindStringSubmatch(m)
		host := sm[1]
		if host == "" {
			host = sm[2]
		}
		return mirror + "/" + host + "/"
	})
}

// AptConf is the apt.conf.d snippet that makes apt trust the hub's CA for the
// hub host only.
func AptConf(hubHost, caFile string) string {
	return "// Written by crsp: the package proxy of the pool (caches R5.82).\n" +
		"Acquire::https::" + hubHost + "::CaInfo \"" + caFile + "\";\n"
}

// CargoConfig is the Cargo config that replaces crates.io with the hub's
// sparse index (caches R5.81). The first line marks it as ours.
const CargoConfigMarker = "# Written by crsp: the package proxy of the pool (caches R5.81)."

func CargoConfig(indexURL, caFile string) string {
	b := CargoConfigMarker + "\n" +
		"[source.crates-io]\nreplace-with = \"crsp-hub\"\n\n" +
		"[source.crsp-hub]\nregistry = \"sparse+" + strings.TrimRight(indexURL, "/") + "/\"\n"
	if caFile != "" {
		b += "\n[http]\ncainfo = \"" + caFile + "\"\n"
	}
	return b
}
