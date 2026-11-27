package lifecycle

import (
	"bytes"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"strconv"
	"strings"
)

// Manifest is the signed release manifest (contract, "Fake server").
type Manifest struct {
	Product   string
	Channel   string
	Version   string
	Required  bool
	ExpiresAt float64
	URL       string
	SHA256    string
	// Assets is one package per target, keyed <os>-<arch> (win-x64, win-arm64). URL and SHA256 are
	// the contract's default package. Empty for a manifest that lists one package only.
	Assets map[string]Asset
}

// Asset is a package of the manifest.
type Asset struct{ URL, SHA256 string }

// For returns the package for a target key such as win-arm64. When the manifest has an assets table
// the target must be in it (an arm64 machine never takes the x64 package); without one the default
// package is the only package.
func (m *Manifest) For(target string) (Asset, bool) {
	if len(m.Assets) == 0 {
		return Asset{URL: m.URL, SHA256: m.SHA256}, true
	}
	a, ok := m.Assets[target]
	return a, ok
}

// Refusal reasons, as the audit detail shows them.
const (
	BadSignature = "bad_signature"
	Expired      = "expired"
	OlderVersion = "older_version"
)

// ErrNoKey says no release key is embedded: every manifest is then refused.
var ErrNoKey = errors.New("no release key is embedded")

// ParsePublicKey reads an Ed25519 public key from PEM (PKIX). Text around the PEM block is ignored,
// so the committed file may carry a comment.
func ParsePublicKey(pemText []byte) (ed25519.PublicKey, error) {
	blk, _ := pem.Decode(pemText)
	if blk == nil {
		return nil, ErrNoKey
	}
	k, err := x509.ParsePKIXPublicKey(blk.Bytes)
	if err != nil {
		return nil, err
	}
	pub, ok := k.(ed25519.PublicKey)
	if !ok {
		return nil, errors.New("the release key is not Ed25519")
	}
	return pub, nil
}

// Canonical is the signed form: sorted keys, compact separators. Manifests are ASCII.
func Canonical(m map[string]interface{}) ([]byte, error) {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	if err := e.Encode(m); err != nil {
		return nil, err
	}
	return bytes.TrimRight(b.Bytes(), "\n"), nil
}

// VersionLess reports whether a is an older dotted version than b.
func VersionLess(a, b string) bool {
	pa, pb := strings.Split(strings.TrimPrefix(a, "v"), "."), strings.Split(strings.TrimPrefix(b, "v"), ".")
	for i := 0; i < len(pa) && i < len(pb); i++ {
		x, _ := strconv.Atoi(pa[i])
		y, _ := strconv.Atoi(pb[i])
		if x != y {
			return x < y
		}
	}
	return len(pa) < len(pb)
}

// ParseManifest verifies the signed manifest and returns it, or a refusal reason. A missing key,
// a missing or bad signature, an expired manifest and an older version are all refused.
func ParseManifest(raw []byte, pub ed25519.PublicKey, product string, now float64, current string) (*Manifest, string) {
	var env struct {
		Manifest  map[string]interface{} `json:"manifest"`
		Signature string                 `json:"signature"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&env); err != nil || env.Manifest == nil || len(pub) != ed25519.PublicKeySize {
		return nil, BadSignature
	}
	sig, err := hex.DecodeString(env.Signature)
	body, err2 := Canonical(env.Manifest)
	if err != nil || err2 != nil || len(sig) != ed25519.SignatureSize || !ed25519.Verify(pub, body, sig) {
		return nil, BadSignature
	}
	str := func(k string) string { s, _ := env.Manifest[k].(string); return s }
	exp, _ := env.Manifest["expires_at"].(json.Number).Float64()
	req, _ := env.Manifest["required"].(bool)
	m := &Manifest{Product: str("product"), Channel: str("channel"), Version: str("version"), Required: req, ExpiresAt: exp, URL: str("url"), SHA256: str("sha256")}
	if as, ok := env.Manifest["assets"].(map[string]interface{}); ok {
		m.Assets = map[string]Asset{}
		for k, v := range as {
			if o, ok := v.(map[string]interface{}); ok {
				u, _ := o["url"].(string)
				h, _ := o["sha256"].(string)
				m.Assets[k] = Asset{URL: u, SHA256: h}
			}
		}
	}
	if product != "" && m.Product != product {
		return nil, BadSignature
	}
	if m.ExpiresAt <= now {
		return nil, Expired
	}
	if VersionLess(m.Version, current) {
		return nil, OlderVersion
	}
	return m, ""
}
