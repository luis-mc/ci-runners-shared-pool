package lancache

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Header names of a peer call. The credential is the machine credential of the
// agent link, sent as a bearer value.
const (
	HeaderMachineID = "Crsp-Machine-Id"
	HeaderDigest    = "Crsp-Digest"
	pathPrefix      = "/lan/v1/"
	twirpPrefix     = "/twirp/github.actions.results.api.v1.CacheService/"
	urlLife         = 15 * time.Minute
)

// Authenticator says whether a machine id and credential belong to a machine
// that joined this pool (machines.Service.Authenticate behind an adapter).
type Authenticator interface {
	Authenticate(ctx context.Context, credential, machineID string) error
}

// Server answers peers. Build with NewServer.
type Server struct {
	store *Store
	auth  Authenticator
	now   func() time.Time
	seed  []byte // signs the upload and download addresses of this run
	base  string // public base of this server for the addresses it returns
	stats Stats  // hits, misses and bytes served per repository (R5.10)
}

// NewServer makes the handler. base is the address peers reach it at, like
// https://192.168.1.20:8470 (no trailing slash).
func NewServer(store *Store, auth Authenticator, base string, now func() time.Time) (*Server, error) {
	seed := make([]byte, 32)
	if _, err := rand.Read(seed); err != nil {
		return nil, fmt.Errorf("make signing seed: %w", err)
	}
	if now == nil {
		now = time.Now
	}
	return &Server{store: store, auth: auth, now: now, seed: seed, base: strings.TrimRight(base, "/")}, nil
}

// Handler is the one HTTP handler.
func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, pathPrefix):
			s.blobs(w, r)
		case strings.HasPrefix(r.URL.Path, twirpPrefix):
			s.twirp(w, r)
		default:
			http.NotFound(w, r)
		}
	})
}

func (s *Server) sign(method, path string, exp int64) string {
	m := hmac.New(sha256.New, s.seed)
	_, _ = fmt.Fprintf(m, "%s\n%s\n%d", method, path, exp)
	return hex.EncodeToString(m.Sum(nil))
}

func (s *Server) signedURL(method, path string) string {
	exp := s.now().Add(urlLife).Unix()
	return s.base + path + "?exp=" + strconv.FormatInt(exp, 10) + "&sig=" + s.sign(method, path, exp)
}

func (s *Server) validSig(r *http.Request) bool {
	exp, err := strconv.ParseInt(r.URL.Query().Get("exp"), 10, 64)
	if err != nil || s.now().Unix() > exp {
		return false
	}
	want := s.sign(r.Method, r.URL.EscapedPath(), exp)
	return hmac.Equal([]byte(want), []byte(r.URL.Query().Get("sig")))
}

// allowed: a pool machine (id + credential) or a signed address this server
// gave to one.
func (s *Server) allowed(r *http.Request) bool {
	if r.URL.Query().Get("sig") != "" {
		return s.validSig(r)
	}
	h := r.Header.Get("Authorization")
	if len(h) < 8 || !strings.EqualFold(h[:7], "bearer ") {
		return false
	}
	id := r.Header.Get(HeaderMachineID)
	if id == "" {
		return false
	}
	return s.auth.Authenticate(r.Context(), strings.TrimSpace(h[7:]), id) == nil
}

// blobs: PUT and GET /lan/v1/<ns>/<scope>/<name...>
func (s *Server) blobs(w http.ResponseWriter, r *http.Request) {
	if !s.allowed(r) {
		http.Error(w, "this machine is not in the pool", http.StatusUnauthorized)
		return
	}
	rest := strings.TrimPrefix(r.URL.EscapedPath(), pathPrefix)
	parts := strings.SplitN(rest, "/", 3)
	if len(parts) != 3 {
		http.Error(w, "bad cache name", http.StatusBadRequest)
		return
	}
	var dec [3]string
	for i, p := range parts {
		u, err := url.PathUnescape(p)
		if err != nil {
			http.Error(w, "bad cache name", http.StatusBadRequest)
			return
		}
		dec[i] = u
	}
	name, err := Name(dec[0], dec[1], dec[2])
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	switch r.Method {
	case http.MethodPut:
		s.put(w, r, dec[0], name)
	case http.MethodGet, http.MethodHead:
		s.get(w, r, name)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) put(w http.ResponseWriter, r *http.Request, ns, name string) {
	want := strings.ToLower(r.Header.Get(HeaderDigest))
	// Tools and packages are always checked by a digest the sender names. An
	// Actions entry comes from the toolkit, which sends none: the server takes
	// the digest of what it received and checks every read against it.
	if want == "" && ns != NSActions {
		http.Error(w, "a digest is required", http.StatusBadRequest)
		return
	}
	if ns == NSActions {
		if _, err := s.store.Lookup(name); err == nil {
			http.Error(w, "entry already exists", http.StatusConflict)
			return
		}
	}
	e, err := s.store.Put(name, want, r.Body)
	switch {
	case errors.Is(err, ErrDigestMismatch):
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
	case errors.Is(err, ErrTooLarge):
		http.Error(w, err.Error(), http.StatusRequestEntityTooLarge)
	case err != nil:
		http.Error(w, "the cache could not store the entry", http.StatusInternalServerError)
	default:
		w.Header().Set(HeaderDigest, e.Digest)
		w.WriteHeader(http.StatusCreated)
	}
}

func (s *Server) get(w http.ResponseWriter, r *http.Request, name string) {
	f, e, err := s.store.Get(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer func() { _ = f.Close() }()
	w.Header().Set(HeaderDigest, e.Digest)
	if parts := strings.SplitN(name, "/", 3); len(parts) == 3 && parts[0] == NSActions && r.Method == http.MethodGet {
		s.stats.Served(parts[1], e.Size)
	}
	http.ServeContent(w, r, "", time.Time{}, f)
}

// Twirp (R5.1), JSON bodies only. The toolkit sends the repository id in
// metadata; it is the scope, so keys never cross repositories.
type twirpMeta struct {
	RepositoryID string `json:"repository_id"`
}
type twirpReq struct {
	Key         string    `json:"key"`
	Version     string    `json:"version"`
	RestoreKeys []string  `json:"restore_keys"`
	SizeBytes   int64     `json:"size_bytes"`
	Metadata    twirpMeta `json:"metadata"`
}

func actionsPath(scope, key, version string) string {
	return pathPrefix + NSActions + "/" + url.PathEscape(scope) + "/" + url.PathEscape(key) + "/" + url.PathEscape(version)
}

func (s *Server) twirp(w http.ResponseWriter, r *http.Request) {
	if !s.allowed(r) {
		http.Error(w, "this machine is not in the pool", http.StatusUnauthorized)
		return
	}
	if r.Method != http.MethodPost || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		http.Error(w, "JSON POST only", http.StatusUnsupportedMediaType)
		return
	}
	var req twirpReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	scope := req.Metadata.RepositoryID
	reply := func(v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	deny := func(msg string) { reply(map[string]any{"ok": false, "message": msg}) }
	if scope == "" || req.Key == "" || req.Version == "" && !strings.HasSuffix(r.URL.Path, "GetCacheEntryDownloadURL") {
		deny("missing repository, key or version")
		return
	}
	switch strings.TrimPrefix(r.URL.Path, twirpPrefix) {
	case "CreateCacheEntry":
		name, err := Name(NSActions, scope, url.PathEscape(req.Key)+"/"+url.PathEscape(req.Version))
		if err != nil {
			deny("bad key")
			return
		}
		if _, err := s.store.Lookup(name); err == nil {
			deny("cache entry already exists")
			return
		}
		reply(map[string]any{"ok": true, "signed_upload_url": s.signedURL(http.MethodPut, actionsPath(scope, req.Key, req.Version))})
	case "FinalizeCacheEntryUpload":
		name, err := Name(NSActions, scope, url.PathEscape(req.Key)+"/"+url.PathEscape(req.Version))
		if err != nil {
			deny("bad key")
			return
		}
		e, err := s.store.Lookup(name)
		if err != nil || (req.SizeBytes > 0 && e.Size != req.SizeBytes) {
			deny("the upload is missing or has another size")
			return
		}
		reply(map[string]any{"ok": true, "entry_id": e.Digest[:16]})
	case "GetCacheEntryDownloadURL":
		s.download(reply, scope, req)
	default:
		http.NotFound(w, r)
	}
}

// download: the exact key and version first, then each restore key as a
// prefix, newest first (R5.3), inside this repository only.
func (s *Server) download(reply func(any), scope string, req twirpReq) {
	miss := map[string]any{"ok": false}
	try := func(key string) (string, bool) {
		if req.Version != "" {
			n, err := Name(NSActions, scope, url.PathEscape(key)+"/"+url.PathEscape(req.Version))
			if err == nil {
				if _, err := s.store.Lookup(n); err == nil {
					return key, true
				}
			}
		}
		return "", false
	}
	if k, ok := try(req.Key); ok {
		s.stats.Hit(scope, s.now())
		reply(map[string]any{"ok": true, "matched_key": k, "signed_download_url": s.signedURL(http.MethodGet, actionsPath(scope, k, req.Version))})
		return
	}
	for _, rk := range req.RestoreKeys {
		prefix := NSActions + "/" + scope + "/" + url.PathEscape(rk)
		n, _, ok := s.store.PrefixNewest(prefix)
		if !ok || !strings.HasSuffix(n, "/"+url.PathEscape(req.Version)) {
			continue
		}
		seg := strings.Split(n, "/")
		key, err := url.PathUnescape(seg[2])
		if err != nil {
			continue
		}
		s.stats.Hit(scope, s.now())
		reply(map[string]any{"ok": true, "matched_key": key, "signed_download_url": s.signedURL(http.MethodGet, actionsPath(scope, key, req.Version))})
		return
	}
	s.stats.Miss(scope)
	reply(miss)
}
