package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
)

// randomBytes panics only if the OS has no randomness, which is not a state
// the controller can run in.
func randomBytes(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("auth: no randomness: %v", err))
	}
	return b
}

// RandomToken returns n random bytes, URL-safe.
func RandomToken(n int) string { return base64.RawURLEncoding.EncodeToString(randomBytes(n)) }

// Hash is the SHA-256 hex of a secret value; the database keeps only this.
func Hash(v string) string {
	sum := sha256.Sum256([]byte(v))
	return hex.EncodeToString(sum[:])
}

func equal(a, b string) bool { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }

// newCode makes a 128-bit code in groups of four, for a human to type:
// ABCD-EFGH-...
func newCode() string {
	s := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(randomBytes(16))
	var parts []string
	for i := 0; i < len(s); i += 4 {
		parts = append(parts, s[i:min(i+4, len(s))])
	}
	return strings.Join(parts, "-")
}

// normalizeCode lets a person type a code in lower case or without dashes.
func normalizeCode(c string) string {
	c = strings.ToUpper(strings.TrimSpace(c))
	c = strings.ReplaceAll(c, "-", "")
	c = strings.ReplaceAll(c, " ", "")
	var parts []string
	for i := 0; i < len(c); i += 4 {
		parts = append(parts, c[i:min(i+4, len(c))])
	}
	return strings.Join(parts, "-")
}
