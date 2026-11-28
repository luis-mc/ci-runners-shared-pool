package lancache

import (
	"bytes"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
)

// The crates.io part of the package proxy (caches R5.81): a sparse-index
// registry Cargo reads with source replacement. Paths under the repository
// token:
//
//	config.json                      the hub's own config (dl points at the hub)
//	<sparse path>                    the index file of one crate (ab/cd/abcd)
//	-/dl/<crate>/<version>/download  the .crate file

var crateNameRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// crateIndexPath checks a sparse-index path (1/a, 2/ab, 3/a/abc, ab/cd/abcd...)
// and returns the crate name it holds.
func crateIndexPath(rest string) (string, bool) {
	segs := strings.Split(rest, "/")
	name := segs[len(segs)-1]
	if !crateNameRe.MatchString(name) {
		return "", false
	}
	lower := strings.ToLower(name)
	return lower, rest == crateSparsePath(lower)
}

// crateSparsePath is the sparse-index path of a lower-case crate name.
func crateSparsePath(name string) string {
	switch len(name) {
	case 1:
		return "1/" + name
	case 2:
		return "2/" + name
	case 3:
		return "3/" + name[:1] + "/" + name
	}
	return name[:2] + "/" + name[2:4] + "/" + name
}

func (p *Proxy) crates(w http.ResponseWriter, r *http.Request, repo, base, sub string) {
	switch {
	case sub == "config.json":
		w.Header().Set("Content-Type", "application/json")
		b, _ := json.Marshal(map[string]string{"dl": base + "/-/dl"})
		_, _ = w.Write(append(b, '\n'))
	case strings.HasPrefix(sub, "-/dl/"):
		parts := strings.Split(strings.TrimPrefix(sub, "-/dl/"), "/")
		if len(parts) != 3 || parts[2] != "download" || !crateNameRe.MatchString(parts[0]) || !crateVersionRe.MatchString(parts[1]) {
			http.Error(w, "bad crate address", http.StatusBadRequest)
			return
		}
		p.crateFile(w, r, repo, strings.ToLower(parts[0]), parts[1])
	default:
		if _, ok := crateIndexPath(sub); !ok {
			http.Error(w, "bad index path", http.StatusBadRequest)
			return
		}
		b, err := p.document(r.Context(), repo, "crates/index/"+sub, p.cratesIndexUp()+"/"+sub, "", true)
		if err != nil {
			fail(w, err)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write(b)
	}
}

var crateVersionRe = regexp.MustCompile(`^[0-9A-Za-z.+_-]{1,128}$`)

// crateChecksum finds the SHA-256 the index names for one version.
func crateChecksum(index []byte, version string) (string, bool) {
	for _, line := range bytes.Split(index, []byte("\n")) {
		var e struct {
			Vers  string `json:"vers"`
			Cksum string `json:"cksum"`
		}
		if json.Unmarshal(line, &e) == nil && e.Vers == version && validDigest(e.Cksum) {
			return e.Cksum, true
		}
	}
	return "", false
}

func (p *Proxy) crateFile(w http.ResponseWriter, r *http.Request, repo, name, version string) {
	key, err := Name(NSPackages, repo, "crates/file/"+name+"/"+version)
	if err != nil {
		fail(w, err)
		return
	}
	if p.serveStored(w, r, repo, key, "application/octet-stream") {
		p.stats.Hit(repo, p.now())
		return
	}
	p.stats.Miss(repo)
	idx := crateSparsePath(name)
	doc, err := p.document(r.Context(), repo, "crates/index/"+idx, p.cratesIndexUp()+"/"+idx, "", false)
	if err != nil {
		fail(w, err)
		return
	}
	sum, ok := crateChecksum(doc, version)
	if !ok {
		http.Error(w, "the index has no checksum for this version", http.StatusBadGateway)
		return
	}
	u := p.cratesFilesUp() + "/" + name + "/" + name + "-" + version + ".crate"
	if err := p.storeFile(r.Context(), key, u, sum, nil); err != nil {
		fail(w, err)
		return
	}
	if !p.serveStored(w, r, repo, key, "application/octet-stream") {
		http.Error(w, "the file could not be read back", http.StatusBadGateway)
	}
}
