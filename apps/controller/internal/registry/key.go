package registry

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// KeyLen is the length of an image key in hex characters (registry R5.21).
const KeyLen = 12

// Key is the content key of one image: the first 12 hex characters of the
// SHA-256 of the canonical JSON of its KeyInput. The same key means the same
// image on every machine, so a machine that needs key K pulls K instead of
// building it (registry R5.21, RES20 5.1, owner decision D19).
type Key string

// String returns the key.
func (k Key) String() string { return string(k) }

// Short is the first 6 characters, the form lists show.
func (k Key) Short() string {
	if len(k) < 6 {
		return string(k)
	}
	return string(k[:6])
}

var keyShape = regexp.MustCompile(`^[0-9a-f]{12}$`)

// Valid reports whether k has the shape of a key: 12 lowercase hex characters.
func (k Key) Valid() bool { return keyShape.MatchString(string(k)) }

// Toolchain is one toolchain baked into the image (the tool cache layer of an
// image profile, RES19 B5).
type Toolchain struct {
	Tool    string `json:"tool"`
	Version string `json:"version"`
}

// KeyInput is everything that makes an image different (registry R5.21).
// Services (sidecars) are not part of it.
type KeyInput struct {
	// Profile is the image profile id (IMG3), such as "node-web".
	Profile string `json:"profile"`
	// StackHash is the 64 lowercase hex characters StackHash returns.
	StackHash string `json:"stack_hash"`
	// Arch is the OCI architecture: "amd64" or "arm64". The agent's "x64"
	// becomes "amd64" (OCIArch).
	Arch string `json:"arch"`
	// BaseDigest is the manifest digest of the base image, "sha256:<64 hex>".
	BaseDigest string `json:"base_digest"`
	// Pins are the pinned downloads of the image (internal/runners/pins.json
	// entries and layer pins), name to value. A nil map is the same as empty.
	Pins map[string]string `json:"pins"`
	// Toolchains are the baked-in toolchains. Order and repeats do not matter.
	Toolchains []Toolchain `json:"toolchains"`
}

var (
	hex64      = regexp.MustCompile(`^[0-9a-f]{64}$`)
	digestForm = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

// ErrBadKeyInput is a KeyInput that cannot give a key.
var ErrBadKeyInput = errors.New("registry: bad key input")

// OCIArch maps the agent's arch names to the OCI ones: x64 and amd64 give
// amd64, arm64 and aarch64 give arm64. Anything else is returned as given.
func OCIArch(a string) string {
	switch strings.ToLower(strings.TrimSpace(a)) {
	case "x64", "amd64", "x86_64":
		return "amd64"
	case "arm64", "aarch64":
		return "arm64"
	}
	return a
}

// canonicalKeyInput is the exact shape that is hashed. Keys are written in
// this struct order, which is also the byte order of the key names, so the
// JSON object has sorted keys.
type canonicalKeyInput struct {
	Arch       string            `json:"arch"`
	BaseDigest string            `json:"base_digest"`
	Pins       map[string]string `json:"pins"`
	Profile    string            `json:"profile"`
	StackHash  string            `json:"stack_hash"`
	Toolchains []Toolchain       `json:"toolchains"`
}

// CanonicalJSON returns the bytes the key hashes (registry R5.21):
//   - one JSON object, no whitespace, keys in byte order: arch, base_digest,
//     pins, profile, stack_hash, toolchains;
//   - arch through OCIArch; strings trimmed of surrounding spaces;
//   - pins an object with keys in byte order ({} when empty);
//   - toolchains an array of {"tool","version"} sorted by tool then version,
//     repeats dropped ([] when empty);
//   - no HTML escaping (<, > and & are written as is), no trailing newline.
func CanonicalJSON(in KeyInput) ([]byte, error) {
	c := canonicalKeyInput{
		Arch:       OCIArch(strings.TrimSpace(in.Arch)),
		BaseDigest: strings.TrimSpace(in.BaseDigest),
		Pins:       map[string]string{},
		Profile:    strings.TrimSpace(in.Profile),
		StackHash:  strings.TrimSpace(in.StackHash),
		Toolchains: []Toolchain{},
	}
	if c.Profile == "" {
		return nil, fmt.Errorf("%w: no profile", ErrBadKeyInput)
	}
	if c.Arch != "amd64" && c.Arch != "arm64" {
		return nil, fmt.Errorf("%w: arch %q is not amd64 or arm64", ErrBadKeyInput, in.Arch)
	}
	if !hex64.MatchString(c.StackHash) {
		return nil, fmt.Errorf("%w: stack hash is not 64 lowercase hex characters", ErrBadKeyInput)
	}
	if !digestForm.MatchString(c.BaseDigest) {
		return nil, fmt.Errorf("%w: base digest is not sha256:<64 hex>", ErrBadKeyInput)
	}
	for k, v := range in.Pins {
		k = strings.TrimSpace(k)
		if k == "" {
			return nil, fmt.Errorf("%w: a pin has no name", ErrBadKeyInput)
		}
		c.Pins[k] = strings.TrimSpace(v)
	}
	seen := map[Toolchain]bool{}
	for _, t := range in.Toolchains {
		t = Toolchain{Tool: strings.TrimSpace(t.Tool), Version: strings.TrimSpace(t.Version)}
		if t.Tool == "" {
			return nil, fmt.Errorf("%w: a toolchain has no tool", ErrBadKeyInput)
		}
		if !seen[t] {
			seen[t] = true
			c.Toolchains = append(c.Toolchains, t)
		}
	}
	sort.Slice(c.Toolchains, func(i, j int) bool {
		if c.Toolchains[i].Tool != c.Toolchains[j].Tool {
			return c.Toolchains[i].Tool < c.Toolchains[j].Tool
		}
		return c.Toolchains[i].Version < c.Toolchains[j].Version
	})
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(c); err != nil { // encoding/json writes map keys in byte order
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

// ComputeKey returns the image key of in: hex(sha256(CanonicalJSON(in)))[:12].
func ComputeKey(in KeyInput) (Key, error) {
	b, err := CanonicalJSON(in)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return Key(hex.EncodeToString(sum[:])[:KeyLen]), nil
}

// StackFile is one stack file the scan read at the run's commit (IMG2).
type StackFile struct {
	// Path is relative to the repository root, with forward slashes and no
	// leading "./" or "/".
	Path    string
	Content []byte
}

// StackHash returns the stack hash of files (registry R5.22): the lowercase
// hex SHA-256 over, for each file sorted by Path in byte order,
//
//	<path> NUL <hex sha256 of the content with CRLF turned into LF> LF
//
// Only "\r\n" becomes "\n"; a lone "\r" stays. No files gives the SHA-256 of
// nothing. Two files with the same path are an error.
func StackHash(files []StackFile) (string, error) {
	type entry struct{ path, sum string }
	entries := make([]entry, 0, len(files))
	seen := map[string]bool{}
	for _, f := range files {
		p := f.Path
		if p == "" || strings.HasPrefix(p, "/") || strings.HasPrefix(p, "./") || strings.Contains(p, `\`) {
			return "", fmt.Errorf("%w: stack file path %q is not a clean repository path", ErrBadKeyInput, p)
		}
		if seen[p] {
			return "", fmt.Errorf("%w: stack file %q twice", ErrBadKeyInput, p)
		}
		seen[p] = true
		sum := sha256.Sum256(bytes.ReplaceAll(f.Content, []byte("\r\n"), []byte("\n")))
		entries = append(entries, entry{p, hex.EncodeToString(sum[:])})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].path < entries[j].path })
	h := sha256.New()
	for _, e := range entries {
		h.Write([]byte(e.path))
		h.Write([]byte{0})
		h.Write([]byte(e.sum))
		h.Write([]byte{'\n'})
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Repository names inside the registry (registry R5.4). Every name starts
// with "crsp/". The tag of an image is its Key.
const (
	// NamePrefix is the root of every repository name.
	NamePrefix = "crsp/"
	// IncomingPrefix is the staging root: a machine pushes only below
	// crsp/incoming/<its machine id>/.
	IncomingPrefix = "crsp/incoming/"
)

// StdName is the repository of the standard image of a profile:
// crsp/std/<profile>.
func StdName(profile string) string { return NamePrefix + "std/" + profile }

// CustomName is the repository of a custom image of one GitHub repository:
// crsp/custom/<owner>-<repo>/<profile>, all lower case.
func CustomName(owner, repo, profile string) string {
	return NamePrefix + "custom/" + strings.ToLower(owner) + "-" + strings.ToLower(repo) + "/" + profile
}

// IncomingName is where machine pushes the image it built before promotion:
// crsp/incoming/<machine id>/<target name without the crsp/ prefix>, for
// example crsp/incoming/m_7f3a/std/node-web.
func IncomingName(machineID, target string) string {
	return IncomingPrefix + machineID + "/" + strings.TrimPrefix(target, NamePrefix)
}
