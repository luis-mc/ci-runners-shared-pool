package runners

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Layer names (runners R5.50). The browsers layer is R5.44.
const (
	LayerBrowsers = "browsers"
)

// PlaywrightPin is the Playwright version the browsers layer is built with
// unless a pool already has a higher one (runners R5.44).
const PlaywrightPin = "1.65.1"

// Layer is one image layer with a fixed tool list (runners R5.50). The names
// are the dev-standards names, so a project setting maps one to one.
type Layer struct {
	Name    string
	Release string // the release that adds it
	// Packages are the distribution packages the layer installs.
	Packages []string
	// Optional packages are installed when the base image has them; a missing
	// one is skipped without a failure.
	Optional []string
	// SelfCheck lists the commands the build runs; the layer fails when one fails.
	SelfCheck []string
	// Dockerfile is the layer's file, relative to the repository root.
	Dockerfile string
	// Rules are the other requirements the layer carries.
	Rules []string
	// Version is the version a layer pins when it has one (the browsers layer
	// pins Playwright, R5.44).
	Version string
}

// KnownLayers are the layers this release can build.
// The browsers layer comes first; the layers of Layers() follow (CRSP60).
var KnownLayers = append([]Layer{
	{Name: LayerBrowsers, Dockerfile: "images/layers/browsers/Dockerfile", Version: PlaywrightPin},
}, Layers()...)

// LayerExtraPackages is the last layer of an image: the pool's extra
// distribution packages (runners R5.51). It is not chosen by name; it is added
// when the pool's list is not empty.
const LayerExtraPackages = "extra-packages"

// ExtraPackagesDockerfile is the file of the extra packages layer.
const ExtraPackagesDockerfile = "images/layers/extra-packages/Dockerfile"

// BuildStep is one docker build of an image: layers in order, the extra
// packages layer last. Each step is built on the image of the step before.
type BuildStep struct {
	Layer      string
	Dockerfile string
	Args       []string
}

// ImagePlan is what one pool's image is built from.
type ImagePlan struct {
	// Key names the image; a change of base, layers or extra packages changes
	// it, so the new image builds aside (R5.33, R5.51).
	Key   string
	Steps []BuildStep
}

// PlanImage plans a pool's image: the selected layers in stable order, then
// the extra packages as the last layer when the list has names. baseImage is
// the first step's base; later steps use "{prev}" for the image of the step
// before, which the builder fills in. The extra package names are checked
// again here, so a list that skipped ParseExtraPackages cannot reach apt.
func PlanImage(baseImage, baseDigest string, p PoolLayers, extras []string) (ImagePlan, error) {
	sel, err := SelectLayers(p)
	if err != nil {
		return ImagePlan{}, err
	}
	var plan ImagePlan
	var names []string
	for i, s := range sel {
		base := baseImage
		if i > 0 {
			base = "{prev}"
		}
		plan.Steps = append(plan.Steps, BuildStep{Layer: s.Name, Dockerfile: s.Dockerfile, Args: s.BuildArgs(base)})
		names = append(names, s.Name)
	}
	var clean []string
	for i, n := range extras {
		if !packageName.MatchString(n) {
			return ImagePlan{}, &PackageError{Line: i + 1, Name: n}
		}
		clean = append(clean, n)
	}
	sort.Strings(clean)
	if len(clean) > 0 {
		base := baseImage
		if len(plan.Steps) > 0 {
			base = "{prev}"
		}
		plan.Steps = append(plan.Steps, BuildStep{
			Layer: LayerExtraPackages, Dockerfile: ExtraPackagesDockerfile,
			Args: []string{"--build-arg", "BASE_IMAGE=" + base, "--build-arg", "EXTRA_PACKAGES=" + strings.Join(clean, " ")},
		})
	}
	plan.Key = LayerSetKey(baseDigest, names, clean)
	return plan, nil
}

// PoolLayers is what a pool asks for: layer names it turned on, and the
// Playwright version its last image was built with ("" when none).
type PoolLayers struct {
	Enabled           []string
	PlaywrightCurrent string
	PlaywrightWanted  string // "" means the pin
}

// SelectedLayer is a layer chosen for one pool's image build.
type SelectedLayer struct {
	Layer
	// Held is true when the version is the pool's current one, kept because the
	// wanted one is lower.
	Held bool
}

// SelectLayers returns the layers to build on top of the base image for one
// pool, in a stable order, and refuses a name it does not know. A layer that is
// not turned on is not built (runners R5.50: a layer is off until turned on).
func SelectLayers(p PoolLayers) ([]SelectedLayer, error) {
	known := map[string]Layer{}
	for _, l := range KnownLayers {
		known[l.Name] = l
	}
	seen := map[string]bool{}
	var names []string
	for _, n := range p.Enabled {
		n = strings.TrimSpace(n)
		if n == "" || seen[n] {
			continue
		}
		if _, ok := known[n]; !ok {
			return nil, refuse("layer %q is not one this release builds", n)
		}
		seen[n] = true
		names = append(names, n)
	}
	sort.Strings(names)
	var out []SelectedLayer
	for _, n := range names {
		l := known[n]
		s := SelectedLayer{Layer: l}
		if n == LayerBrowsers {
			want := p.PlaywrightWanted
			if want == "" {
				want = l.Version
			}
			v, held, err := NeverLower(p.PlaywrightCurrent, want)
			if err != nil {
				return nil, err
			}
			s.Version, s.Held = v, held
		}
		out = append(out, s)
	}
	return out, nil
}

// NeverLower picks the Playwright version of a rebuild: the wanted one, unless
// the pool's current one is higher, which then stays (held is true). A rebuild
// never lowers it (runners R5.44). An empty current takes
// the wanted version. A version that is not dotted numbers is refused and
// nothing changes.
func NeverLower(current, wanted string) (version string, held bool, err error) {
	w, err := parseVersion(wanted)
	if err != nil {
		return "", false, refuse("Playwright version %q: %v", wanted, err)
	}
	if strings.TrimSpace(current) == "" {
		return wanted, false, nil
	}
	c, err := parseVersion(current)
	if err != nil {
		return "", false, refuse("Playwright version %q: %v", current, err)
	}
	if compareVersions(c, w) > 0 {
		return strings.TrimSpace(current), true, nil
	}
	return strings.TrimSpace(wanted), false, nil
}

func parseVersion(s string) ([]int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, fmt.Errorf("is empty")
	}
	parts := strings.Split(s, ".")
	if len(parts) < 2 || len(parts) > 3 {
		return nil, fmt.Errorf("is not major.minor or major.minor.patch")
	}
	out := make([]int, 3)
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || p != strconv.Itoa(n) {
			return nil, fmt.Errorf("has a part that is not a plain number")
		}
		out[i] = n
	}
	return out, nil
}

func compareVersions(a, b []int) int {
	for i := range a {
		if a[i] != b[i] {
			if a[i] < b[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

// BuildArgs gives the docker build arguments of a selected layer.
func (s SelectedLayer) BuildArgs(baseImage string) []string {
	args := []string{"--build-arg", "BASE_IMAGE=" + baseImage}
	if s.Name == LayerBrowsers {
		args = append(args, "--build-arg", "PLAYWRIGHT_VERSION="+s.Version)
	}
	return args
}

// Layers returns the layers of R5.50 in image order. The browsers layer
// (R5.44) is not here: it is built through KnownLayers and SelectLayers.
func Layers() []Layer {
	return []Layer{
		{
			Name: "windows-packaging", Release: "v0.5.0",
			Packages:   []string{"dotnet-sdk-8.0", "wine", "wine64", "xvfb", "xauth", "msitools"},
			SelfCheck:  []string{"dotnet --version", "wine --version", "xvfb-run --help", "command -v xauth", "command -v msiinfo"},
			Dockerfile: "images/layers/windows-packaging/Dockerfile",
			Rules:      []string{"R5.42", "R5.52"},
		},
		{
			Name: "linux-packaging", Release: "v0.5.0",
			Packages: []string{"dpkg-dev", "fakeroot", "rpm", "make", "checkpolicy", "selinux-policy-dev", "policycoreutils",
				"gnupg", "shellcheck", "systemd", "createrepo-c"},
			Optional:   []string{"policycoreutils-python-utils"},
			SelfCheck:  []string{"dpkg-deb --version", "dpkg-buildpackage --version", "fakeroot --version", "rpmbuild --version", "make --version", "checkmodule -V", "gpg --version", "shellcheck --version", "createrepo_c --version"},
			Dockerfile: "images/layers/linux-packaging/Dockerfile",
		},
		{
			Name: "llvm", Release: "v0.5.0",
			SelfCheck:  []string{"clang-cl --version", "lld-link --version", "llvm-lib --help"},
			Dockerfile: "images/layers/llvm/Dockerfile",
			Rules:      []string{"R5.43"},
		},
		{
			Name: "kvm", Release: "v0.6.0",
			Packages:   []string{"qemu-system-x86", "qemu-utils", "cloud-image-utils", "openssh-client"},
			SelfCheck:  []string{"qemu-system-x86_64 --version", "qemu-img --version", "cloud-localds --help", "ssh -V"},
			Dockerfile: "images/layers/kvm/Dockerfile",
			Rules:      []string{"R5.41"},
		},
	}
}

// LayerByName finds a layer.
func LayerByName(name string) (Layer, bool) {
	for _, l := range Layers() {
		if l.Name == name {
			return l, true
		}
	}
	return Layer{}, false
}

var packageName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9+.\-]*$`)

// PackageError names the line of a refused package list (runners R5.51).
type PackageError struct {
	Line int
	Name string
}

func (e *PackageError) Error() string {
	return fmt.Sprintf("line %d: %q is not a package name (letters, digits and + - . only, not starting with -)", e.Line, e.Name)
}

// ParseExtraPackages reads a pool's extra packages, one per line (runners
// R5.51). Blank lines and lines that start with # are skipped. Any bad name
// refuses the whole list with its line number, so nothing changes. The result
// is sorted without repeats.
func ParseExtraPackages(text string) ([]string, error) {
	seen := map[string]bool{}
	for i, raw := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !packageName.MatchString(line) {
			return nil, &PackageError{Line: i + 1, Name: line}
		}
		seen[line] = true
	}
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Strings(out)
	return out, nil
}

// MergeExtraPackages keeps a name that was added until the user drops it
// (R5.51): the new list is the old names plus the added ones, minus dropped.
func MergeExtraPackages(current, add, drop []string) []string {
	set := map[string]bool{}
	for _, n := range current {
		set[n] = true
	}
	for _, n := range add {
		set[n] = true
	}
	for _, n := range drop {
		delete(set, n)
	}
	out := make([]string, 0, len(set))
	for n := range set {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// LayerSetKey is the key of a layer set: the base digest, the layers that are on and
// the extra package names (R5.33, R5.51). The order of the inputs does not
// change it; any change of a layer set or a name does.
func LayerSetKey(baseDigest string, layers, extras []string) string {
	ls := append([]string(nil), layers...)
	es := append([]string(nil), extras...)
	sort.Strings(ls)
	sort.Strings(es)
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "base=%s\nlayers=%s\nextras=%s\n", baseDigest, strings.Join(ls, ","), strings.Join(es, ","))
	return hex.EncodeToString(h.Sum(nil))[:16]
}
