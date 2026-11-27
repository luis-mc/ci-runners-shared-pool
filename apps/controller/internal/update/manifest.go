package update

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/update/lifecycle"
)

// ManifestFailure is a refusal by the signed manifest. Reason is the audit detail
// (bad_signature, bad_hash, expired, older_version).
type ManifestFailure struct{ Reason string }

func (f *ManifestFailure) Error() string { return "the signed manifest check failed: " + f.Reason }

// Refusal gives the lifecycle core its reason for the refused state.
func (f *ManifestFailure) Refusal() string { return f.Reason }

// verifyManifest checks the Ed25519 signed manifest of the asset next to the SHA-256 and the
// build provenance: the manifest must exist, carry a good signature, be unexpired, name this
// release, and hold the SHA-256 of the file that was downloaded. Without RequireManifest it does nothing.
func (m *Manager) verifyManifest(ctx context.Context, rel Release, asset, file string) error {
	if !m.o.RequireManifest {
		return nil
	}
	raw, err := m.manifestBytes(ctx, rel, asset)
	if err != nil {
		return &ManifestFailure{Reason: lifecycle.BadSignature} // no manifest is no signature
	}
	mf, bad := lifecycle.ParseManifest(raw, m.o.Key, "", float64(m.o.Now().Unix()), m.o.Current)
	if bad != "" {
		return &ManifestFailure{Reason: bad}
	}
	sum, err := FileSHA256(file)
	if err != nil {
		return err
	}
	want, ok := mf.For(TargetKey(asset))
	if !ok || mf.Version != rel.Version || want.SHA256 != sum {
		return &ManifestFailure{Reason: "bad_hash"}
	}
	return nil
}

// TargetKey is the key of an asset in the manifest's assets table: <os>-<arch>, such as win-x64 or
// win-arm64, read from the lifecycle name (Crsp-0.1.22-win-arm64.msi) or the old names
// (crsp-windows-arm64.msi, crsp-windows-0.1.22-arm64.msi). Empty when the name is none of them.
func TargetKey(asset string) string {
	if plain := plainFromRelease(asset); plain != "" {
		asset = plain
	}
	for _, ext := range []string{".tar.gz", ".zip", ".msi", ".pkg", ".deb", ".rpm"} {
		base, ok := strings.CutSuffix(asset, ext)
		if !ok {
			continue
		}
		parts := strings.Split(base, "-")
		if len(parts) < 3 || parts[0] != "crsp" {
			return ""
		}
		osName := map[string]string{"windows": "win", "darwin": "macos", "linux": "linux"}[parts[1]]
		arch := map[string]string{"amd64": "x64", "arm64": "arm64"}[parts[len(parts)-1]]
		if osName == "" || arch == "" {
			return ""
		}
		return osName + "-" + arch
	}
	return ""
}

// sharedManifest and its signature are the two files a release publishes (scripts/ci/release_manifest.py
// and release-sign.sh): the canonical manifest and the hex Ed25519 signature over its bytes.
const (
	sharedManifest = "manifest.json"
	sharedSig      = "manifest.json.sig"
)

// manifestBytes downloads the manifest asset of asset into the release's work folder and reads it.
func (m *Manager) manifestBytes(ctx context.Context, rel Release, asset string) ([]byte, error) {
	if _, ok := rel.Assets[sharedManifest]; ok {
		return m.sharedManifestBytes(ctx, rel)
	}
	name := ManifestName(asset)
	u, ok := rel.Assets[name]
	if !ok {
		return nil, fmt.Errorf("release %s has no %s", rel.Version, name)
	}
	if !m.o.AllowURL(u) {
		return nil, fmt.Errorf("refusing to download from %s: not a GitHub release address", u)
	}
	dir := filepath.Join(m.o.Dir, rel.Version)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	dst := filepath.Join(dir, name)
	if err := m.o.Fetch(ctx, u, dst); err != nil {
		return nil, err
	}
	return os.ReadFile(dst) //nolint:gosec // work folder
}

// fetchSmall downloads one release asset into the work folder and reads it.
func (m *Manager) fetchSmall(ctx context.Context, rel Release, name string) ([]byte, error) {
	u, ok := rel.Assets[name]
	if !ok {
		return nil, fmt.Errorf("release %s has no %s", rel.Version, name)
	}
	if !m.o.AllowURL(u) {
		return nil, fmt.Errorf("refusing to download from %s: not a GitHub release address", u)
	}
	dir := filepath.Join(m.o.Dir, rel.Version)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	dst := filepath.Join(dir, name)
	if err := m.o.Fetch(ctx, u, dst); err != nil {
		return nil, err
	}
	return os.ReadFile(dst) //nolint:gosec // work folder
}

// sharedManifestBytes reads manifest.json and manifest.json.sig and joins them into the signed
// envelope the lifecycle core verifies: {"manifest": <body>, "signature": "<hex>"}.
func (m *Manager) sharedManifestBytes(ctx context.Context, rel Release) ([]byte, error) {
	body, err := m.fetchSmall(ctx, rel, sharedManifest)
	if err != nil {
		return nil, err
	}
	sig, err := m.fetchSmall(ctx, rel, sharedSig)
	if err != nil {
		return nil, err
	}
	return json.Marshal(struct {
		Manifest  json.RawMessage `json:"manifest"`
		Signature string          `json:"signature"`
	}{body, strings.TrimSpace(string(sig))})
}

// LatestManifest returns the signed manifest of the newest release for this machine, unverified,
// and the release it belongs to. The lifecycle service verifies it.
func (m *Manager) LatestManifest(ctx context.Context) ([]byte, Release, error) {
	rel, err := m.checkOnly(ctx)
	if err != nil {
		return nil, Release{}, err
	}
	asset := PickAsset(rel, AssetFor(m.o.InstallKind, m.o.GOOS, m.o.GOARCH))
	raw, err := m.manifestBytes(ctx, rel, asset)
	return raw, rel, err
}

// prepareOrCached uses the download PrepareVersion verified, when it is for this release.
func (m *Manager) prepareOrCached(ctx context.Context, rel Release) (string, error) {
	m.mu.Lock()
	p := m.prepared
	m.mu.Unlock()
	if p != nil && p.version == rel.Version {
		if _, err := os.Stat(p.file); err == nil {
			return p.file, nil
		}
	}
	return m.prepare(ctx, rel)
}

// PrepareVersion downloads and verifies the release (SHA256SUMS, provenance, signed manifest)
// and returns the SHA-256 of the package. The install then reuses the file.
func (m *Manager) PrepareVersion(ctx context.Context, version string) (string, error) {
	m.mu.Lock()
	rel := m.latest
	m.mu.Unlock()
	if rel.Version != version {
		return "", fmt.Errorf("release %s is not the newest known release", version)
	}
	file, err := m.prepare(ctx, rel)
	if err != nil {
		return "", err
	}
	m.mu.Lock()
	m.prepared = &preparedFile{version: version, file: file}
	m.mu.Unlock()
	return FileSHA256(file)
}
