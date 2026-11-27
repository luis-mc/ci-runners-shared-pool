package update

import (
	"crypto/ed25519"
	_ "embed" // the release public key

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/update/lifecycle"
)

//go:embed keys/release-ed25519.pub
var releaseKeyPEM []byte

// ReleaseKey is the Ed25519 key that signs release manifests, from keys/release-ed25519.pub.
// With no key committed yet it returns lifecycle.ErrNoKey, and every manifest is refused.
func ReleaseKey() (ed25519.PublicKey, error) { return lifecycle.ParsePublicKey(releaseKeyPEM) }
