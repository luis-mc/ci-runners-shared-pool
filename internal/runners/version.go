package runners

import (
	"bytes"
	"crypto/sha256"
	_ "embed" // the pins file
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/luis-mc/ci-runners-shared-pool/internal/addresses"
	"io"
	"os"
	"regexp"
	"strings"
)

// Pin is one pinned GitHub runner download (runners R5.37).
type Pin struct {
	OS      string
	Arch    string
	Version string
	SHA256  string
}

// ErrBadHash is returned when a pin hash is not a real SHA-256.
var ErrBadHash = errors.New("runner hash is not a valid SHA-256")

// ErrHashMismatch is returned when a download does not match its pin.
var ErrHashMismatch = errors.New("runner download hash does not match the pinned hash")

// ValidSHA256 is false for a value that is not 64 hex characters or is one
// repeated character (port of dev-standards DS R5.14).
func ValidSHA256(h string) bool {
	if len(h) != 64 {
		return false
	}
	if _, err := hex.DecodeString(h); err != nil {
		return false
	}
	return strings.Count(h, string(h[0])) != len(h)
}

// pinsJSON is the one pins file (runners R5.37, R5.79): the runner version and
// SHA-256 per OS and architecture, and the container images by digest. An
// update of a pin is a reviewed change of this file and nothing else.
//
//go:embed pins.json
var pinsJSON []byte

type pinsFile struct {
	Runner []struct {
		OS      string `json:"os"`
		Arch    string `json:"arch"`
		Version string `json:"version"`
		SHA256  string `json:"sha256"`
	} `json:"runner"`
	Images map[string]string `json:"images"`
}

// loadPins reads and checks a pins file. A runner hash that is not a real
// SHA-256, a repeated OS/arch pair, or an image that is not a digest
// reference refuses the whole file, so a half-filled pin never downloads.
func loadPins(data []byte) ([]Pin, map[string]string, error) {
	var f pinsFile
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return nil, nil, fmt.Errorf("pins file: %w", err)
	}
	seen := map[string]bool{}
	var out []Pin
	for _, r := range f.Runner {
		key := r.OS + "/" + r.Arch
		if seen[key] {
			return nil, nil, fmt.Errorf("pins file: %s is pinned twice", key)
		}
		seen[key] = true
		if r.Version == "" {
			return nil, nil, fmt.Errorf("pins file: %s has no version", key)
		}
		if !ValidSHA256(r.SHA256) {
			return nil, nil, fmt.Errorf("pins file: %s: %w", key, ErrBadHash)
		}
		out = append(out, Pin{OS: r.OS, Arch: r.Arch, Version: r.Version, SHA256: strings.ToLower(r.SHA256)})
	}
	for name, ref := range f.Images {
		if !ValidDigestRef(ref) {
			return nil, nil, fmt.Errorf("pins file: image %s: %w", name, ErrNotDigestRef)
		}
	}
	return out, f.Images, nil
}

var (
	pins       []Pin
	imagePins  map[string]string
	pinsLoadEr error
)

func init() { pins, imagePins, pinsLoadEr = loadPins(pinsJSON) }

// ErrNotDigestRef is returned for an image reference without a sha256 digest.
var ErrNotDigestRef = errors.New("image reference is not pinned by digest (name@sha256:<64 hex>)")

var digestRef = regexp.MustCompile(`^[a-z0-9][a-zA-Z0-9._/:-]*@sha256:([0-9a-f]{64})$`)

// ValidDigestRef is true for name[:tag]@sha256:<64 lower-case hex>. A bare tag
// is never valid: a tag can move, a digest cannot (runners R5.79).
func ValidDigestRef(ref string) bool {
	m := digestRef.FindStringSubmatch(ref)
	return m != nil && ValidSHA256(m[1])
}

// ImagePin returns the pinned digest reference of a named image.
func ImagePin(name string) (string, error) {
	if pinsLoadEr != nil {
		return "", pinsLoadEr
	}
	ref, ok := imagePins[name]
	if !ok {
		return "", fmt.Errorf("no pinned image %q", name)
	}
	return ref, nil
}

// PinFor returns the pin for an OS and architecture.
func PinFor(os, arch string) (Pin, error) {
	if pinsLoadEr != nil {
		return Pin{}, pinsLoadEr
	}
	for _, p := range AllPins() {
		if p.OS == os && p.Arch == arch {
			if !ValidSHA256(p.SHA256) {
				return Pin{}, fmt.Errorf("%s/%s: %w", os, arch, ErrBadHash)
			}
			return p, nil
		}
	}
	return Pin{}, fmt.Errorf("no pinned runner for %s/%s", os, arch)
}

// DownloadURL is the release asset for a pin.
// The base comes from CRSP_RUNNER_DOWNLOAD_URL (addresses.Set); a zero
// Addresses takes the github.com default.
func (p Pin) DownloadURL(a addresses.Set) string {
	if a.RunnerDownloadURL == "" {
		a = addresses.Defaults()
	}
	ext := "tar.gz"
	osName := p.OS
	if p.OS == "windows" {
		ext = "zip"
		osName = "win"
	}
	if p.OS == "macos" {
		osName = "osx"
	}
	return a.RunnerAsset(p.Version, osName, p.Arch, ext)
}

// VerifyFile checks the file against want. A file that does not match is
// deleted and refused (runners R5.37, section 8).
func VerifyFile(path, want string) error {
	if !ValidSHA256(want) {
		_ = os.Remove(path)
		return ErrBadHash
	}
	f, err := os.Open(path) //nolint:gosec // path is the agent's own download.
	if err != nil {
		return err
	}
	h := sha256.New()
	_, copyErr := io.Copy(h, f)
	_ = f.Close()
	if copyErr != nil {
		return copyErr
	}
	if !strings.EqualFold(hex.EncodeToString(h.Sum(nil)), want) {
		_ = os.Remove(path)
		return ErrHashMismatch
	}
	return nil
}
