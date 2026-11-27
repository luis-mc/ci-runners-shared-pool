package core

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/distribution/distribution/v3"
	"github.com/distribution/distribution/v3/registry/storage"
	"github.com/distribution/reference"
	"github.com/opencontainers/go-digest"
	v1 "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry/keys"
)

// Ledger keeps the rows of staging and promotion (internal/store/registry_core.go).
type Ledger interface {
	// TagExists says whether (name, tag) has a row; tags are immutable (R5.6).
	TagExists(ctx context.Context, name, tag string) (bool, error)
	// Stage records a staged image with the canonical JSON of its key input.
	Stage(ctx context.Context, img registry.Image, keyInputJSON string) error
	// Refuse marks the staged image refused with the reason.
	Refuse(ctx context.Context, id, why string) error
	// Promoted writes the tag row and moves the image to img.State.
	Promoted(ctx context.Context, img registry.Image) error
}

// Outcomes is told what became of a push (records.Service.SetPushOutcome).
type Outcomes interface {
	SetPushOutcome(ctx context.Context, pushID string, promoted bool, imageID, why string) error
}

// Promoter is registry.Promoter over the embedded registry and a Ledger.
type Promoter struct {
	Registry *Registry
	Ledger   Ledger
	Log      *slog.Logger
	Now      func() time.Time
	// Room is asked before a promotion copies anything: may the kind take the
	// layers of this image (budget R5.41, R5.50)? A non-nil error refuses the
	// promotion; it is wrapped with registry.ErrPromoteRefuse. Nil allows.
	Room  func(ctx context.Context, kind registry.Kind, layers []registry.Layer) error
	hooks []registry.PromotedHook
	mu    sync.Mutex
}

var _ registry.Promoter = (*Promoter)(nil)

// OnPromoted adds a hook (REG3 update detection, REG4 layer accounting, REG8
// scan). A hook error is logged and never undoes a promotion.
func (p *Promoter) OnPromoted(h registry.PromotedHook) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.hooks = append(p.hooks, h)
}

func (p *Promoter) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

func (p *Promoter) log() *slog.Logger {
	if p.Log != nil {
		return p.Log
	}
	return slog.Default()
}

// refuse wraps a reason as ErrPromoteRefuse.
func refuse(format string, a ...any) error {
	return fmt.Errorf("%w: %s", registry.ErrPromoteRefuse, fmt.Sprintf(format, a...))
}

func newImageID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return "img_" + hex.EncodeToString(b[:])
}

// requiredLabels are the labels every image carries (R5.7).
var requiredLabels = []string{
	keys.LabelRevision, keys.LabelSource, keys.LabelBaseName, keys.LabelBaseDigest,
	keys.LabelProfile, keys.LabelLineage, keys.LabelStackHash, keys.LabelKey,
}

// check runs the input checks that need no storage: the machine pushed to its
// own staging, the target is a promoted name that follows from the key input,
// and the key equals ComputeKey of the key input (R5.5).
func check(s registry.Staged) (registry.Kind, error) {
	if err := checkMachineID(s.MachineID); err != nil {
		return "", refuse("%v", err)
	}
	if !OwnStaging(s.MachineID, s.Name) {
		return "", refuse("%s is not below the staging root of machine %s", s.Name, s.MachineID)
	}
	kind, ok := TargetKind(s.Target)
	if !ok {
		return "", refuse("%s is not a standard or custom image name", s.Target)
	}
	if s.Name != registry.IncomingName(s.MachineID, s.Target) {
		return "", refuse("the staging name %s does not belong to %s", s.Name, s.Target)
	}
	if !s.Key.Valid() {
		return "", refuse("the key %q is not 12 hex characters", s.Key.String())
	}
	want, err := registry.ComputeKey(s.KeyInput)
	if err != nil {
		return "", refuse("the key input is not valid: %v", err)
	}
	if want != s.Key {
		return "", refuse("the key %s is not the key of the key input (%s)", s.Key, want)
	}
	switch kind {
	case registry.KindStandard:
		if s.Target != registry.StdName(s.KeyInput.Profile) || s.Source != "" {
			return "", refuse("%s is not the standard image of profile %s", s.Target, s.KeyInput.Profile)
		}
	case registry.KindCustom:
		owner, repo, ok := strings.Cut(s.Source, "/")
		if !ok || owner == "" || repo == "" || s.Target != registry.CustomName(owner, repo, s.KeyInput.Profile) {
			return "", refuse("%s is not the custom image of %q and profile %s", s.Target, s.Source, s.KeyInput.Profile)
		}
	}
	return kind, nil
}

// Promote implements registry.Promoter (R5.5, R5.6).
func (p *Promoter) Promote(ctx context.Context, s registry.Staged) (registry.Image, error) {
	if p.Registry.ReadOnly() {
		return registry.Image{}, registry.ErrReadOnly
	}
	kind, cerr := check(s)
	img := registry.Image{
		ID: newImageID(), Kind: kind, Name: s.Target, Key: s.Key, Profile: s.KeyInput.Profile,
		Arch: registry.OCIArch(s.KeyInput.Arch), State: registry.StateStaged, Manifest: s.Manifest,
		StackHash: s.KeyInput.StackHash, BaseDigest: s.KeyInput.BaseDigest, SourceRepository: s.Source,
		Commit: s.Commit, Branch: s.Branch, PushedBy: s.MachineID, PushedAt: p.now(), Scan: registry.ScanNotYet,
	}
	recordable := cerr == nil
	if recordable {
		input, err := registry.CanonicalJSON(s.KeyInput)
		if err != nil {
			return registry.Image{}, refuse("%v", err)
		}
		if err := p.Ledger.Stage(ctx, img, string(input)); err != nil {
			return registry.Image{}, fmt.Errorf("record the staged image: %w", err)
		}
	}
	fail := func(err error) (registry.Image, error) {
		if recordable {
			if rerr := p.Ledger.Refuse(ctx, img.ID, err.Error()); rerr != nil {
				p.log().Warn("registry: could not record a refused image", "image", img.ID, "err", rerr)
			}
		}
		return registry.Image{ID: img.ID}, err
	}
	if cerr != nil {
		return fail(cerr)
	}
	base, err := p.promoteBlobs(ctx, s, &img)
	if err != nil {
		return fail(err)
	}
	img.BaseName = base
	img.State = registry.StateCurrent
	if s.Branch != "" {
		img.State = registry.StateVariant
	}
	img.PromotedAt = p.now()
	if err := p.Ledger.Promoted(ctx, img); err != nil {
		// The manifest is in the target now; the row failed. Take the manifest
		// out again so a retry can promote, and report the failure.
		_ = p.Registry.DeleteManifest(ctx, s.Target, s.Manifest)
		return fail(fmt.Errorf("record the promotion: %w", err))
	}
	p.mu.Lock()
	hooks := append([]registry.PromotedHook(nil), p.hooks...)
	p.mu.Unlock()
	for _, h := range hooks {
		if err := h(ctx, img); err != nil {
			p.log().Warn("registry: a promoted hook failed", "image", img.ID, "err", err)
		}
	}
	return img, nil
}

// promoteBlobs checks the staged manifest and copies it to the target by
// linking blobs, then tags it with the key. It returns the base image name
// from the labels.
func (p *Promoter) promoteBlobs(ctx context.Context, s registry.Staged, img *registry.Image) (string, error) {
	src, err := p.Registry.repo(ctx, s.Name)
	if err != nil {
		return "", refuse("%v", err)
	}
	desc, err := src.Tags(ctx).Get(ctx, s.Key.String())
	if err != nil {
		return "", refuse("nothing is tagged %s in %s", s.Key, s.Name)
	}
	want, err := digest.Parse(s.Manifest)
	if err != nil {
		return "", refuse("the reported digest %q is not a digest", s.Manifest)
	}
	if desc.Digest != want {
		return "", refuse("the manifest in staging is %s, the machine reported %s", desc.Digest, want)
	}
	// The tag must be new under the target, in the store and in the registry.
	if exists, err := p.Ledger.TagExists(ctx, s.Target, s.Key.String()); err != nil {
		return "", fmt.Errorf("look up the tag: %w", err)
	} else if exists {
		return "", fmt.Errorf("%w: %w", registry.ErrPromoteRefuse, registry.ErrTagExists)
	}
	dst, err := p.Registry.repo(ctx, s.Target)
	if err != nil {
		return "", refuse("%v", err)
	}
	if _, terr := dst.Tags(ctx).Get(ctx, s.Key.String()); terr == nil {
		return "", fmt.Errorf("%w: %w", registry.ErrPromoteRefuse, registry.ErrTagExists)
	}
	srcMS, err := src.Manifests(ctx)
	if err != nil {
		return "", err
	}
	m, err := srcMS.Get(ctx, want)
	if err != nil {
		return "", refuse("the manifest %s is not in %s", want, s.Name)
	}
	if _, payload, perr := m.Payload(); perr != nil || digest.FromBytes(payload) != want {
		return "", refuse("the manifest in staging does not hash to %s", want)
	}
	labels, size, err := labelsOf(ctx, src, srcMS, m, registry.OCIArch(s.KeyInput.Arch))
	if err != nil {
		return "", refuse("%v", err)
	}
	if err := checkLabels(labels, s); err != nil {
		return "", err
	}
	img.SizeBytes = size
	if p.Room != nil {
		layers, err := p.Registry.Layers(ctx, s.Name, s.Manifest)
		if err != nil {
			return "", fmt.Errorf("list the layers of the staged image: %w", err)
		}
		if err := p.Room(ctx, img.Kind, layers); err != nil {
			if errors.Is(err, registry.ErrOverBudget) {
				return "", fmt.Errorf("%w: %w", registry.ErrPromoteRefuse, err)
			}
			return "", err
		}
	}
	dstMS, err := dst.Manifests(ctx)
	if err != nil {
		return "", err
	}
	if err := copyManifest(ctx, src, srcMS, dst, dstMS, s.Name, m); err != nil {
		return "", fmt.Errorf("copy the manifest: %w", err)
	}
	if err := dst.Tags(ctx).Tag(ctx, s.Key.String(), v1.Descriptor{Digest: want, MediaType: desc.MediaType, Size: desc.Size}); err != nil {
		return "", fmt.Errorf("tag the image: %w", err)
	}
	return labels[keys.LabelBaseName], nil
}

// checkLabels refuses an image whose labels are missing or disagree with the
// key input (R5.5, R5.7).
func checkLabels(l map[string]string, s registry.Staged) error {
	for _, k := range requiredLabels {
		if _, ok := l[k]; !ok {
			return refuse("the label %s is missing", k)
		}
	}
	in := s.KeyInput
	agree := map[string]string{
		keys.LabelKey:        s.Key.String(),
		keys.LabelStackHash:  in.StackHash,
		keys.LabelProfile:    in.Profile,
		keys.LabelBaseDigest: in.BaseDigest,
		keys.LabelLineage:    keys.Lineage(s.Source, in.Profile, in.Arch),
	}
	for k, v := range agree {
		if l[k] != v {
			return refuse("the label %s is %q, the key input says %q", k, l[k], v)
		}
	}
	return nil
}

func isManifestType(mt string) bool {
	switch mt {
	case v1.MediaTypeImageManifest, v1.MediaTypeImageIndex,
		"application/vnd.docker.distribution.manifest.v2+json", "application/vnd.docker.distribution.manifest.list.v2+json":
		return true
	}
	return false
}

// labelsOf reads the labels of an image: the config labels, overlaid by the
// manifest annotations. For an index (docker push with the containerd store)
// it reads the child image of the arch and ignores attestation children. size
// is the sum of the blobs of the image, children included.
func labelsOf(ctx context.Context, repo distribution.Repository, ms distribution.ManifestService, m distribution.Manifest, arch string) (map[string]string, int64, error) {
	mt, payload, err := m.Payload()
	if err != nil {
		return nil, 0, err
	}
	var doc struct {
		Config      v1.Descriptor     `json:"config"`
		Manifests   []v1.Descriptor   `json:"manifests"`
		Annotations map[string]string `json:"annotations"`
	}
	if err := json.Unmarshal(payload, &doc); err != nil {
		return nil, 0, fmt.Errorf("the manifest is not JSON: %w", err)
	}
	var size int64
	for _, ref := range m.References() {
		size += ref.Size
	}
	if mt == v1.MediaTypeImageIndex || mt == "application/vnd.docker.distribution.manifest.list.v2+json" {
		for _, child := range doc.Manifests {
			if child.Annotations["vnd.docker.reference.type"] != "" || child.Platform == nil || child.Platform.Architecture != arch {
				continue
			}
			cm, err := ms.Get(ctx, child.Digest)
			if err != nil {
				return nil, 0, fmt.Errorf("the child manifest %s is missing", child.Digest)
			}
			l, _, err := labelsOf(ctx, repo, ms, cm, arch)
			return l, size, err
		}
		return nil, 0, fmt.Errorf("the index has no image for %s", arch)
	}
	if doc.Config.Digest == "" {
		return nil, 0, errors.New("the manifest has no config")
	}
	cfg, err := repo.Blobs(ctx).Get(ctx, doc.Config.Digest)
	if err != nil {
		return nil, 0, fmt.Errorf("the config blob is missing: %w", err)
	}
	var ic struct {
		Config struct {
			Labels map[string]string `json:"Labels"`
		} `json:"config"`
	}
	if err := json.Unmarshal(cfg, &ic); err != nil {
		return nil, 0, fmt.Errorf("the config is not JSON: %w", err)
	}
	out := map[string]string{}
	for k, v := range ic.Config.Labels {
		out[k] = v
	}
	for k, v := range doc.Annotations {
		out[k] = v
	}
	return out, size, nil
}

// copyManifest links every blob of m (and of the children of an index) into
// dst by a mount from src, then puts the manifest. No data moves.
func copyManifest(ctx context.Context, src distribution.Repository, srcMS distribution.ManifestService, dst distribution.Repository, dstMS distribution.ManifestService, srcName string, m distribution.Manifest) error {
	srcNamed, err := reference.WithName(srcName)
	if err != nil {
		return err
	}
	for _, ref := range m.References() {
		if isManifestType(ref.MediaType) {
			child, err := srcMS.Get(ctx, ref.Digest)
			if err != nil {
				return fmt.Errorf("child manifest %s: %w", ref.Digest, err)
			}
			if err := copyManifest(ctx, src, srcMS, dst, dstMS, srcName, child); err != nil {
				return err
			}
			continue
		}
		canon, err := reference.WithDigest(srcNamed, ref.Digest)
		if err != nil {
			return err
		}
		w, err := dst.Blobs(ctx).Create(ctx, storage.WithMountFrom(canon))
		var mounted distribution.ErrBlobMounted
		switch {
		case errors.As(err, &mounted):
		case err == nil:
			_ = w.Cancel(ctx)
			return fmt.Errorf("the blob %s could not be linked", ref.Digest)
		default:
			return err
		}
	}
	_, err = dstMS.Put(ctx, m)
	return err
}
