package runners

import (
	"encoding/json"
	"errors"
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestSelectLayersOffUntilTurnedOn(t *testing.T) {
	got, err := SelectLayers(PoolLayers{})
	if err != nil || len(got) != 0 {
		t.Fatalf("got %v %v", got, err)
	}
	got, err = SelectLayers(PoolLayers{Enabled: []string{"browsers", " browsers ", ""}})
	if err != nil || len(got) != 1 || got[0].Name != LayerBrowsers || got[0].Version != PlaywrightPin || got[0].Held {
		t.Fatalf("got %+v %v", got, err)
	}
}

func TestSelectLayersRefusesUnknown(t *testing.T) {
	_, err := SelectLayers(PoolLayers{Enabled: []string{"nonesuch"}})
	var r *RefusalError
	if !errors.As(err, &r) {
		t.Fatalf("want refusal, got %v", err)
	}
}

func TestNeverLowerPlaywright(t *testing.T) {
	cases := []struct {
		cur, want, out string
		held           bool
	}{
		{"", "1.65.1", "1.65.1", false},
		{"1.50.0", "1.65.1", "1.65.1", false},
		{"1.65.1", "1.65.1", "1.65.1", false},
		{"1.70.0", "1.65.1", "1.70.0", true},
		{"2.0.0", "1.99.9", "2.0.0", true},
		{"1.9.0", "1.10.0", "1.10.0", false}, // numeric, not text, order
	}
	for _, c := range cases {
		v, held, err := NeverLower(c.cur, c.want)
		if err != nil || v != c.out || held != c.held {
			t.Errorf("NeverLower(%q,%q) = %q,%v,%v; want %q,%v", c.cur, c.want, v, held, err, c.out, c.held)
		}
	}
	for _, bad := range [][2]string{{"", "latest"}, {"1.x", "1.65.1"}, {"1.65.1", ""}, {"01.2.3", "1.2.3"}} {
		if _, _, err := NeverLower(bad[0], bad[1]); err == nil {
			t.Errorf("NeverLower(%q,%q) accepted", bad[0], bad[1])
		}
	}
}

func TestRebuildKeepsHigherPlaywright(t *testing.T) {
	got, err := SelectLayers(PoolLayers{Enabled: []string{"browsers"}, PlaywrightCurrent: "1.70.0"})
	if err != nil || got[0].Version != "1.70.0" || !got[0].Held {
		t.Fatalf("pin must not lower: %+v %v", got, err)
	}
	args := strings.Join(got[0].BuildArgs("base:x"), " ")
	if !strings.Contains(args, "PLAYWRIGHT_VERSION=1.70.0") || !strings.Contains(args, "BASE_IMAGE=base:x") {
		t.Fatal(args)
	}
}

// TestPlaywrightLayerSelfCheck keeps the layer Dockerfile to R5.44: on the base
// image, version pinned and passed in, the fixed path, three browsers with
// system packages, a headless launch of each at build time.
func TestPlaywrightLayerSelfCheck(t *testing.T) {
	d := readFile(t, "../../../../images/layers/browsers/Dockerfile")
	for _, want := range []string{
		"ARG BASE_IMAGE", "FROM ${BASE_IMAGE}", "ARG PLAYWRIGHT_VERSION=",
		"PLAYWRIGHT_BROWSERS_PATH=/opt/crsp/browsers", "install-deps chromium firefox webkit", "for b in chromium firefox webkit",
		"pw.chromium, pw.firefox, pw.webkit", "headless: true", "process.exit(1)", "USER runner",
	} {
		if !strings.Contains(d, want) {
			t.Errorf("layer Dockerfile lacks %q", want)
		}
	}
	m := regexp.MustCompile(`(?m)^ARG PLAYWRIGHT_VERSION=(\S+)$`).FindStringSubmatch(d)
	if m == nil || m[1] != PlaywrightPin {
		t.Errorf("Dockerfile pin %v differs from PlaywrightPin %s", m, PlaywrightPin)
	}
	if regexp.MustCompile(`\|\s*(sh|bash)\b`).MatchString(strings.ReplaceAll(d, "set -o pipefail", "")) &&
		regexp.MustCompile(`curl[^\n]*\|\s*(sh|bash)`).MatchString(d) {
		t.Error("a download is piped to a shell")
	}
	if KnownLayers[0].Dockerfile != "images/layers/browsers/Dockerfile" {
		t.Error("layer table points at another file")
	}
}

func layerFile(t *testing.T, l Layer) string {
	t.Helper()
	b, err := os.ReadFile("../../../../" + l.Dockerfile)
	if err != nil {
		t.Fatal(err)
	}
	return strings.ReplaceAll(string(b), "\r\n", "\n")
}

// TestLayerToolLists: each layer's Dockerfile installs the tools of R5.50.
func TestLayerToolLists(t *testing.T) {
	want := map[string][]string{
		"windows-packaging": {"dotnet-sdk-8.0", "wine", "wine64", "xvfb", "xauth", "msitools"},
		"linux-packaging": {"dpkg-dev", "fakeroot", "rpm", "make", "checkpolicy", "selinux-policy-dev", "policycoreutils",
			"gnupg", "shellcheck", "systemd", "createrepo-c"},
		"kvm": {"qemu-system-x86", "qemu-utils", "cloud-image-utils", "openssh-client"},
	}
	for name, pkgs := range want {
		l, ok := LayerByName(name)
		if !ok {
			t.Fatalf("layer %s missing", name)
		}
		if strings.Join(l.Packages, " ") != strings.Join(pkgs, " ") {
			t.Errorf("%s packages %v, want %v", name, l.Packages, pkgs)
		}
		d := layerFile(t, l)
		for _, p := range pkgs {
			if !regexp.MustCompile(`(^|[\s\\])` + regexp.QuoteMeta(p) + `([\s\\]|$)`).MatchString(d) {
				t.Errorf("%s: Dockerfile does not install %s", name, p)
			}
		}
	}
	lp, _ := LayerByName("linux-packaging")
	if len(lp.Optional) != 1 || lp.Optional[0] != "policycoreutils-python-utils" {
		t.Errorf("optional package %v", lp.Optional)
	}
	if d := layerFile(t, lp); !strings.Contains(d, "apt-cache show policycoreutils-python-utils") {
		t.Error("the optional package is not skipped when the base lacks it")
	}
	if _, ok := LayerByName("nope"); ok {
		t.Error("unknown layer found")
	}
}

// TestLayerSelfChecks: each Dockerfile runs its main tools at build time, and
// the llvm layer pins its download by SHA-256 (R5.43, R5.37 rule).
func TestLayerSelfChecks(t *testing.T) {
	for _, l := range Layers() {
		d := layerFile(t, l)
		if !strings.Contains(d, "ARG BASE_IMAGE") || !strings.Contains(d, "FROM ${BASE_IMAGE}") {
			t.Errorf("%s: not built on the base image", l.Name)
		}
		if !regexp.MustCompile(`(?m)^USER root$`).MatchString(d) || !regexp.MustCompile(`(?m)^USER runner$`).MatchString(d) {
			t.Errorf("%s: must install as root and give the job user back", l.Name)
		}
		if regexp.MustCompile(`\|\s*(sh|bash)\b`).MatchString(d) {
			t.Errorf("%s: a download is piped to a shell", l.Name)
		}
		selfIdx := strings.Index(d, "Self-check")
		if selfIdx < 0 {
			t.Errorf("%s: no self-check", l.Name)
			continue
		}
		self := d[selfIdx:]
		for _, c := range l.SelfCheck {
			tool := strings.Fields(c)[0]
			if tool == "command" {
				tool = strings.Fields(c)[2]
			}
			if !strings.Contains(self, tool) {
				t.Errorf("%s: self-check does not run %s", l.Name, tool)
			}
		}
	}
	llvm, _ := LayerByName("llvm")
	d := layerFile(t, llvm)
	pins := map[string]string{}
	for arg, key := range map[string]string{"LLVM_SHA256_AMD64": "linux_x64", "LLVM_SHA256_ARM64": "linux_arm64"} {
		m := regexp.MustCompile(`(?m)^ARG ` + arg + `=([0-9a-f]+)$`).FindStringSubmatch(d)
		if m == nil || !ValidSHA256(m[1]) {
			t.Errorf("the LLVM download for %s has no valid pinned SHA-256", key)
			continue
		}
		pins[key] = m[1]
	}
	if !strings.Contains(d, "arm64)") || !strings.Contains(d, "Linux-${llvm_arch}") {
		t.Error("the llvm layer must build for arm64 (R5.43)")
	}
	checkLLVMPinsMatchToolsJSON(t, d, pins)
	if !strings.Contains(d, "sha256sum -c -") {
		t.Error("the LLVM download is not hash checked")
	}
	for _, tool := range []string{"clang-cl", "lld-link", "llvm-lib"} {
		if !strings.Contains(d, "/usr/local/bin/") || !strings.Contains(d, tool) {
			t.Errorf("llvm layer does not put %s on PATH", tool)
		}
	}
	if !strings.Contains(d, "clang-cl --version") {
		t.Error("llvm self-check must run clang-cl --version")
	}
	win, _ := LayerByName("windows-packaging")
	if got := layerFile(t, win); strings.Contains(got, "TMPDIR=") {
		t.Error("windows-packaging must not set TMPDIR (R5.42)")
	}
}

func TestLayerNamesAndReleases(t *testing.T) {
	rel := map[string]string{"windows-packaging": "v0.5.0", "linux-packaging": "v0.5.0", "llvm": "v0.5.0", "kvm": "v0.6.0"}
	if len(Layers()) != len(rel) {
		t.Fatalf("%d layers", len(Layers()))
	}
	for _, l := range Layers() {
		if rel[l.Name] != l.Release {
			t.Errorf("%s release %s", l.Name, l.Release)
		}
	}
}

// TestExtraPackageNamesChecked: R5.51.
func TestExtraPackageNamesChecked(t *testing.T) {
	got, err := ParseExtraPackages("zlib1g-dev\r\n\n# comment\nlibc++-dev\nzlib1g-dev\ng++-12.1\n")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, " ") != "g++-12.1 libc++-dev zlib1g-dev" {
		t.Errorf("got %v", got)
	}
	for text, line := range map[string]int{"ok\n-bad\n": 2, "a b\n": 1, "ok\nok2\nrm;ls": 3, "+x": 1, "x$": 1} {
		_, err := ParseExtraPackages(text)
		var pe *PackageError
		if !errors.As(err, &pe) || pe.Line != line {
			t.Errorf("%q: err %v, want line %d", text, err, line)
		}
	}
	// A name added stays until dropped.
	cur := MergeExtraPackages(nil, []string{"b", "a"}, nil)
	cur = MergeExtraPackages(cur, []string{"c"}, nil)
	if strings.Join(cur, " ") != "a b c" {
		t.Errorf("merge %v", cur)
	}
	if got := MergeExtraPackages(cur, nil, []string{"b"}); strings.Join(got, " ") != "a c" {
		t.Errorf("drop %v", got)
	}
}

func TestLayerSetKeyCoversLayersAndExtras(t *testing.T) {
	a := LayerSetKey("sha256:x", []string{"kvm", "llvm"}, []string{"b", "a"})
	if a != LayerSetKey("sha256:x", []string{"llvm", "kvm"}, []string{"a", "b"}) {
		t.Error("order changes the key")
	}
	for name, k := range map[string]string{
		"base":   LayerSetKey("sha256:y", []string{"kvm", "llvm"}, []string{"a", "b"}),
		"layer":  LayerSetKey("sha256:x", []string{"kvm"}, []string{"a", "b"}),
		"extras": LayerSetKey("sha256:x", []string{"kvm", "llvm"}, []string{"a"}),
	} {
		if k == a {
			t.Errorf("%s does not change the key", name)
		}
	}
}

// TestPlanImageExtrasLastAndKeyed: the extra packages are the last layer and
// a changed list changes the image key (R5.51, R5.33).
func TestPlanImageExtrasLastAndKeyed(t *testing.T) {
	p := PoolLayers{Enabled: []string{"linux-packaging", "windows-packaging"}}
	a, err := PlanImage("base:ci", "sha256:x", p, []string{"zlib1g-dev", "libc++-dev"})
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, s := range a.Steps {
		order = append(order, s.Layer)
	}
	if strings.Join(order, " ") != "linux-packaging windows-packaging extra-packages" {
		t.Fatalf("order %v", order)
	}
	last := a.Steps[2]
	if strings.Join(last.Args, " ") != "--build-arg BASE_IMAGE={prev} --build-arg EXTRA_PACKAGES=libc++-dev zlib1g-dev" {
		t.Errorf("args %v", last.Args)
	}
	if a.Steps[0].Args[1] != "BASE_IMAGE=base:ci" {
		t.Errorf("first base %v", a.Steps[0].Args)
	}
	b, _ := PlanImage("base:ci", "sha256:x", p, []string{"zlib1g-dev"})
	if a.Key == b.Key {
		t.Error("a changed extra list keeps the key")
	}
	none, _ := PlanImage("base:ci", "sha256:x", p, nil)
	if len(none.Steps) != 2 {
		t.Errorf("empty list added a step: %v", none.Steps)
	}
	only, _ := PlanImage("base:ci", "sha256:x", PoolLayers{}, []string{"jq"})
	if len(only.Steps) != 1 || only.Steps[0].Args[1] != "BASE_IMAGE=base:ci" {
		t.Errorf("only extras: %+v", only.Steps)
	}
	if _, err := PlanImage("base:ci", "sha256:x", p, []string{"ok", "-bad"}); err == nil {
		t.Error("bad name planned")
	} else {
		var pe *PackageError
		if !errors.As(err, &pe) {
			t.Errorf("err %v", err)
		}
	}
}

// TestPackagingLayersSelectable: the packaging layers are chosen by name and
// pin their lines (R5.50).
func TestPackagingLayersSelectable(t *testing.T) {
	got, err := SelectLayers(PoolLayers{Enabled: []string{"windows-packaging", "linux-packaging"}})
	if err != nil || len(got) != 2 || got[0].Name != "linux-packaging" {
		t.Fatalf("got %+v %v", got, err)
	}
	win, _ := LayerByName("windows-packaging")
	d := layerFile(t, win)
	for _, want := range []string{"ARG DOTNET_SDK_LINE=8.0", "ARG WINE_LINE=9.0", `grep -q "^${DOTNET_SDK_LINE}\.`, `grep -q "^wine-${WINE_LINE}"`} {
		if !strings.Contains(d, want) {
			t.Errorf("windows-packaging lacks %q", want)
		}
	}
}

// TestExtraPackagesDockerfile: the layer checks names again and every package.
func TestExtraPackagesDockerfile(t *testing.T) {
	b, err := os.ReadFile("../../../../" + ExtraPackagesDockerfile)
	if err != nil {
		t.Fatal(err)
	}
	d := string(b)
	for _, want := range []string{"FROM ${BASE_IMAGE}", "ARG EXTRA_PACKAGES", "USER root", "USER runner", "Self-check", "dpkg -s", "extra package name refused"} {
		if !strings.Contains(d, want) {
			t.Errorf("extra-packages Dockerfile lacks %q", want)
		}
	}
}

// checkLLVMPinsMatchToolsJSON: the layer and scripts/delivery/tools.json (the
// pins ci_tool.py downloads from) name one LLVM version and one hash per
// architecture, so a bump in one place cannot leave the other behind.
func checkLLVMPinsMatchToolsJSON(t *testing.T, dockerfile string, pins map[string]string) {
	t.Helper()
	raw, err := os.ReadFile("../../../../scripts/delivery/tools.json")
	if err != nil {
		t.Fatal(err)
	}
	var tools map[string]struct {
		Version   string `json:"version"`
		Platforms map[string]struct {
			SHA256 string `json:"sha256"`
		} `json:"platforms"`
	}
	if err := json.Unmarshal(raw, &tools); err != nil {
		t.Fatal(err)
	}
	llvm, ok := tools["llvm"]
	if !ok {
		t.Fatal("tools.json has no llvm entry")
	}
	if !strings.Contains(dockerfile, "ARG LLVM_VERSION="+llvm.Version+"\n") {
		t.Errorf("layer LLVM version differs from tools.json (%s)", llvm.Version)
	}
	for key, want := range pins {
		if got := llvm.Platforms[key].SHA256; got != want {
			t.Errorf("%s: layer hash %s, tools.json %s", key, want, got)
		}
	}
}

// TestImageBuildMatrixListsArm64: the CI image job builds the llvm layer for
// amd64 and arm64 (R5.43), the arm64 entry on an arm64 runner.
func TestImageBuildMatrixListsArm64(t *testing.T) {
	raw, err := os.ReadFile("../../../../.github/workflows/ci.yml")
	if err != nil {
		t.Fatal(err)
	}
	w := strings.ReplaceAll(string(raw), "\r\n", "\n") // a Windows checkout has CRLF
	i := strings.Index(w, "\n  image:\n")
	if i < 0 {
		t.Fatal("no image job in ci.yml")
	}
	job := w[i+1:]
	if j := strings.Index(job[1:], "\n  e2e:"); j >= 0 {
		job = job[:j+1]
	}
	for _, want := range []string{"arch: amd64", "arch: arm64", "ubuntu-24.04-arm", "context: images/layers/llvm", "scope=llvm-${{ matrix.arch }}", "crsp-linux-${{ matrix.arch }}"} {
		if !strings.Contains(job, want) {
			t.Errorf("image job lacks %q", want)
		}
	}
	// The llvm step has no `if: matrix.all`, so both architectures build it.
	llvm := job[strings.Index(job, "docker build llvm layer"):]
	if j := strings.Index(llvm[1:], "\n      - name:"); j >= 0 {
		llvm = llvm[:j+1]
	}
	if regexp.MustCompile(`(?m)^\s+if:`).MatchString(llvm) {
		t.Error("the llvm layer must build on every architecture, with no `if`")
	}
}
