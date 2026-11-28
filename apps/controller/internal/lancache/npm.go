package lancache

import (
	"bytes"
	"crypto/sha1" //nolint:gosec // npm's old dist.shasum is SHA-1; it is checked, never trusted alone for new packages
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"hash"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
)

// The npm part of the package proxy (caches R5.74).

const (
	npmCorgi       = "application/vnd.npm.install-v1+json"
	npmCorgiAccept = npmCorgi + "; q=1.0, application/json; q=0.8, */*"
)

var npmNameRe = regexp.MustCompile(`^(@[a-z0-9][a-z0-9._~-]*/)?[a-z0-9_][a-z0-9._~-]*$`)

// hashCheck is a second hash a stored file must match.
type hashCheck struct {
	h    hash.Hash
	want []byte
}

// npmName unescapes and checks a package name from the request path.
func npmName(escaped string) (string, bool) {
	n, err := url.PathUnescape(escaped)
	if err != nil {
		return "", false
	}
	n = strings.ToLower(n)
	return n, len(n) <= 214 && npmNameRe.MatchString(n)
}

// npmDist is the part of a package document the proxy reads.
type npmDoc struct {
	Versions map[string]struct {
		Dist struct {
			Tarball   string `json:"tarball"`
			Integrity string `json:"integrity"`
			Shasum    string `json:"shasum"`
		} `json:"dist"`
	} `json:"versions"`
}

func (p *Proxy) npm(w http.ResponseWriter, r *http.Request, repo, base, sub string) {
	if sub == "" || sub == "-/ping" {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("{}\n"))
		return
	}
	if strings.HasPrefix(sub, "-/") {
		// Search, audit and the other registry services are not proxied.
		http.NotFound(w, r)
		return
	}
	if pkg, file, ok := strings.Cut(sub, "/-/"); ok {
		p.npmTarball(w, r, repo, pkg, file)
		return
	}
	name, ok := npmName(sub)
	if !ok {
		http.Error(w, "bad package name", http.StatusBadRequest)
		return
	}
	corgi := strings.Contains(r.Header.Get("Accept"), npmCorgi)
	form, accept, ctype := "full", "application/json", "application/json"
	if corgi {
		form, accept, ctype = "corgi", npmCorgiAccept, npmCorgi
	}
	b, err := p.document(r.Context(), repo, "npm/doc/"+form+"/"+name, p.npmUp()+"/"+url.PathEscape(name), accept, true)
	if err != nil {
		fail(w, err)
		return
	}
	w.Header().Set("Content-Type", ctype)
	_, _ = w.Write(p.npmRewrite(b, base))
}

// npmRewrite points every tarball address of the upstream at the hub.
func (p *Proxy) npmRewrite(doc []byte, base string) []byte {
	re := regexp.MustCompile(`("tarball"\s*:\s*")` + regexp.QuoteMeta(p.npmUp()) + `/`)
	return re.ReplaceAll(doc, []byte("${1}"+strings.ReplaceAll(base, "$", "$$")+"/"))
}

func (p *Proxy) npmTarball(w http.ResponseWriter, r *http.Request, repo, escapedPkg, file string) {
	name, ok := npmName(escapedPkg)
	if !ok || file == "" || strings.ContainsAny(file, "/\\") || !strings.HasSuffix(file, ".tgz") || strings.HasPrefix(file, ".") {
		http.Error(w, "bad package file", http.StatusBadRequest)
		return
	}
	key, err := Name(NSPackages, repo, "npm/tgz/"+name+"/"+file)
	if err != nil {
		fail(w, err)
		return
	}
	if p.serveStored(w, r, repo, key, "application/octet-stream") {
		p.stats.Hit(repo, p.now())
		return
	}
	p.stats.Miss(repo)
	// The hash comes from the package document (stored or fetched).
	doc, err := p.document(r.Context(), repo, "npm/doc/corgi/"+name, p.npmUp()+"/"+url.PathEscape(name), npmCorgiAccept, false)
	if err != nil {
		fail(w, err)
		return
	}
	check, ok := npmCheckFor(doc, file)
	if !ok {
		// No published hash: never stored, never served from here.
		http.Error(w, "the package has no published hash for this file", http.StatusBadGateway)
		return
	}
	u := p.npmUp() + "/" + url.PathEscape(name) + "/-/" + url.PathEscape(file)
	if err := p.storeFile(r.Context(), key, u, "", func() (hashCheck, bool) { return check, true }); err != nil {
		fail(w, err)
		return
	}
	if !p.serveStored(w, r, repo, key, "application/octet-stream") {
		http.Error(w, "the file could not be read back", http.StatusBadGateway)
	}
}

// npmCheckFor finds the version whose tarball is file and returns its hash:
// the SHA-512 integrity, else the SHA-1 shasum.
func npmCheckFor(doc []byte, file string) (hashCheck, bool) {
	var d npmDoc
	if err := json.NewDecoder(bytes.NewReader(doc)).Decode(&d); err != nil {
		return hashCheck{}, false
	}
	for _, v := range d.Versions {
		if v.Dist.Tarball == "" || path.Base(v.Dist.Tarball) != file {
			continue
		}
		for _, sri := range strings.Fields(v.Dist.Integrity) {
			if b64, ok := strings.CutPrefix(sri, "sha512-"); ok {
				if want, err := base64.StdEncoding.DecodeString(b64); err == nil && len(want) == sha512.Size {
					return hashCheck{h: sha512.New(), want: want}, true
				}
			}
		}
		if want, err := hex.DecodeString(v.Dist.Shasum); err == nil && len(want) == sha1.Size {
			return hashCheck{h: sha1.New(), want: want}, true //nolint:gosec // see import
		}
		return hashCheck{}, false
	}
	return hashCheck{}, false
}
