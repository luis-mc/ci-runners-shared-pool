package lancache

import (
	"bytes"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
)

// The PyPI part of the package proxy (caches R5.75).

const (
	pypiJSON       = "application/vnd.pypi.simple.v1+json"
	pypiJSONAccept = pypiJSON + ", text/html; q=0.01"
	pypiHTMLAccept = "text/html"
)

var (
	pypiSepRe     = regexp.MustCompile(`[-_.]+`)
	pypiProjectRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)
)

// pypiProject normalizes a project name (PEP 503) and checks it.
func pypiProject(s string) (string, bool) {
	n := pypiSepRe.ReplaceAllString(strings.ToLower(s), "-")
	return n, len(n) <= 200 && pypiProjectRe.MatchString(n)
}

func (p *Proxy) pypi(w http.ResponseWriter, r *http.Request, repo, base, sub string) {
	switch {
	case strings.HasPrefix(sub, "simple/"):
		proj, ok := pypiProject(strings.TrimSuffix(strings.TrimPrefix(sub, "simple/"), "/"))
		if !ok {
			http.Error(w, "bad project name", http.StatusBadRequest)
			return
		}
		p.pypiIndex(w, r, repo, base, proj)
	case strings.HasPrefix(sub, "files/"):
		sha, rest, ok := strings.Cut(strings.TrimPrefix(sub, "files/"), "/")
		if !ok || !validDigest(sha) || !cleanPath(rest) {
			http.Error(w, "bad file address", http.StatusBadRequest)
			return
		}
		p.pypiFile(w, r, repo, sha, rest)
	default:
		http.NotFound(w, r)
	}
}

// cleanPath accepts a relative path of plain segments.
func cleanPath(s string) bool {
	if s == "" || strings.ContainsAny(s, "\\\x00?#") {
		return false
	}
	for _, seg := range strings.Split(s, "/") {
		if seg == "" || seg == "." || seg == ".." || strings.Contains(seg, "%") {
			return false
		}
	}
	return true
}

func (p *Proxy) pypiIndex(w http.ResponseWriter, r *http.Request, repo, base, proj string) {
	asJSON := strings.Contains(r.Header.Get("Accept"), pypiJSON)
	form, accept, ctype := "html", pypiHTMLAccept, "text/html; charset=utf-8"
	if asJSON {
		form, accept, ctype = "json", pypiJSONAccept, pypiJSON
	}
	b, err := p.document(r.Context(), repo, "pypi/index/"+form+"/"+proj, p.pypiUp()+"/simple/"+proj+"/", accept, true)
	if err != nil {
		fail(w, err)
		return
	}
	if asJSON {
		out, ok := p.pypiRewriteJSON(b, base)
		if !ok {
			http.Error(w, "the upstream index could not be read", http.StatusBadGateway)
			return
		}
		b = out
	} else {
		b = p.pypiRewriteHTML(b, base)
	}
	w.Header().Set("Content-Type", ctype)
	_, _ = w.Write(b)
}

// pypiRewriteHTML points every file link that carries a SHA-256 at the hub;
// a link without one keeps its upstream address.
func (p *Proxy) pypiRewriteHTML(doc []byte, base string) []byte {
	re := regexp.MustCompile(`href="` + regexp.QuoteMeta(p.filesUp()) + `/([^"#]+)#sha256=([0-9a-f]{64})"`)
	return re.ReplaceAll(doc, []byte(`href="`+strings.ReplaceAll(base, "$", "$$")+`/files/${2}/${1}#sha256=${2}"`))
}

// pypiRewriteJSON does the same for the JSON form of PEP 691.
func (p *Proxy) pypiRewriteJSON(doc []byte, base string) ([]byte, bool) {
	var d map[string]any
	dec := json.NewDecoder(bytes.NewReader(doc))
	dec.UseNumber()
	if err := dec.Decode(&d); err != nil {
		return nil, false
	}
	files, _ := d["files"].([]any)
	prefix := p.filesUp() + "/"
	for _, f := range files {
		m, ok := f.(map[string]any)
		if !ok {
			continue
		}
		u, _ := m["url"].(string)
		hashes, _ := m["hashes"].(map[string]any)
		sha, _ := hashes["sha256"].(string)
		rest, found := strings.CutPrefix(u, prefix)
		if !found || !validDigest(sha) {
			continue
		}
		rest, _, _ = strings.Cut(rest, "#")
		if !cleanPath(rest) {
			continue
		}
		m["url"] = base + "/files/" + sha + "/" + rest
	}
	out, err := json.Marshal(d)
	return out, err == nil
}

func (p *Proxy) pypiFile(w http.ResponseWriter, r *http.Request, repo, sha, rest string) {
	key, err := Name(NSPackages, repo, "pypi/file/"+sha+"/"+rest)
	if err != nil {
		fail(w, err)
		return
	}
	if p.serveStored(w, r, repo, key, "application/octet-stream") {
		p.stats.Hit(repo, p.now())
		return
	}
	p.stats.Miss(repo)
	if err := p.storeFile(r.Context(), key, p.filesUp()+"/"+rest, sha, nil); err != nil {
		fail(w, err)
		return
	}
	if !p.serveStored(w, r, repo, key, "application/octet-stream") {
		http.Error(w, "the file could not be read back", http.StatusBadGateway)
	}
}
