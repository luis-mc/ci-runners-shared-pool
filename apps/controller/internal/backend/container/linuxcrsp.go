package container

import (
	"bytes"
	"context"
	"debug/elf"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/update"
)

// OPS60 (registry R5.82, runners R5.48): the Linux base image copies a crsp
// binary of the image's own OS and architecture, never the controller's own
// file when that is a Windows or macOS binary.

// ErrNoLinuxCrsp wraps every reason the build has no usable Linux crsp.
var ErrNoLinuxCrsp = errors.New("no Linux crsp binary for the image")

// CheckLinuxBinary reads the file header and says nothing when the file is an
// ELF executable for Linux and arch ("amd64" or "arm64"). Otherwise the error
// names what the file is, so the reason of a failed build is plain.
func CheckLinuxBinary(path, arch string) error {
	f, err := os.Open(path) //nolint:gosec // the agent's own binary or its cache
	if err != nil {
		return fmt.Errorf("%w: %w", ErrNoLinuxCrsp, err)
	}
	defer func() { _ = f.Close() }()
	head := make([]byte, 4)
	if _, err := io.ReadFull(f, head); err != nil {
		return fmt.Errorf("%w: %s is too short to be a program", ErrNoLinuxCrsp, filepath.Base(path))
	}
	switch {
	case bytes.HasPrefix(head, []byte("MZ")):
		return fmt.Errorf("%w: %s is a Windows program, not Linux %s", ErrNoLinuxCrsp, filepath.Base(path), arch)
	case bytes.Equal(head, []byte{0xcf, 0xfa, 0xed, 0xfe}), bytes.Equal(head, []byte{0xca, 0xfe, 0xba, 0xbe}), bytes.Equal(head, []byte{0xfe, 0xed, 0xfa, 0xcf}):
		return fmt.Errorf("%w: %s is a macOS program, not Linux %s", ErrNoLinuxCrsp, filepath.Base(path), arch)
	}
	ef, err := elf.NewFile(f)
	if err != nil {
		return fmt.Errorf("%w: %s is not an ELF program", ErrNoLinuxCrsp, filepath.Base(path))
	}
	var want elf.Machine
	switch arch {
	case "amd64":
		want = elf.EM_X86_64
	case "arm64":
		want = elf.EM_AARCH64
	default:
		return fmt.Errorf("%w: architecture %q is not supported", ErrNoLinuxCrsp, arch)
	}
	if ef.Machine != want {
		return fmt.Errorf("%w: %s is built for %s, the image is %s", ErrNoLinuxCrsp, filepath.Base(path), ef.Machine, arch)
	}
	if ef.OSABI != elf.ELFOSABI_NONE && ef.OSABI != elf.ELFOSABI_LINUX {
		return fmt.Errorf("%w: %s is not a Linux program (%s)", ErrNoLinuxCrsp, filepath.Base(path), ef.OSABI)
	}
	if ef.Type != elf.ET_EXEC && ef.Type != elf.ET_DYN {
		return fmt.Errorf("%w: %s is not an executable", ErrNoLinuxCrsp, filepath.Base(path))
	}
	return nil
}

// LinuxCrspSource finds the crsp binary for a Linux image. On a Linux host of
// the same architecture it is the agent's own file. Elsewhere it is the Linux
// release asset of the same version, fetched once, checked against the release
// SHA256SUMS and kept in the data folder.
type LinuxCrspSource struct {
	Version string // the agent's version, without a leading v
	DataDir string // the cache is <DataDir>/linux-crsp/<version>/<arch>/crsp
	GOOS    string // the host; empty means this one
	GOARCH  string
	Exe     func() (string, error) // empty means os.Executable
	// Release and Fetch are the release lookup and the download (tests fake them).
	Release func(ctx context.Context, version string) (update.Release, error)
	Fetch   func(ctx context.Context, url, dst string) error
	Log     func(msg string)
}

// Resolve returns the path of a Linux crsp for arch, or an error that wraps
// ErrNoLinuxCrsp and says what to do.
func (s LinuxCrspSource) Resolve(ctx context.Context, arch string) (string, error) {
	goos, goarch := s.GOOS, s.GOARCH
	if goos == "" {
		goos = runtime.GOOS
	}
	if goarch == "" {
		goarch = runtime.GOARCH
	}
	if goos == "linux" && goarch == arch {
		exe := s.Exe
		if exe == nil {
			exe = os.Executable
		}
		p, err := exe()
		if err != nil {
			return "", fmt.Errorf("%w: %w", ErrNoLinuxCrsp, err)
		}
		return p, nil
	}
	version := strings.TrimPrefix(s.Version, "v")
	if _, ok := update.ParseVersion(version); !ok {
		return "", fmt.Errorf("%w: this crsp (%s, %s/%s) is not a release, so there is no Linux %s crsp of the same version to fetch; install a released crsp", ErrNoLinuxCrsp, orDev(version), goos, goarch, arch)
	}
	if s.DataDir == "" {
		return "", fmt.Errorf("%w: no data folder to keep the Linux crsp in", ErrNoLinuxCrsp)
	}
	dir := filepath.Join(s.DataDir, "linux-crsp", version, arch)
	bin := filepath.Join(dir, "crsp")
	if s.cached(bin, arch) {
		return bin, nil
	}
	if err := s.download(ctx, version, arch, dir, bin); err != nil {
		return "", err
	}
	return bin, nil
}

func orDev(v string) string {
	if v == "" {
		return "dev"
	}
	return v
}

// cached says whether the kept copy is still the one that was checked: an ELF
// of the right arch whose SHA-256 matches the one written after the check.
func (s LinuxCrspSource) cached(bin, arch string) bool {
	want, err := os.ReadFile(bin + ".sha256") //nolint:gosec // the data folder
	if err != nil {
		return false
	}
	got, err := update.FileSHA256(bin)
	if err != nil || got != strings.TrimSpace(string(want)) {
		return false
	}
	return CheckLinuxBinary(bin, arch) == nil
}

func (s LinuxCrspSource) download(ctx context.Context, version, arch, dir, bin string) error {
	fail := func(format string, a ...any) error {
		return fmt.Errorf("%w: "+format, append([]any{ErrNoLinuxCrsp}, a...)...)
	}
	release, fetch := s.Release, s.Fetch
	if release == nil {
		release = func(ctx context.Context, v string) (update.Release, error) {
			return update.GitHubSource{Version: v}.Latest(ctx)
		}
	}
	if fetch == nil {
		fetch = update.Fetch
	}
	rel, err := release(ctx, version)
	if err != nil {
		return fail("could not find release %s to fetch the Linux %s crsp (%v); check the network and try again", version, arch, err)
	}
	plain := update.AssetName("linux", arch)
	asset := update.PickAsset(rel, plain)
	assetURL, ok := rel.Assets[asset]
	if !ok {
		return fail("release %s has no %s", version, asset)
	}
	sumsURL, ok := rel.Assets["SHA256SUMS"]
	if !ok {
		return fail("release %s has no SHA256SUMS to check %s against", version, asset)
	}
	if !update.AllowedURL(assetURL) || !update.AllowedURL(sumsURL) {
		if !s.allowAnyURL() {
			return fail("the download address of %s is not a GitHub address", asset)
		}
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fail("make the folder %s: %v", dir, err)
	}
	work, err := os.MkdirTemp(dir, "fetch-")
	if err != nil {
		return fail("make a work folder: %v", err)
	}
	defer func() { _ = os.RemoveAll(work) }()
	sumsFile := filepath.Join(work, "SHA256SUMS")
	if err := fetch(ctx, sumsURL, sumsFile); err != nil {
		return fail("download SHA256SUMS: %v", err)
	}
	sums, err := os.ReadFile(sumsFile) //nolint:gosec // our work folder
	if err != nil {
		return fail("read SHA256SUMS: %v", err)
	}
	want, ok := update.SumFor(sums, asset)
	if !ok {
		return fail("SHA256SUMS of release %s does not list %s", version, asset)
	}
	if s.Log != nil {
		s.Log("fetching " + asset + " for the Linux image")
	}
	archive := filepath.Join(work, asset)
	if err := fetch(ctx, assetURL, archive); err != nil {
		return fail("download %s: %v", asset, err)
	}
	got, err := update.FileSHA256(archive)
	if err != nil {
		return fail("hash %s: %v", asset, err)
	}
	if got != want {
		return fail("%s does not match the checksum of release %s", asset, version)
	}
	tmp := filepath.Join(work, "crsp")
	if err := update.ExtractBinary(archive, tmp); err != nil {
		return fail("open %s: %v", asset, err)
	}
	if err := CheckLinuxBinary(tmp, arch); err != nil {
		return err
	}
	sum, err := update.FileSHA256(tmp)
	if err != nil {
		return fail("hash the binary: %v", err)
	}
	// The sum file goes first: a binary without a sum is never trusted.
	_ = os.Remove(bin + ".sha256")
	if err := os.Rename(tmp, bin); err != nil {
		return fail("keep the binary in %s: %v", dir, err)
	}
	if err := os.WriteFile(bin+".sha256", []byte(sum+"\n"), 0o600); err != nil {
		return fail("keep the checksum in %s: %v", dir, err)
	}
	return nil
}

// allowAnyURL is false in production. A test sets it through a fetcher that
// does not touch the network by giving addresses on the allow list.
func (s LinuxCrspSource) allowAnyURL() bool { return s.Fetch != nil }
