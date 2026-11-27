// Package joinfile renders the installer file the owner downloads from the
// Add a machine screen (machines R5.65, CRSP122, owner decision D70). The file
// carries the controller address, the certificate fingerprint, a one-time join
// code and the release's public SHA256SUMS lines, and nothing else secret.
// Windows files use CRLF line ends; shell files use LF.
package joinfile

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"text/template"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/update"
)

// Params are the values a file is made from.
type Params struct {
	OS string // windows, linux or macos
	// Controller is the agent link address, host:port (--controller).
	Controller string
	// DownloadBase is the console address packages are fetched from, such as
	// https://pool.lan:8460 (no trailing slash).
	DownloadBase string
	Fingerprint  string
	// Code is the one-time join code, the only secret in the file.
	Code    string
	Version string
	Expires time.Time
	// Sums is the SHA256SUMS text of the release; only the crsp installers are kept.
	Sums []byte
}

// File is a rendered installer.
type File struct {
	Name        string
	ContentType string
	Content     []byte
}

// ErrInvalid says a value cannot go into a script safely.
var ErrInvalid = errors.New("invalid value for the installer file")

var (
	addrRe    = regexp.MustCompile(`^[A-Za-z0-9._\-]+(:[0-9]{1,5})?$|^\[[0-9A-Fa-f:.]+\](:[0-9]{1,5})?$`)
	baseRe    = regexp.MustCompile(`^https?://([A-Za-z0-9._\-]+|\[[0-9A-Fa-f:.]+\])(:[0-9]{1,5})?$`)
	fpRe      = regexp.MustCompile(`^[0-9A-Fa-f:]{16,200}$`)
	codeRe    = regexp.MustCompile(`^[A-Za-z0-9_\-]{8,128}$`)
	versionRe = regexp.MustCompile(`^[0-9A-Za-z.+\-]{1,64}$`)
	sumRe     = regexp.MustCompile(`^([0-9a-f]{64})[ ]+\*?(crsp-[A-Za-z0-9._\-]+\.(?:msi|deb|rpm|pkg|tar\.gz))$`)
)

// Sum is one package and its SHA-256.
type Sum struct{ Name, SHA256 string }

// Plain is the name without the version part: crsp-linux-amd64_0.1.15.deb is
// crsp-linux-amd64.deb. The files below ask for a package by its plain name, so
// they work for a release with versioned names and for one up to 0.1.14 (OPS40).
func (s Sum) Plain() string { return update.PlainName(s.Name) }

// ParseSums keeps the installer packages of a SHA256SUMS text.
func ParseSums(text []byte) []Sum {
	var out []Sum
	sc := bufio.NewScanner(bytes.NewReader(text))
	for sc.Scan() {
		if m := sumRe.FindStringSubmatch(strings.TrimRight(sc.Text(), "\r")); m != nil {
			out = append(out, Sum{Name: m[2], SHA256: m[1]})
		}
	}
	return out
}

// Extension is the file extension of an OS's file.
func Extension(os string) (string, bool) {
	switch os {
	case "windows":
		return "cmd", true
	case "linux":
		return "sh", true
	case "macos":
		return "command", true
	}
	return "", false
}

// Name is `crsp-join-<os>-until-<UTC stamp>.<ext>`: it says the OS and the expiry.
func Name(os string, expires time.Time) (string, error) {
	ext, ok := Extension(os)
	if !ok {
		return "", fmt.Errorf("%w: os must be windows, linux or macos", ErrInvalid)
	}
	return fmt.Sprintf("crsp-join-%s-until-%s.%s", os, expires.UTC().Format("20060102T1504Z"), ext), nil
}

// forOS keeps the packages the file of an OS can fetch.
func forOS(os string, all []Sum) []Sum {
	prefix, suffix := "crsp-linux-", ""
	switch os {
	case "windows":
		prefix, suffix = "crsp-windows-", ".msi"
	case "macos":
		prefix, suffix = "crsp-darwin-", ".pkg"
	}
	var out []Sum
	for _, s := range all {
		if strings.HasPrefix(s.Name, prefix) && strings.HasSuffix(s.Name, suffix) {
			out = append(out, s)
		}
	}
	return out
}

type view struct {
	Controller, Base, Fingerprint, Code, Version, Expires string
	Sums                                                  []Sum
}

// Render makes the file for p.OS.
func Render(p Params) (File, error) {
	name, err := Name(p.OS, p.Expires)
	if err != nil {
		return File{}, err
	}
	switch {
	case !addrRe.MatchString(p.Controller):
		return File{}, fmt.Errorf("%w: controller address", ErrInvalid)
	case !baseRe.MatchString(p.DownloadBase):
		return File{}, fmt.Errorf("%w: download address", ErrInvalid)
	case !fpRe.MatchString(p.Fingerprint):
		return File{}, fmt.Errorf("%w: fingerprint", ErrInvalid)
	case !codeRe.MatchString(p.Code):
		return File{}, fmt.Errorf("%w: join code", ErrInvalid)
	case !versionRe.MatchString(p.Version):
		return File{}, fmt.Errorf("%w: version", ErrInvalid)
	}
	sums := forOS(p.OS, ParseSums(p.Sums))
	if len(sums) == 0 {
		return File{}, fmt.Errorf("%w: the release lists no installer packages", ErrInvalid)
	}
	v := view{Controller: p.Controller, Base: p.DownloadBase, Fingerprint: p.Fingerprint, Code: p.Code, Version: p.Version,
		Expires: p.Expires.UTC().Format("2006-01-02 15:04 UTC"), Sums: sums}
	var tpl, ctype string
	switch p.OS {
	case "windows":
		tpl, ctype = windowsTemplate, "application/octet-stream"
	case "linux":
		tpl, ctype = linuxTemplate, "text/x-shellscript"
	default:
		tpl, ctype = macosTemplate, "text/x-shellscript"
	}
	t, err := template.New(p.OS).Delims("<<", ">>").Parse(tpl)
	if err != nil {
		return File{}, err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, v); err != nil {
		return File{}, err
	}
	body := strings.ReplaceAll(buf.String(), "\r\n", "\n")
	if p.OS == "windows" {
		body = strings.ReplaceAll(body, "\n", "\r\n")
	}
	return File{Name: name, ContentType: ctype, Content: []byte(body)}, nil
}
