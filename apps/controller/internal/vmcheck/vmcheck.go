// Package vmcheck is the Go port of the logic and data of the dev-standards
// scripts/delivery/vm_check.py and vm-images.json (CRSP26, dev-standards
// D302): the catalogue of expected VM images, the checksum files that say
// which image is current, the cache keys, the readiness report of a `kvm`
// runner's host, the check that the cached image matches the published one,
// and the prune plan.
//
// Booting the VMs (qemu, cloud-init seeds, ssh) is not part of this port; it
// waits for the KVM backend (runners spec, milestone 6).
package vmcheck

import (
	"context"
	"crypto/sha256"
	_ "embed" // the catalogue
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/addresses"
)

//go:embed vm-images.json
var imagesJSON []byte

// Distro is one expected image: where it is published and how to find it.
type Distro struct {
	BaseURL        string `json:"base_url"`
	ChecksumURL    string `json:"checksum_url"`
	ChecksumFormat string `json:"checksum_format"` // bsd or sums
	Algo           string `json:"algo"`
	Pattern        string `json:"pattern"`
	User           string `json:"user"`
}

// Catalogue is the expected images by distro name.
type Catalogue map[string]Distro

// Load reads the built-in catalogue. Each extra JSON document (same shape,
// a "distros" object) adds or replaces distros.
func Load(extra ...[]byte) (Catalogue, error) {
	c := Catalogue{}
	for _, doc := range append([][]byte{imagesJSON}, extra...) {
		var f struct {
			Distros map[string]Distro `json:"distros"`
		}
		if err := json.Unmarshal(doc, &f); err != nil {
			return nil, fmt.Errorf("vm images: %w", err)
		}
		for k, v := range f.Distros {
			c[k] = v
		}
	}
	for k, d := range c {
		if err := d.validate(); err != nil {
			return nil, fmt.Errorf("vm images: %s: %w", k, err)
		}
	}
	return c, nil
}

// LoadOnly reads one catalogue document without the built-in one.
func LoadOnly(doc []byte) (Catalogue, error) {
	var f struct {
		Distros map[string]Distro `json:"distros"`
	}
	if err := json.Unmarshal(doc, &f); err != nil {
		return nil, fmt.Errorf("vm images: %w", err)
	}
	return Catalogue(f.Distros), nil
}

func (d Distro) validate() error {
	switch {
	case d.BaseURL == "" || d.ChecksumURL == "" || d.Pattern == "" || d.User == "":
		return errors.New("base_url, checksum_url, pattern and user are required")
	case d.ChecksumFormat != "bsd" && d.ChecksumFormat != "sums":
		return fmt.Errorf("checksum_format %q is not bsd or sums", d.ChecksumFormat)
	case d.Algo != "sha256" && d.Algo != "sha512":
		return fmt.Errorf("algo %q is not sha256 or sha512", d.Algo)
	}
	return nil
}

// Mirrored returns the catalogue with every download address moved to the VM
// image mirror of a (CRSP_VM_IMAGE_MIRROR, operations R5.96). Without a mirror
// it is c itself, so the default is unchanged. c is never modified.
func (c Catalogue) Mirrored(a addresses.Set) Catalogue {
	if a.VMImageMirror == "" {
		return c
	}
	out := make(Catalogue, len(c))
	for name, d := range c {
		d.BaseURL = a.VMImageURL(name, d.BaseURL)
		d.ChecksumURL = a.VMImageURL(name, d.ChecksumURL)
		out[name] = d
	}
	return out
}

// Names lists the distros, sorted.
func (c Catalogue) Names() []string {
	n := make([]string, 0, len(c))
	for k := range c {
		n = append(n, k)
	}
	sort.Strings(n)
	return n
}

// Get returns a distro or an error that lists the known ones.
func (c Catalogue) Get(name string) (Distro, error) {
	d, ok := c[name]
	if !ok {
		return Distro{}, fmt.Errorf("unknown distro %s (known: %s)", name, strings.Join(c.Names(), ", "))
	}
	return d, nil
}

var (
	bsdLine  = regexp.MustCompile(`^SHA\d+ \((.+)\) = ([0-9a-fA-F]+)$`)
	sumsLine = regexp.MustCompile(`^([0-9a-fA-F]+)\s+\*?(.+)$`)
	hrefRe   = regexp.MustCompile(`href="([^"/?]+)"`)
)

// Image is the file a checksum file names for a pattern.
type Image struct{ Name, Digest string }

// ParseChecksums returns the last file in the checksum text whose name
// matches pattern (a shell glob), with its lower-case digest.
func ParseChecksums(text, format, pattern string) (Image, error) {
	var found *Image
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		var name, digest string
		switch format {
		case "bsd":
			if m := bsdLine.FindStringSubmatch(line); m != nil {
				name, digest = m[1], m[2]
			}
		default:
			if m := sumsLine.FindStringSubmatch(line); m != nil {
				name, digest = m[2], m[1]
			}
		}
		if name == "" {
			continue
		}
		if ok, _ := path.Match(pattern, name); ok {
			found = &Image{Name: name, Digest: strings.ToLower(digest)}
		}
	}
	if found == nil {
		return Image{}, fmt.Errorf("no image matching %s in the checksum file", pattern)
	}
	return *found, nil
}

// ResolveChecksumURL turns a checksum URL with a '*' in its last part into the
// newest file of that name in the directory listing at its base.
func ResolveChecksumURL(url, listing string) (string, error) {
	if !strings.Contains(url, "*") {
		return url, nil
	}
	i := strings.LastIndex(url, "/")
	base, last := url[:i], url[i+1:]
	seen := map[string]bool{}
	var hits []string
	for _, m := range hrefRe.FindAllStringSubmatch(listing, -1) {
		if seen[m[1]] {
			continue
		}
		seen[m[1]] = true
		if ok, _ := path.Match(last, m[1]); ok {
			hits = append(hits, m[1])
		}
	}
	if len(hits) == 0 {
		return "", fmt.Errorf("no file matching %s in %s/", last, base)
	}
	sort.Strings(hits)
	return base + "/" + hits[len(hits)-1], nil
}

// Short is the 16 hex digit key of the parts (vm_check.py `short`).
func Short(parts ...string) string {
	h := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	return fmt.Sprintf("%x", h)[:16]
}

// BaseKey names a prepared base disk: the image digest and the sorted packages.
func BaseKey(checksum string, packages []string) string {
	p := append([]string(nil), packages...)
	sort.Strings(p)
	return Short("base", checksum, strings.Join(p, " "))
}

// SnapKey names a snapshot disk: the base, the qemu version, the CPU model and
// the memory and CPU counts, so a snapshot is never resumed on other hardware.
func SnapKey(baseKey, qemuVersion, cpuModel string, memMB, cpus int) string {
	return Short("snap", baseKey, qemuVersion, cpuModel, fmt.Sprint(memMB), fmt.Sprint(cpus))
}

// CountAVC picks the SELinux denial lines out of an audit log.
func CountAVC(text string) []string {
	var out []string
	for _, l := range strings.Split(text, "\n") {
		low := strings.ToLower(l)
		if strings.Contains(low, "avc:") && strings.Contains(low, "denied") {
			out = append(out, l)
		}
	}
	return out
}

// SELinuxVerdict is "" when all is well, else a plain reason.
func SELinuxVerdict(mode string, avc []string) string {
	mode = strings.TrimSpace(mode)
	if mode != "Enforcing" {
		if mode == "" {
			mode = "unknown"
		}
		return fmt.Sprintf("SELinux is %s, not Enforcing", mode)
	}
	if len(avc) > 0 {
		return fmt.Sprintf("%d AVC denial(s) since boot:\n%s", len(avc), strings.Join(avc, "\n"))
	}
	return ""
}

// Status is how a cached image compares with the published one.
type Status string

// Statuses of Verify.
const (
	Current Status = "current" // the cached image is the published one
	Stale   Status = "stale"   // a newer image is published
	Missing Status = "missing" // nothing cached for the distro
	Offline Status = "offline" // the checksum file could not be read
)

// Result is the outcome of Verify for one distro.
type Result struct {
	Distro    string
	Status    Status
	Published string // digest of the published image, when known
	Cached    string // digest of the newest cached image, when any
	Detail    string
}

// Source is what Verify reads: the network and the cache folder.
type Source interface {
	// Text fetches a URL as text.
	Text(ctx context.Context, url string) (string, error)
	// CachedDigest is the digest of the newest cached image of the distro,
	// or "" when none (the .sum files of the cache).
	CachedDigest(distro string) (string, error)
}

// Verify checks that the image cached for a distro is the one the vendor
// publishes now. A distro that cannot be checked offline reports Offline with
// the cached digest, which is what vm_check.py uses to keep working.
func Verify(ctx context.Context, src Source, name string, d Distro) (Result, error) {
	cached, err := src.CachedDigest(name)
	if err != nil {
		return Result{}, err
	}
	res := Result{Distro: name, Cached: cached}
	url := d.ChecksumURL
	if strings.Contains(url, "*") {
		i := strings.LastIndex(url, "/")
		listing, err := src.Text(ctx, url[:i+1])
		if err != nil {
			return offline(res, err), nil
		}
		if url, err = ResolveChecksumURL(url, listing); err != nil {
			return Result{}, err
		}
	}
	text, err := src.Text(ctx, url)
	if err != nil {
		return offline(res, err), nil
	}
	img, err := ParseChecksums(text, d.ChecksumFormat, d.Pattern)
	if err != nil {
		return Result{}, err
	}
	res.Published = img.Digest
	switch cached {
	case "":
		res.Status, res.Detail = Missing, "no image of "+name+" is cached; the first run downloads "+img.Name
	case img.Digest:
		res.Status, res.Detail = Current, name+" matches "+img.Name
	default:
		res.Status, res.Detail = Stale, name+" is behind "+img.Name+"; the next run downloads it"
	}
	return res, nil
}

func offline(r Result, err error) Result {
	r.Status = Offline
	r.Detail = "the checksum file could not be read: " + err.Error()
	return r
}

// Tools are the programs a kvm runner's host needs to boot a check VM.
var Tools = []string{"qemu-system-x86_64", "cloud-localds", "qemu-img", "ssh", "scp", "ssh-keygen"}

// Host is what Readiness reads from the machine.
type Host struct {
	KVMUsable     bool
	Tools         map[string]bool // tool name -> found
	CacheWritable bool
}

// Readiness lists what stops this host from running check VMs; empty means
// ready (vm_check.py `doctor`).
func Readiness(h Host) []string {
	var p []string
	if !h.KVMUsable {
		p = append(p, "/dev/kvm is not usable")
	}
	for _, t := range Tools {
		if !h.Tools[t] {
			p = append(p, t+" is missing")
		}
	}
	if !h.CacheWritable {
		p = append(p, "cache is not writable")
	}
	return p
}

var pruneRe = regexp.MustCompile(`^(.+)-([0-9a-f]{16})\.qcow2$`)

// File is one file of the VM cache.
type File struct {
	Path    string
	ModUnix int64
}

// PrunePlan says which cache files to remove: per distro in each kind folder,
// all but the newest keep, plus temporary files older than a day. kinds is
// the folder name of each file ("images", "bases", "snaps").
func PrunePlan(files map[string][]File, keep int, nowUnix int64) []string {
	var out []string
	for _, kind := range []string{"images", "bases", "snaps"} {
		groups := map[string][]File{}
		for _, f := range files[kind] {
			if m := pruneRe.FindStringSubmatch(filepath.Base(f.Path)); m != nil {
				groups[m[1]] = append(groups[m[1]], f)
			}
		}
		keys := make([]string, 0, len(groups))
		for k := range groups {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			g := groups[k]
			sort.Slice(g, func(i, j int) bool {
				if g[i].ModUnix != g[j].ModUnix {
					return g[i].ModUnix > g[j].ModUnix
				}
				return g[i].Path < g[j].Path
			})
			for _, f := range g[min(max(keep, 0), len(g)):] {
				out = append(out, f.Path)
			}
		}
	}
	for _, f := range files["tmp"] {
		if strings.Contains(filepath.Base(f.Path), ".tmp-") && f.ModUnix < nowUnix-86400 {
			out = append(out, f.Path)
		}
	}
	return out
}
