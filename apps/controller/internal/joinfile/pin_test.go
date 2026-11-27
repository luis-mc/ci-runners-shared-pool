package joinfile

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/machines/secretstest"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/tlsca"
)

// CRSP133 (machines R5.65): every file pins the controller certificate by the
// fingerprint the join uses, for the packages it downloads.
func TestFilesPinTheControllerCertificate(t *testing.T) {
	for _, os := range []string{"windows", "linux", "macos"} {
		f, err := Render(params(os))
		if err != nil {
			t.Fatal(err)
		}
		s := string(f.Content)
		if strings.Contains(s, "wget") || strings.Contains(s, "CrspTrust") || strings.Contains(s, "delegate { return true") {
			t.Errorf("%s: a download that skips the certificate check is left", os)
		}
		for _, want := range []string{"Delete this file", "Fingerprint"} {
			if want == "Fingerprint" {
				want = map[string]string{"windows": "$Fingerprint =", "linux": "FINGERPRINT=", "macos": "FINGERPRINT="}[os]
			}
			if !strings.Contains(s, want) {
				t.Errorf("%s: file lacks %q", os, want)
			}
		}
		if os == "windows" {
			if !strings.Contains(s, "[CrspPin]::Enable($Fingerprint)") || !strings.Contains(s, "ComputeHash(el.Certificate.RawData)") {
				t.Errorf("windows: the download is not pinned")
			}
			if strings.Index(s, "[CrspPin]::Enable") > strings.Index(s, "DownloadFile") {
				t.Errorf("windows: the pin is set after the download")
			}
			continue
		}
		if !strings.Contains(s, "--pinnedpubkey") || !strings.Contains(s, "openssl verify -CAfile") || !strings.Contains(s, "pin_controller") {
			t.Errorf("%s: the download is not pinned", os)
		}
		if strings.Contains(s, "curl -fsSL -k -o") {
			t.Errorf("%s: a curl without the pin is left", os)
		}
	}
}

// The first lines of the file say what it holds (R5.65, CRSP133).
func TestFilesSayTheCodeIsOneTimeAndToDeleteThem(t *testing.T) {
	for _, os := range []string{"windows", "linux", "macos"} {
		f, _ := Render(params(os))
		lines := strings.SplitN(string(f.Content), "\n", 6)
		head := strings.Join(lines[:min(5, len(lines))], "\n")
		if !strings.Contains(head, "works once") || !strings.Contains(head, "Delete this file") {
			t.Errorf("%s: the first lines must say the code works once and to delete the file:\n%s", os, head)
		}
	}
}

func authority(t *testing.T) *tlsca.Authority {
	t.Helper()
	a, err := tlsca.LoadOrCreate(context.Background(), tlsca.Options{Dir: t.TempDir(), Secrets: secretstest.New(), Hosts: []string{"127.0.0.1"}})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// controller serves the package endpoint over TLS with the certificate of ca.
func controller(t *testing.T, ca *tlsca.Authority, body []byte) (base string) {
	t.Helper()
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(body) }))
	srv.TLS = &tls.Config{Certificates: ca.ServerTLS().Certificates, MinVersion: tls.VersionTLS12}
	srv.Listener = mustListen(t)
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return "https://" + srv.Listener.Addr().String()
}

func mustListen(t *testing.T) net.Listener {
	t.Helper()
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	return ln
}

// runScript renders the file for os against base with the pinned fingerprint,
// puts stubs for the installer and sudo first in PATH, runs it, and says whether
// the install step was reached.
func runScript(t *testing.T, base, fp string, pkg []byte, goos string) (out string, installed bool, err error) {
	t.Helper()
	sum := sha256.Sum256(pkg)
	h := hex.EncodeToString(sum[:])
	sums := strings.Join([]string{h + "  crsp-linux-amd64.deb", h + "  crsp-linux-arm64.deb"}, "\n") + "\n"
	p := params(goos)
	p.DownloadBase, p.Fingerprint, p.Sums = base, fp, []byte(sums)
	f, rerr := Render(p)
	if rerr != nil {
		t.Fatal(rerr)
	}
	dir := t.TempDir()
	stubs := filepath.Join(dir, "bin")
	if err := os.Mkdir(stubs, 0o700); err != nil {
		t.Fatal(err)
	}
	mark := filepath.Join(dir, "installed")
	stub := "#!/bin/sh\necho \"$0 $*\" >> '" + mark + "'\n"
	for _, n := range []string{"dpkg", "sudo"} {
		if err := os.WriteFile(filepath.Join(stubs, n), []byte(stub), 0o700); err != nil { //nolint:gosec // a test stub must run
			t.Fatal(err)
		}
	}
	script := filepath.Join(dir, "join.sh")
	if err := os.WriteFile(script, f.Content, 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "sh", script)
	cmd.Env = append(os.Environ(), "PATH="+stubs+":"+os.Getenv("PATH"))
	b, err := cmd.CombinedOutput()
	_, serr := os.Stat(mark)
	return string(b), serr == nil, err
}

func needTools(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the shell file runs on Linux and macOS")
	}
	for _, n := range []string{"sh", "openssl", "curl", "awk"} {
		if _, err := exec.LookPath(n); err != nil {
			t.Skipf("%s is not installed", n)
		}
	}
	if m, _ := exec.CommandContext(t.Context(), "uname", "-m").Output(); !strings.Contains("x86_64 amd64 aarch64 arm64", strings.TrimSpace(string(m))) {
		t.Skip("the file supports amd64 and arm64")
	}
}

// The file downloads from the controller whose certificate it pins, and refuses a
// controller with another certificate before it fetches or installs anything.
func TestLinuxFilePinsTheController(t *testing.T) {
	needTools(t)
	pkg := []byte("package bytes")
	right, other := authority(t), authority(t)

	out, installed, err := runScript(t, controller(t, right, pkg), right.Fingerprint(), pkg, "linux")
	// The run ends at the agent install, which there is no crsp for; the package
	// installer (a stub) having run is what shows the download was accepted.
	if !installed || !strings.Contains(out, "Checked the SHA-256") {
		t.Fatalf("the pinned controller was refused: %v\n%s", err, out)
	}

	// The same address with the fingerprint of another certificate.
	out, installed, err = runScript(t, controller(t, other, pkg), right.Fingerprint(), pkg, "linux")
	if err == nil || installed || !strings.Contains(out, "not the one pinned") {
		t.Fatalf("a controller with another certificate must be refused (err %v, installed %v):\n%s", err, installed, out)
	}

	// A fingerprint written with colons and capitals still pins.
	fp := right.Fingerprint()
	var colon []string
	for i := 0; i < len(fp); i += 2 {
		colon = append(colon, strings.ToUpper(fp[i:i+2]))
	}
	if out, installed, err = runScript(t, controller(t, right, pkg), strings.Join(colon, ":"), pkg, "linux"); !installed {
		t.Fatalf("colon form refused: %v\n%s", err, out)
	}
}

// A chain whose server certificate is not signed by the pinned CA is refused even
// when the pinned certificate is sent along.
func TestLinuxFileRefusesAServerCertificateNotSignedByThePin(t *testing.T) {
	needTools(t)
	pkg := []byte("package bytes")
	right, other := authority(t), authority(t)
	cert := other.ServerTLS().Certificates[0]
	cert.Certificate = append(append([][]byte{}, cert.Certificate[0]), right.ServerTLS().Certificates[0].Certificate[1])
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(pkg) }))
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
	srv.Listener = mustListen(t)
	srv.StartTLS()
	t.Cleanup(srv.Close)
	out, installed, err := runScript(t, "https://"+srv.Listener.Addr().String(), right.Fingerprint(), pkg, "linux")
	if err == nil || installed || !strings.Contains(out, "not signed by the pinned one") {
		t.Fatalf("err %v installed %v:\n%s", err, installed, out)
	}
}

// curl keeps to the pinned server key even with -k (the file relies on it), and
// the .command file is valid shell.
func TestCurlPinnedKeyBeatsInsecureAndMacosFileParses(t *testing.T) {
	needTools(t)
	ca := authority(t)
	base := controller(t, ca, []byte("x"))
	wrong := "sha256//" + strings.Repeat("A", 43) + "="
	if err := exec.CommandContext(t.Context(), "curl", "-fsS", "-k", "--pinnedpubkey", wrong, "-o", os.DevNull, base).Run(); err == nil {
		t.Fatal("curl -k accepted a server key that is not the pinned one")
	}
	f, err := Render(params("macos"))
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(t.TempDir(), "join.command")
	if err := os.WriteFile(script, f.Content, 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.CommandContext(t.Context(), "sh", "-n", script).CombinedOutput(); err != nil {
		t.Fatalf("the macOS file is not valid shell: %v\n%s", err, out)
	}
}
