package update

import (
	"crypto/ed25519"
	"os"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/internal/update/lifecycle"
)

// The committed file is the release key the updater trusts: it must parse as an Ed25519 key.
func TestCommittedReleaseKeyIsAValidEd25519Key(t *testing.T) {
	raw, err := os.ReadFile("keys/release-ed25519.pub")
	if err != nil {
		t.Fatal(err)
	}
	pub, err := lifecycle.ParsePublicKey(raw)
	if err != nil {
		t.Fatalf("the committed release key does not parse: %v", err)
	}
	if len(pub) != ed25519.PublicKeySize {
		t.Fatalf("key length %d", len(pub))
	}
	embedded, err := ReleaseKey()
	if err != nil || !pub.Equal(embedded) {
		t.Fatalf("the embedded key differs from the file: %v", err)
	}
}
