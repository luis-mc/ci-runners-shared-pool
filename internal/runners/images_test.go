package runners

import (
	"context"
	"errors"
	"os"
	"regexp"
	"strings"
	"testing"
)

const baseDockerfile = "../../images/linux-base/Dockerfile"

func readDockerfile(t *testing.T) string { return readFile(t, baseDockerfile) }

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.ReplaceAll(string(b), "\r\n", "\n")
}

// TestBaseImageToolListAndSelfCheck keeps the Dockerfile to runners R5.49: a
// pinned base digest, the tool list, a hash on every download, a non-root job
// user with no sudo, and a build-time self-check of each tool.
func TestBaseImageToolListAndSelfCheck(t *testing.T) {
	d := readDockerfile(t)
	if !regexp.MustCompile(`(?m)^FROM ubuntu:24\.04@sha256:[0-9a-f]{64}( AS tools)?$`).MatchString(d) {
		t.Error("base image is not pinned by digest")
	}
	for _, pkg := range []string{"build-essential", "ca-certificates", "curl", "git", "jq", "libatomic1", "libicu74", "pkg-config", "postgresql-client", "python3", "unzip", "xz-utils", "zip"} {
		if !strings.Contains(d, pkg) {
			t.Errorf("package %s missing", pkg)
		}
	}
	for _, arg := range []string{"NODE_SHA256_AMD64", "NODE_SHA256_ARM64", "COREPACK_SHA256", "GH_SHA256_AMD64", "GH_SHA256_ARM64"} {
		m := regexp.MustCompile(`(?m)^ARG ` + arg + `=([0-9a-f]+)$`).FindStringSubmatch(d)
		if m == nil || !ValidSHA256(m[1]) {
			t.Errorf("%s is not a valid pinned SHA-256", arg)
		}
	}
	if got := strings.Count(d, "sha256sum -c -"); got < 3 {
		t.Errorf("%d hash checks, want one per download (3)", got)
	}
	if regexp.MustCompile(`\|\s*(sh|bash)\b`).MatchString(d) {
		t.Error("a download is piped to a shell")
	}
	// Node first on PATH, corepack enable once at build time as root, before USER.
	enable, user := strings.Index(d, "corepack enable"), strings.Index(d, "\nUSER runner")
	if enable < 0 || user < 0 || enable > user {
		t.Error("corepack enable must run at build time as root, before USER")
	}
	if !strings.Contains(d, "PATH=/usr/local/bin:") {
		t.Error("/usr/local/bin is not first on PATH")
	}
	for _, check := range []string{"node --version", "corepack --version", "gh --version", `command -v pnpm`, "ldconfig -p | grep libatomic >/dev/null", "crsp version"} {
		if !strings.Contains(d, check) {
			t.Errorf("self-check %q missing", check)
		}
	}
	// Fixed non-root job user, no sudo.
	if !strings.Contains(d, "useradd --uid") || !strings.Contains(d, "USER runner") || strings.Contains(d, "install -y sudo") || !strings.Contains(d, "! command -v sudo") {
		t.Error("job user rules broken")
	}
	// R5.48: the crsp binary is copied in.
	if !strings.Contains(d, "COPY --chmod=0755 crsp /usr/local/bin/crsp") {
		t.Error("crsp is not copied into the image")
	}
}

// The Actions runner in the Dockerfile has the version and hashes of the pins
// the agent uses (runners R5.37, R5.49, open question 10).
func TestBaseImageRunnerPinMatchesPins(t *testing.T) {
	d := readDockerfile(t)
	arg := func(name string) string {
		m := regexp.MustCompile(`(?m)^ARG ` + name + `=(\S+)$`).FindStringSubmatch(d)
		if m == nil {
			t.Fatalf("no ARG %s in the Dockerfile", name)
		}
		return m[1]
	}
	for arch, name := range map[string]string{"x64": "RUNNER_SHA256_AMD64", "arm64": "RUNNER_SHA256_ARM64"} {
		p, err := PinFor("linux", arch)
		if err != nil {
			t.Fatal(err)
		}
		if got := arg("RUNNER_VERSION"); got != p.Version {
			t.Errorf("%s: Dockerfile version %s, pin %s", arch, got, p.Version)
		}
		if got := arg(name); got != p.SHA256 {
			t.Errorf("%s: Dockerfile hash %s, pin %s", arch, got, p.SHA256)
		}
	}
	for _, want := range []string{"sha256sum -c", "/opt/actions-runner", "installdependencies.sh", "DISABLE_RUNNER_UPDATE=1", "run.sh --version"} {
		if !strings.Contains(d, want) {
			t.Errorf("Dockerfile lacks %q", want)
		}
	}
}

func TestMirrorFallback(t *testing.T) {
	var pulled, notices []string
	m := &MirrorPull{
		Mirror: "mirror.lan:5000",
		Pull: func(_ context.Context, ref string) error {
			pulled = append(pulled, ref)
			if strings.HasPrefix(ref, "mirror.lan:5000/") {
				return errors.New("no route to mirror")
			}
			return nil
		},
		Notice: func(s string) { notices = append(notices, s) },
	}
	for i := 0; i < 2; i++ {
		if err := m.Do(context.Background(), "ghcr.io/x/img@sha256:1"); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{"mirror.lan:5000/ghcr.io/x/img@sha256:1", "ghcr.io/x/img@sha256:1", "mirror.lan:5000/ghcr.io/x/img@sha256:1", "ghcr.io/x/img@sha256:1"}
	if strings.Join(pulled, ",") != strings.Join(want, ",") {
		t.Fatalf("pulled %v", pulled)
	}
	if len(notices) != 1 {
		t.Fatalf("notice must be said once, got %d", len(notices))
	}
}

func TestMirrorUsedWhenUp(t *testing.T) {
	var pulled []string
	m := &MirrorPull{Mirror: "m:1", Pull: func(_ context.Context, r string) error { pulled = append(pulled, r); return nil },
		Notice: func(string) { t.Fatal("no notice when the mirror works") }}
	if err := m.Do(context.Background(), "a/b"); err != nil || len(pulled) != 1 || pulled[0] != "m:1/a/b" {
		t.Fatal(pulled, err)
	}
	none := &MirrorPull{Pull: func(_ context.Context, r string) error { pulled = append(pulled, r); return nil }}
	_ = none.Do(context.Background(), "a/b")
	if pulled[1] != "a/b" {
		t.Fatal(pulled)
	}
}
