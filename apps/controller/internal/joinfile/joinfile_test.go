package joinfile

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	hexA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	hexB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	hexC = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	code = "JOINCODE-abc123_xyz"
)

var sums = []byte(strings.Join([]string{
	hexA + "  crsp-windows-amd64.msi",
	hexB + "  crsp-linux-amd64.deb",
	hexC + "  crsp-darwin-arm64.pkg",
	hexA + "  crsp-linux-amd64.tar.gz",
	hexB + "  crsp-linux-amd64.sbom.json",
	hexB + "  SHA256SUMS-not-a-package",
}, "\n") + "\n")

func params(os string) Params {
	return Params{OS: os, Controller: "pool.lan:8461", DownloadBase: "https://pool.lan:8460", Fingerprint: "AB:CD:EF:01:23:45:67:89",
		Code: code, Version: "1.2.3", Expires: time.Date(2026, 11, 16, 14, 5, 0, 0, time.UTC), Sums: sums}
}

func TestNameSaysOSAndExpiry(t *testing.T) {
	cases := map[string]string{
		"windows": "crsp-join-windows-until-20261116T1405Z.cmd",
		"linux":   "crsp-join-linux-until-20261116T1405Z.sh",
		"macos":   "crsp-join-macos-until-20261116T1405Z.command",
	}
	for os, want := range cases {
		f, err := Render(params(os))
		if err != nil {
			t.Fatalf("%s: %v", os, err)
		}
		if f.Name != want {
			t.Errorf("%s: name %q, want %q", os, f.Name, want)
		}
	}
}

func TestFileHoldsTheValuesAndTheCodeOnce(t *testing.T) {
	for _, os := range []string{"windows", "linux", "macos"} {
		f, err := Render(params(os))
		if err != nil {
			t.Fatal(err)
		}
		s := string(f.Content)
		for _, want := range []string{"pool.lan:8461", "https://pool.lan:8460", "AB:CD:EF:01:23:45:67:89", code, "1.2.3"} {
			if !strings.Contains(s, want) {
				t.Errorf("%s: file lacks %q", os, want)
			}
		}
		if n := strings.Count(s, code); n != 1 {
			t.Errorf("%s: the code appears %d times, want once", os, n)
		}
		if strings.Contains(s, "SHA256SUMS-not-a-package") || strings.Contains(s, "sbom") {
			t.Errorf("%s: a non-installer line got in", os)
		}
	}
}

func TestOnlyThePackagesOfTheOSAreListed(t *testing.T) {
	w, _ := Render(params("windows"))
	if !strings.Contains(string(w.Content), hexA) || strings.Contains(string(w.Content), "crsp-linux") && strings.Contains(string(w.Content), "'crsp-linux-amd64.deb'") {
		t.Errorf("windows file lists the wrong packages")
	}
	l, _ := Render(params("linux"))
	if !strings.Contains(string(l.Content), "'crsp-linux-amd64.deb') say '"+hexB+"'") || strings.Contains(string(l.Content), ".msi'") {
		t.Errorf("linux file lists the wrong packages")
	}
	m, _ := Render(params("macos"))
	if !strings.Contains(string(m.Content), "'crsp-darwin-arm64.pkg') say '"+hexC+"'") {
		t.Errorf("macos file lacks the pkg sum")
	}
}

func TestLineEnds(t *testing.T) {
	w, _ := Render(params("windows"))
	if !bytes.Contains(w.Content, []byte("\r\n")) || bytes.Contains(bytes.ReplaceAll(w.Content, []byte("\r\n"), nil), []byte("\n")) {
		t.Errorf("the Windows file must use CRLF on every line")
	}
	for _, os := range []string{"linux", "macos"} {
		f, _ := Render(params(os))
		if bytes.Contains(f.Content, []byte("\r")) {
			t.Errorf("%s file must use LF only", os)
		}
		if !bytes.HasPrefix(f.Content, []byte("#!/bin/sh\n")) {
			t.Errorf("%s file lacks the shebang", os)
		}
	}
}

func TestShellFilesPassShellcheck(t *testing.T) {
	sc, err := exec.LookPath("shellcheck")
	if err != nil {
		t.Skip("shellcheck is not installed")
	}
	for _, os := range []string{"linux", "macos"} {
		f, _ := Render(params(os))
		p := filepath.Join(t.TempDir(), f.Name+".sh")
		if err := writeFile(p, f.Content); err != nil {
			t.Fatal(err)
		}
		out, err := exec.Command(sc, "-s", "sh", p).CombinedOutput() //nolint:gosec,noctx // test tool on a temp file
		if err != nil {
			t.Errorf("%s: shellcheck: %s", os, out)
		}
		if o, err := exec.Command("sh", "-n", p).CombinedOutput(); err != nil { //nolint:gosec,noctx // syntax check of a temp file
			t.Errorf("%s: sh -n: %s", os, o)
		}
	}
}

func writeFile(p string, b []byte) error { return os.WriteFile(p, b, 0o600) }

func TestWindowsFileKeepsItsBodyMarkerOnce(t *testing.T) {
	f, _ := Render(params("windows"))
	if n := strings.Count(string(f.Content), "#--PS-BODY--"); n != 1 {
		t.Errorf("body marker appears %d times, want once (the launcher splits on it)", n)
	}
	if !strings.Contains(string(f.Content), "agent install --controller $Controller --fingerprint $Fingerprint --join-code $JoinCode") {
		t.Errorf("the file does not run crsp agent install")
	}
	if !strings.Contains(string(f.Content), "-Verb RunAs") {
		t.Errorf("the file does not ask for administrator rights")
	}
}

func TestBadValuesAreRefused(t *testing.T) {
	bad := []func(*Params){
		func(p *Params) { p.Controller = "pool.lan:8461'; rm -rf /" },
		func(p *Params) { p.DownloadBase = "https://pool.lan:8460/x y" },
		func(p *Params) { p.Fingerprint = "AB'CD" },
		func(p *Params) { p.Code = "abc$(id)xxxxxxxx" },
		func(p *Params) { p.Version = "1.0\n" },
		func(p *Params) { p.OS = "plan9" },
		func(p *Params) { p.Sums = []byte("nothing useful\n") },
		func(p *Params) { p.Sums = []byte(hexA + "  crsp-linux-amd64.deb\n"); p.OS = "windows" },
	}
	for i, mut := range bad {
		p := params("linux")
		mut(&p)
		if _, err := Render(p); err == nil {
			t.Errorf("case %d: want an error", i)
		}
	}
}

func TestParseSumsTakesCRLFAndStarMarks(t *testing.T) {
	got := ParseSums([]byte(hexA + " *crsp-linux-arm64.rpm\r\n" + hexB + "  crsp-windows-arm64.msi\r\n"))
	if len(got) != 2 || got[0].Name != "crsp-linux-arm64.rpm" || got[1].SHA256 != hexB {
		t.Errorf("got %+v", got)
	}
}

// OPS40: a release since 0.1.15 names its installers crsp-<os>-<arch>_<version>.<ext>.
// The file still asks for the plain name and downloads the versioned one.
func TestVersionedPackagesAreFoundByPlainName(t *testing.T) {
	versioned := []byte(strings.Join([]string{
		hexA + "  crsp-windows-amd64_0.1.15.msi",
		hexB + "  crsp-linux-amd64_0.1.15.deb",
		hexC + "  crsp-darwin-arm64_0.1.15.pkg",
	}, "\n") + "\n")
	for osName, want := range map[string][]string{
		"windows": {"'crsp-windows-amd64.msi' = 'crsp-windows-amd64_0.1.15.msi'"},
		"linux":   {"'crsp-linux-amd64.deb') say 'crsp-linux-amd64_0.1.15.deb'", "'crsp-linux-amd64.deb') say '" + hexB + "'"},
		"macos":   {"'crsp-darwin-arm64.pkg') say 'crsp-darwin-arm64_0.1.15.pkg'"},
	} {
		p := params(osName)
		p.Sums = versioned
		f, err := Render(p)
		if err != nil {
			t.Fatalf("%s: %v", osName, err)
		}
		for _, w := range want {
			if !strings.Contains(string(f.Content), w) {
				t.Errorf("%s file lacks %q", osName, w)
			}
		}
	}
}
