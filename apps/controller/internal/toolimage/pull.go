package toolimage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"strings"
)

// ErrDigestMismatch is returned when pulled bytes do not hash to their digest.
var ErrDigestMismatch = errors.New("tool image bytes do not match their digest")

// MaxLayerBytes caps one layer read, so a hostile registry cannot fill the disk.
const MaxLayerBytes = 8 << 30

// Puller reads an image from a registry. The real one speaks the registry
// protocol; tests use a fake.
type Puller interface {
	// Manifest returns the raw manifest bytes of the digest.
	Manifest(ctx context.Context, ref Ref) ([]byte, error)
	// Blob returns the blob with the digest.
	Blob(ctx context.Context, ref Ref, digest string) (io.ReadCloser, error)
}

// Descriptor is one layer in a manifest.
type Descriptor struct {
	MediaType string `json:"mediaType"`
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
}

// Manifest is the part of an OCI image manifest this package reads.
type Manifest struct {
	SchemaVersion int          `json:"schemaVersion"`
	MediaType     string       `json:"mediaType"`
	Layers        []Descriptor `json:"layers"`
}

func sumHex(b []byte) string {
	s := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(s[:])
}

// FetchManifest pulls the manifest and checks it against the reference digest.
// An image index is refused: the reference must name one image.
func FetchManifest(ctx context.Context, p Puller, ref Ref) (Manifest, error) {
	raw, err := p.Manifest(ctx, ref)
	if err != nil {
		return Manifest{}, fmt.Errorf("pull manifest: %w", err)
	}
	if sumHex(raw) != ref.Digest {
		return Manifest{}, fmt.Errorf("manifest of %s: %w", ref, ErrDigestMismatch)
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return Manifest{}, fmt.Errorf("manifest of %s: %w", ref, err)
	}
	if strings.Contains(m.MediaType, "index") || strings.Contains(m.MediaType, "manifest.list") {
		return Manifest{}, fmt.Errorf("%s is an image index; pin the digest of one image", ref)
	}
	if len(m.Layers) == 0 {
		return Manifest{}, fmt.Errorf("%s has no layers", ref)
	}
	for _, l := range m.Layers {
		if !strings.HasPrefix(l.Digest, "sha256:") || len(l.Digest) != len("sha256:")+64 {
			return Manifest{}, fmt.Errorf("%s: layer digest %q is not sha256", ref, l.Digest)
		}
	}
	return m, nil
}

// layerStream is a layer being read: it counts and hashes every byte, and
// Check says whether the whole blob matched its descriptor. The unpacker
// writes into a staging folder and deletes it when Check fails, so a bad layer
// never reaches the disk the VM sees.
type layerStream struct {
	rc   io.ReadCloser
	h    hash.Hash
	n    int64
	want Descriptor
	r    io.Reader
}

func openLayer(ctx context.Context, p Puller, ref Ref, d Descriptor) (*layerStream, error) {
	rc, err := p.Blob(ctx, ref, d.Digest)
	if err != nil {
		return nil, fmt.Errorf("pull layer %s: %w", d.Digest, err)
	}
	s := &layerStream{rc: rc, h: sha256.New(), want: d}
	s.r = io.TeeReader(io.LimitReader(rc, MaxLayerBytes+1), s.h)
	return s, nil
}

func (s *layerStream) Read(b []byte) (int, error) {
	n, err := s.r.Read(b)
	s.n += int64(n)
	return n, err
}

// Check drains what is left and compares size and hash with the descriptor.
func (s *layerStream) Check() error {
	defer func() { _ = s.rc.Close() }()
	if _, err := io.Copy(io.Discard, s); err != nil {
		return fmt.Errorf("read layer %s: %w", s.want.Digest, err)
	}
	got := "sha256:" + hex.EncodeToString(s.h.Sum(nil))
	if s.n > MaxLayerBytes || (s.want.Size > 0 && s.n != s.want.Size) || got != s.want.Digest {
		return fmt.Errorf("layer %s: %w", s.want.Digest, ErrDigestMismatch)
	}
	return nil
}
