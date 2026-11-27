package toolimage

import (
	"errors"
	"fmt"
	"strings"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runners"
)

// ErrNotDigest is returned for a reference that is not pinned by digest.
var ErrNotDigest = errors.New("tool image must be referenced by digest (name@sha256:<64 hex>), not by tag")

// Ref is a tool image reference pinned by digest.
type Ref struct {
	// Name is the repository part, with an optional tag kept for the reader.
	Name string
	// Digest is "sha256:<64 hex>".
	Digest string
}

// ParseRef accepts only name[:tag]@sha256:<64 lower-case hex>.
func ParseRef(s string) (Ref, error) {
	if !runners.ValidDigestRef(s) {
		return Ref{}, fmt.Errorf("%q: %w", s, ErrNotDigest)
	}
	i := strings.LastIndex(s, "@")
	return Ref{Name: s[:i], Digest: s[i+1:]}, nil
}

// String is the reference text.
func (r Ref) String() string { return r.Name + "@" + r.Digest }

// Hex is the digest without the algorithm prefix.
func (r Ref) Hex() string { return strings.TrimPrefix(r.Digest, "sha256:") }
