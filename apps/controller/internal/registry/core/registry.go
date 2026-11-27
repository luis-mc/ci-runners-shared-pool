package core

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/distribution/distribution/v3"
	"github.com/distribution/distribution/v3/configuration"
	"github.com/distribution/distribution/v3/registry/handlers"
	"github.com/distribution/distribution/v3/registry/storage"
	storagedriver "github.com/distribution/distribution/v3/registry/storage/driver"
	"github.com/distribution/reference"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry/fsdriver"
	"github.com/opencontainers/go-digest"
	v1 "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/sirupsen/logrus"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry"
)

// eventBuffer is how many events wait for the consumer; a full buffer drops the
// newest event rather than slow a pull.
const eventBuffer = 256

// maxManifest is the largest manifest body the referrers header reads.
const maxManifest = 4 << 20

// Options configure New.
type Options struct {
	// Dir is the storage folder, <data_dir>/registry or the folder of the
	// registry settings (R5.1).
	Dir string
	// ListenAddr is the address of the listener the registry is mounted on; it
	// must pass CheckListen (R5.2).
	ListenAddr string
	// Tokens checks the token of every request (R5.3).
	Tokens registry.TokenIssuer
	Logger *slog.Logger
}

// Registry is registry.Registry over an embedded Distribution v3.
type Registry struct {
	app      *handlers.App
	driver   storagedriver.StorageDriver
	ns       distribution.Namespace
	tokens   registry.TokenIssuer
	log      *slog.Logger
	handler  http.Handler
	readOnly atomic.Bool
	events   chan registry.Event
	dir      string
}

var _ registry.Registry = (*Registry)(nil)

// New mounts Distribution on Dir. It refuses an address that is not a private
// LAN one.
func New(ctx context.Context, o Options) (r *Registry, err error) {
	if err := CheckListen(o.ListenAddr); err != nil {
		return nil, err
	}
	if o.Dir == "" || o.Tokens == nil {
		return nil, errors.New("registry: a folder and a token issuer are needed")
	}
	if err := os.MkdirAll(o.Dir, 0o700); err != nil {
		return nil, fmt.Errorf("registry folder: %w", err)
	}
	lg := o.Logger
	if lg == nil {
		lg = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	drv, err := fsdriver.New(map[string]any{"rootdirectory": o.Dir})
	if err != nil {
		return nil, fmt.Errorf("registry storage: %w", err)
	}
	ns, err := storage.NewRegistry(ctx, drv, storage.EnableDelete)
	if err != nil {
		return nil, fmt.Errorf("registry storage: %w", err)
	}
	var secret [32]byte
	if _, err := rand.Read(secret[:]); err != nil {
		return nil, err
	}
	cfg := newConfig(o.Dir, hex.EncodeToString(secret[:]))
	logrus.SetLevel(logrus.ErrorLevel) // Distribution logs through logrus; its access lines are not wanted
	defer func() {
		if p := recover(); p != nil { // NewApp panics on a bad configuration
			r, err = nil, fmt.Errorf("registry: %v", p)
		}
	}()
	app := handlers.NewApp(ctx, cfg)
	r = &Registry{app: app, driver: drv, ns: ns, tokens: o.Tokens, log: lg, events: make(chan registry.Event, eventBuffer), dir: o.Dir}
	r.handler = privatePeersOnly(r.authorize(app))
	return r, nil
}

// newConfig is the Distribution configuration of the core. It sets no
// notification endpoint and no reporting (bugsnag, newrelic): the registry sends
// no telemetry to any host (R5.81); events stay in process (Events).
func newConfig(dir, secret string) *configuration.Configuration {
	cfg := &configuration.Configuration{Version: "0.1"}
	cfg.Storage = configuration.Storage{
		fsdriver.Name: configuration.Parameters{"rootdirectory": dir},
		"delete":      configuration.Parameters{"enabled": true},
	}
	cfg.HTTP.Secret = secret
	cfg.Log.Level = "error"
	cfg.Log.AccessLog.Disabled = true
	return cfg
}

// Close stops the Distribution app.
func (r *Registry) Close() error { return r.app.Shutdown() }

// Handler implements registry.Registry: mount it at /v2/.
func (r *Registry) Handler() http.Handler { return r.handler }

// SetReadOnly implements registry.Registry.
func (r *Registry) SetReadOnly(on bool) { r.readOnly.Store(on) }

// ReadOnly reports the read-only window.
func (r *Registry) ReadOnly() bool { return r.readOnly.Load() }

// Events implements registry.Registry.
func (r *Registry) Events() <-chan registry.Event { return r.events }

func (r *Registry) emit(ev registry.Event) {
	select {
	case r.events <- ev:
	default:
	}
}

// repo opens a repository of the in-process namespace.
func (r *Registry) repo(ctx context.Context, name string) (distribution.Repository, error) {
	n, err := reference.WithName(name)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrBadName, name)
	}
	return r.ns.Repository(ctx, n)
}

// DeleteManifest implements registry.Registry: the manifest and the tags that
// name it go; blobs wait for GC.
func (r *Registry) DeleteManifest(ctx context.Context, name, dgst string) error {
	d, err := digest.Parse(dgst)
	if err != nil {
		return fmt.Errorf("registry: digest %q: %w", dgst, err)
	}
	repo, err := r.repo(ctx, name)
	if err != nil {
		return err
	}
	ms, err := repo.Manifests(ctx)
	if err != nil {
		return err
	}
	if tags, terr := repo.Tags(ctx).Lookup(ctx, v1.Descriptor{Digest: d}); terr == nil {
		for _, t := range tags {
			_ = repo.Tags(ctx).Untag(ctx, t)
		}
	}
	if err := ms.Delete(ctx, d); err != nil {
		var rev distribution.ErrManifestUnknownRevision
		var unk distribution.ErrManifestUnknown
		if errors.As(err, &rev) || errors.As(err, &unk) || errors.Is(err, distribution.ErrBlobUnknown) {
			return registry.ErrNotFound
		}
		return err
	}
	return nil
}

// GC implements registry.Registry: mark and sweep of the blobs no manifest
// names. A dry run only counts. The bytes are those of the unmarked blobs.
func (r *Registry) GC(ctx context.Context, dryRun bool) (int64, error) {
	freed, err := r.unreferencedBytes(ctx)
	if err != nil {
		return 0, err
	}
	if dryRun {
		return freed, nil
	}
	if err := storage.MarkAndSweep(ctx, r.driver, r.ns, storage.GCOpts{Quiet: true}); err != nil {
		return 0, err
	}
	return freed, nil
}

// unreferencedBytes adds up the blobs no manifest of any repository names.
func (r *Registry) unreferencedBytes(ctx context.Context) (int64, error) {
	repos, ok := r.ns.(distribution.RepositoryEnumerator)
	if !ok {
		return 0, errors.New("registry: storage cannot list repositories")
	}
	marked := map[digest.Digest]bool{}
	err := repos.Enumerate(ctx, func(name string) error {
		repo, err := r.repo(ctx, name)
		if err != nil {
			return err
		}
		ms, err := repo.Manifests(ctx)
		if err != nil {
			return err
		}
		en, ok := ms.(distribution.ManifestEnumerator)
		if !ok {
			return errors.New("registry: storage cannot list manifests")
		}
		return en.Enumerate(ctx, func(d digest.Digest) error {
			marked[d] = true
			m, err := ms.Get(ctx, d)
			if err != nil {
				return nil // a manifest gone meanwhile marks nothing
			}
			for _, ref := range m.References() {
				marked[ref.Digest] = true
			}
			return nil
		})
	})
	if err != nil {
		return 0, err
	}
	var total int64
	err = r.ns.Blobs().Enumerate(ctx, func(d digest.Digest) error {
		if marked[d] {
			return nil
		}
		desc, serr := r.ns.BlobStatter().Stat(ctx, d)
		if serr == nil {
			total += desc.Size
		}
		return nil
	})
	return total, err
}

// ---- the door: token, scope, read-only, events, referrers

func registryError(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"errors": []map[string]string{{"code": code, "message": msg}}})
}

// tokenOf takes the token from a Bearer header, or from the password of a
// Basic header (docker login with the token on standard input).
func tokenOf(req *http.Request) string {
	h := req.Header.Get("Authorization")
	if t, ok := strings.CutPrefix(h, "Bearer "); ok {
		return strings.TrimSpace(t)
	}
	if strings.HasPrefix(h, "Basic ") {
		if _, pw, ok := req.BasicAuth(); ok {
			return pw
		}
	}
	return ""
}

// route is one parsed /v2/ path.
type route struct {
	name, kind, ref string // kind: manifests, blobs, uploads, tags, referrers
}

// parseRoute splits /v2/<name>/manifests/<ref>, /blobs/<digest>,
// /blobs/uploads/[<id>], /tags/list and /referrers/<digest>. The name is
// everything before the last place the path fits one of those shapes, as
// Distribution reads it.
func parseRoute(path string) (route, bool) {
	segs := strings.Split(strings.TrimPrefix(path, "/v2/"), "/")
	for i := len(segs) - 2; i >= 1; i-- {
		rest := segs[i:]
		var kind, ref string
		switch {
		case rest[0] == "manifests" && len(rest) == 2 && rest[1] != "":
			kind, ref = "manifests", rest[1]
		case rest[0] == "referrers" && len(rest) == 2 && rest[1] != "":
			kind, ref = "referrers", rest[1]
		case rest[0] == "tags" && len(rest) == 2 && rest[1] == "list":
			kind = "tags"
		case rest[0] == "blobs" && len(rest) == 2 && rest[1] != "" && rest[1] != "uploads":
			kind, ref = "blobs", rest[1]
		case rest[0] == "blobs" && len(rest) >= 2 && rest[1] == "uploads" && len(rest) <= 3:
			kind = "uploads"
			if len(rest) == 3 {
				ref = rest[2]
			}
		default:
			continue
		}
		name := strings.Join(segs[:i], "/")
		if !ValidName(name) {
			return route{}, false
		}
		return route{name: name, kind: kind, ref: ref}, true
	}
	return route{}, false
}

func challenge(w http.ResponseWriter) {
	w.Header().Add("WWW-Authenticate", `Bearer service="crsp-registry"`)
	w.Header().Add("WWW-Authenticate", `Basic realm="crsp-registry"`)
	registryError(w, http.StatusUnauthorized, "UNAUTHORIZED", "a registry token is needed")
}

// authorize is the door of /v2/: every request carries a valid token (R5.3),
// stays inside the scopes of the token (R5.11), and a push waits out the
// read-only window. A machine never deletes and never lists the catalogue.
func (r *Registry) authorize(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		tok := tokenOf(req)
		if tok == "" {
			challenge(w)
			return
		}
		machine, scopes, err := r.tokens.Verify(req.Context(), tok)
		if err != nil {
			challenge(w)
			return
		}
		if req.URL.Path == "/v2/" || req.URL.Path == "/v2" {
			if req.Method == http.MethodGet || req.Method == http.MethodHead {
				next.ServeHTTP(w, req)
				return
			}
			registryError(w, http.StatusMethodNotAllowed, "UNSUPPORTED", "method not allowed")
			return
		}
		rt, ok := parseRoute(req.URL.Path)
		if !ok {
			registryError(w, http.StatusForbidden, "DENIED", "this path is not open to machines")
			return
		}
		action := ""
		switch req.Method {
		case http.MethodGet, http.MethodHead:
			action = ActionPull
		case http.MethodPost, http.MethodPut, http.MethodPatch:
			action = ActionPush
		default:
			registryError(w, http.StatusForbidden, "DENIED", "a machine never deletes")
			return
		}
		if !Allows(scopes, rt.name, action) {
			registryError(w, http.StatusForbidden, "DENIED", "the token has no "+action+" scope on "+rt.name)
			return
		}
		if action == ActionPush {
			if from := req.URL.Query().Get("from"); from != "" && !Allows(scopes, from, ActionPull) {
				registryError(w, http.StatusForbidden, "DENIED", "the token has no pull scope on "+from)
				return
			}
			if r.readOnly.Load() {
				registryError(w, http.StatusServiceUnavailable, "UNAVAILABLE", "the registry is read-only during the cleanup")
				return
			}
		}
		if rt.kind == "referrers" {
			if req.Method != http.MethodGet && req.Method != http.MethodHead {
				registryError(w, http.StatusMethodNotAllowed, "UNSUPPORTED", "method not allowed")
				return
			}
			r.serveReferrers(w, req, rt)
			return
		}
		rec := &recorder{ResponseWriter: w}
		if rt.kind == "manifests" && req.Method == http.MethodPut {
			if subj := subjectOf(req); subj != "" {
				rec.subject = subj
			}
		}
		next.ServeHTTP(rec, req)
		if rt.kind != "manifests" {
			return
		}
		switch {
		case req.Method == http.MethodGet && rec.status() == http.StatusOK:
			r.emit(registry.Event{At: time.Now(), Action: "pull", Name: rt.name, Digest: digestOf(rt.ref, rec), MachineID: machine})
		case req.Method == http.MethodPut && rec.status() == http.StatusCreated:
			r.emit(registry.Event{At: time.Now(), Action: "push", Name: rt.name, Digest: rec.Header().Get("Docker-Content-Digest"), MachineID: machine})
		}
	})
}

func digestOf(ref string, rec *recorder) string {
	if d := rec.Header().Get("Docker-Content-Digest"); d != "" {
		return d
	}
	if _, err := digest.Parse(ref); err == nil {
		return ref
	}
	return ""
}

// recorder keeps the status a handler wrote.
type recorder struct {
	http.ResponseWriter
	code    int
	subject string // sent as OCI-Subject with a 201
}

func (w *recorder) WriteHeader(c int) {
	if w.code == 0 {
		w.code = c
	}
	if c == http.StatusCreated && w.subject != "" {
		w.Header().Set("OCI-Subject", w.subject)
	}
	w.ResponseWriter.WriteHeader(c)
}

func (w *recorder) Write(b []byte) (int, error) {
	if w.code == 0 {
		w.code = http.StatusOK
	}
	return w.ResponseWriter.Write(b)
}

func (w *recorder) status() int {
	if w.code == 0 {
		return http.StatusOK
	}
	return w.code
}

// Unwrap lets http.ResponseController reach the real writer.
func (w *recorder) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// Flush passes a flush through.
func (w *recorder) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// subjectOf reads the subject digest of a manifest being pushed and puts the
// body back, so the push can answer OCI-Subject (OCI Distribution Spec 1.1).
func subjectOf(req *http.Request) string {
	if req.Body == nil {
		return ""
	}
	body, err := io.ReadAll(io.LimitReader(req.Body, maxManifest+1))
	_ = req.Body.Close()
	req.Body = io.NopCloser(bytes.NewReader(body))
	if err != nil || len(body) > maxManifest {
		return ""
	}
	var m struct {
		Subject *struct {
			Digest string `json:"digest"`
		} `json:"subject"`
	}
	if json.Unmarshal(body, &m) != nil || m.Subject == nil {
		return ""
	}
	return m.Subject.Digest
}

// ---- referrers (v3.1.2 has no referrers route)

// serveReferrers answers GET /v2/<name>/referrers/<digest> with an OCI image
// index of the manifests of the repository whose subject is the digest.
func (r *Registry) serveReferrers(w http.ResponseWriter, req *http.Request, rt route) {
	subject, err := digest.Parse(rt.ref)
	if err != nil {
		registryError(w, http.StatusBadRequest, "DIGEST_INVALID", "the digest is not valid")
		return
	}
	list, err := r.Referrers(req.Context(), rt.name, subject, req.URL.Query().Get("artifactType"))
	if err != nil {
		if errors.Is(err, registry.ErrNotFound) {
			registryError(w, http.StatusNotFound, "NAME_UNKNOWN", "repository name not known to registry")
			return
		}
		registryError(w, http.StatusInternalServerError, "UNKNOWN", "could not list the referrers")
		return
	}
	idx := v1.Index{MediaType: v1.MediaTypeImageIndex, Manifests: list}
	idx.SchemaVersion = 2
	if list == nil {
		idx.Manifests = []v1.Descriptor{}
	}
	body, _ := json.Marshal(idx)
	w.Header().Set("Content-Type", v1.MediaTypeImageIndex)
	if t := req.URL.Query().Get("artifactType"); t != "" {
		w.Header().Set("OCI-Filters-Applied", "artifactType")
	}
	w.WriteHeader(http.StatusOK)
	if req.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
}

// Referrers lists the manifests of repository name whose subject is subject,
// as descriptors with their artifact type and annotations. A filter keeps one
// artifact type.
func (r *Registry) Referrers(ctx context.Context, name string, subject digest.Digest, artifactType string) ([]v1.Descriptor, error) {
	repo, err := r.repo(ctx, name)
	if err != nil {
		return nil, err
	}
	ms, err := repo.Manifests(ctx)
	if err != nil {
		return nil, err
	}
	en, ok := ms.(distribution.ManifestEnumerator)
	if !ok {
		return nil, errors.New("registry: storage cannot list manifests")
	}
	var out []v1.Descriptor
	err = en.Enumerate(ctx, func(d digest.Digest) error {
		m, err := ms.Get(ctx, d)
		if err != nil {
			return nil
		}
		mt, payload, err := m.Payload()
		if err != nil {
			return nil
		}
		var doc struct {
			ArtifactType string `json:"artifactType"`
			Config       struct {
				MediaType string `json:"mediaType"`
			} `json:"config"`
			Subject *struct {
				Digest string `json:"digest"`
			} `json:"subject"`
			Annotations map[string]string `json:"annotations"`
		}
		if json.Unmarshal(payload, &doc) != nil || doc.Subject == nil || doc.Subject.Digest != subject.String() {
			return nil
		}
		at := doc.ArtifactType
		if at == "" {
			at = doc.Config.MediaType
		}
		if artifactType != "" && at != artifactType {
			return nil
		}
		out = append(out, v1.Descriptor{MediaType: mt, Digest: d, Size: int64(len(payload)), ArtifactType: at, Annotations: doc.Annotations})
		return nil
	})
	if err != nil {
		var unknown distribution.ErrRepositoryUnknown
		if errors.As(err, &unknown) {
			return nil, registry.ErrNotFound
		}
		return nil, err
	}
	return out, nil
}

// SizeOnDisk is the size of the registry folder, for the free-disk checks.
func (r *Registry) SizeOnDisk() (int64, error) {
	var n int64
	err := filepath.WalkDir(r.dir, func(_ string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		info, ierr := d.Info()
		if ierr == nil {
			n += info.Size()
		}
		return nil
	})
	return n, err
}

// LogEvents records the registry events as log lines until ctx ends or the
// channel closes: the action, the repository name, the digest and the machine.
// A token is never in an event. The records of REG4 take the same channel
// through records.Service.Consume when the services are joined.
func LogEvents(ctx context.Context, events <-chan registry.Event, log *slog.Logger) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-events:
			if !ok {
				return
			}
			log.Info("registry event", "action", ev.Action, "name", ev.Name, "digest", ev.Digest, "machine", ev.MachineID)
		}
	}
}
