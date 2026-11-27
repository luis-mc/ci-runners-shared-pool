package vmcheck

import (
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// The boot of a check VM (port of vm_check.py `boot`, caches R5.46): the image
// of a catalogue distro is downloaded and checked against the published
// digest, a throw-away overlay disk is put on top of it, QEMU boots it with
// KVM, and the serial log is read until a readiness line shows or the time is
// up. Only command building and parsing run in unit tests; the QEMU process,
// the network and the disk are behind Deps.

// Defaults of a boot.
const (
	DefaultMemMB   = 1024
	DefaultCPUs    = 2
	DefaultTimeout = 5 * time.Minute
	// DefaultReady is the line the serial console shows when a cloud image has
	// finished booting to its login prompt.
	DefaultReady = `login:`
	// LogTail is how many lines of the serial log a failure shows.
	LogTail = 20
)

// BootSpec is one boot.
type BootSpec struct {
	Distro  string
	MemMB   int
	CPUs    int
	Timeout time.Duration
	// Ready is a regular expression; the first serial line that matches means the guest is up.
	Ready string
	// CacheDir holds images/ (the downloaded files); WorkDir holds the overlay
	// and the serial log of this boot and is removed after it.
	CacheDir string
	WorkDir  string
}

// Normalize fills the defaults and refuses values that cannot boot.
func (s BootSpec) Normalize() (BootSpec, error) {
	if s.MemMB == 0 {
		s.MemMB = DefaultMemMB
	}
	if s.CPUs == 0 {
		s.CPUs = DefaultCPUs
	}
	if s.Timeout == 0 {
		s.Timeout = DefaultTimeout
	}
	if s.Ready == "" {
		s.Ready = DefaultReady
	}
	switch {
	case s.Distro == "":
		return s, errors.New("a distro is required")
	case s.MemMB < 256:
		return s, fmt.Errorf("memory %d MiB is too small to boot", s.MemMB)
	case s.CPUs < 1:
		return s, errors.New("at least one CPU is required")
	case s.Timeout < 0:
		return s, errors.New("the timeout cannot be negative")
	case s.CacheDir == "" || s.WorkDir == "":
		return s, errors.New("the cache folder and the work folder are required")
	}
	if _, err := regexp.Compile(s.Ready); err != nil {
		return s, fmt.Errorf("the readiness pattern: %w", err)
	}
	return s, nil
}

// ImageFile is the cache path of an image: images/<distro>-<16 hex of digest>.qcow2,
// the name PrunePlan keeps the newest of.
func ImageFile(cacheDir, distro, digest string) string {
	k := digest
	if len(k) > 16 {
		k = k[:16]
	}
	return filepath.Join(cacheDir, "images", distro+"-"+k+".qcow2")
}

// OverlayArgs is the `qemu-img` call that makes the throw-away disk on top of
// the cached image, so the cached file is never written.
func OverlayArgs(base, overlay string) []string {
	return []string{"create", "-f", "qcow2", "-F", "qcow2", "-b", base, overlay}
}

// QemuArgs is the `qemu-system-x86_64` call. KVM is required (-enable-kvm), the
// console is the serial log file, there is no display, no reboot loop and the
// only network is QEMU's user mode (no host port is opened).
func QemuArgs(s BootSpec, overlay, serialLog string) []string {
	return []string{
		"-enable-kvm", "-cpu", "host",
		"-m", strconv.Itoa(s.MemMB), "-smp", strconv.Itoa(s.CPUs),
		"-drive", "file=" + overlay + ",if=virtio,format=qcow2",
		"-nic", "user,model=virtio-net-pci",
		"-display", "none", "-no-reboot",
		"-serial", "file:" + serialLog,
		"-monitor", "none",
	}
}

// Ready reports the first line of the serial log that matches pattern. The
// log is read as it grows, so a line without its newline yet counts too.
func Ready(log, pattern string) (string, bool) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return "", false
	}
	for _, l := range strings.Split(strings.ReplaceAll(log, "\r", ""), "\n") {
		if re.MatchString(l) {
			return strings.TrimSpace(l), true
		}
	}
	return "", false
}

// Tail is the last n non-empty lines of a log.
func Tail(log string, n int) string {
	var lines []string
	for _, l := range strings.Split(strings.ReplaceAll(log, "\r", ""), "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// NewHash is the digest the distro's algo names.
func NewHash(algo string) (hash.Hash, error) {
	switch algo {
	case "sha256":
		return sha256.New(), nil
	case "sha512":
		return sha512.New(), nil
	}
	return nil, fmt.Errorf("algo %q is not sha256 or sha512", algo)
}

// ErrDigest is returned when a downloaded file does not match the published digest.
var ErrDigest = errors.New("the downloaded image does not match the published digest")

// Fetcher reads from the network.
type Fetcher interface {
	// Text fetches a URL as text.
	Text(ctx context.Context, url string) (string, error)
	// Get opens a URL for reading.
	Get(ctx context.Context, url string) (io.ReadCloser, error)
}

// Deps is everything Boot touches outside memory.
type Deps struct {
	Fetch Fetcher
	// KVMUsable says whether /dev/kvm opens for read and write.
	KVMUsable func() bool
	// Run runs a short command (qemu-img) to its end. It is built on exec.CommandContext.
	Run func(ctx context.Context, name string, args ...string) error
	// Start starts the guest and returns a function that waits for it to end.
	// Cancelling ctx kills it. Built on exec.CommandContext.
	Start func(ctx context.Context, name string, args ...string) (wait func() error, err error)
	// Sleep waits between two reads of the serial log.
	Sleep func(ctx context.Context, d time.Duration)
	// FreeMemMB is the memory the runner has free, for the warning of
	// MemoryWarning; nil or ok false means not known.
	FreeMemMB func() (mb int64, ok bool)
}

// Outcome is the verdict of a boot.
type Outcome struct {
	Pass    bool
	Distro  string
	Image   string // the image file name the checksum file named
	Digest  string
	Line    string // the readiness line, when it was seen
	Elapsed time.Duration
	Detail  string // why it failed, with the serial tail
}

// Download makes sure the cache holds the image the checksum file names for
// the distro, checked against its published digest, and returns its path. A
// file that is already there with the right name is trusted: its name holds
// the digest, and it was verified when it was written.
func Download(ctx context.Context, f Fetcher, name string, d Distro, cacheDir string) (path, imageName, digest string, err error) {
	url := d.ChecksumURL
	if strings.Contains(url, "*") {
		i := strings.LastIndex(url, "/")
		listing, lerr := f.Text(ctx, url[:i+1])
		if lerr != nil {
			return "", "", "", fmt.Errorf("the image listing could not be read: %w", lerr)
		}
		if url, err = ResolveChecksumURL(url, listing); err != nil {
			return "", "", "", err
		}
	}
	text, err := f.Text(ctx, url)
	if err != nil {
		return "", "", "", fmt.Errorf("the checksum file could not be read: %w", err)
	}
	img, err := ParseChecksums(text, d.ChecksumFormat, d.Pattern)
	if err != nil {
		return "", "", "", err
	}
	path = ImageFile(cacheDir, name, img.Digest)
	if st, serr := os.Stat(path); serr == nil && st.Mode().IsRegular() {
		return path, img.Name, img.Digest, nil
	}
	// One download at a time per distro: a second job waits here and then
	// finds the file the first made (caches R5.47).
	unlock, err := Lock(ctx, cacheDir, "image-"+name)
	if err != nil {
		return "", "", "", err
	}
	defer unlock()
	if st, serr := os.Stat(path); serr == nil && st.Mode().IsRegular() {
		return path, img.Name, img.Digest, nil
	}
	if err := EnsureCacheDir(cacheDir); err != nil {
		return "", "", "", err
	}
	h, err := NewHash(d.Algo)
	if err != nil {
		return "", "", "", err
	}
	rc, err := f.Get(ctx, strings.TrimRight(d.BaseURL, "/")+"/"+img.Name)
	if err != nil {
		return "", "", "", fmt.Errorf("the image could not be downloaded: %w", err)
	}
	defer func() { _ = rc.Close() }()
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return "", "", "", err
	}
	defer func() { _ = os.Remove(tmp.Name()) }() // gone after the rename; the failure path leaves nothing
	if _, err := io.Copy(io.MultiWriter(tmp, h), rc); err != nil {
		_ = tmp.Close()
		return "", "", "", fmt.Errorf("the image download broke off: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", "", "", err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != img.Digest {
		return "", "", "", fmt.Errorf("%w: %s is %s, the checksum file says %s", ErrDigest, img.Name, got, img.Digest)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return "", "", "", err
	}
	return path, img.Name, img.Digest, nil
}

// Boot downloads the image, boots it and says pass or fail. It never leaves
// the guest, the overlay or the serial log behind. An error is returned only
// for what stops the boot from being tried (bad spec, no KVM); a boot that
// does not come up is a failing Outcome.
func Boot(ctx context.Context, cat Catalogue, spec BootSpec, deps Deps, now func() time.Time) (Outcome, error) {
	spec, err := spec.Normalize()
	if err != nil {
		return Outcome{}, err
	}
	d, err := cat.Get(spec.Distro)
	if err != nil {
		return Outcome{}, err
	}
	if !deps.KVMUsable() {
		return Outcome{}, errors.New("/dev/kvm is not usable; this runner has no kvm (see the doctor check kvm.host)")
	}
	start := now()
	out := Outcome{Distro: spec.Distro}
	fail := func(detail string) (Outcome, error) {
		out.Pass, out.Detail, out.Elapsed = false, detail, now().Sub(start)
		return out, nil
	}

	image, imgName, digest, err := Download(ctx, deps.Fetch, spec.Distro, d, spec.CacheDir)
	if err != nil {
		return fail(err.Error())
	}
	out.Image, out.Digest = imgName, digest
	base, release, err := UseBase(ctx, deps, spec.CacheDir, spec.Distro, image, digest)
	if err != nil {
		return fail(err.Error())
	}
	defer release()

	if err := os.MkdirAll(spec.WorkDir, 0o750); err != nil {
		return Outcome{}, err
	}
	overlay := filepath.Join(spec.WorkDir, "overlay.qcow2")
	serial := filepath.Join(spec.WorkDir, "serial.log")
	defer func() {
		_ = os.Remove(overlay)
		_ = os.Remove(serial)
	}()
	if err := deps.Run(ctx, "qemu-img", OverlayArgs(base, overlay)...); err != nil {
		return fail("qemu-img could not make the overlay disk: " + err.Error())
	}

	bctx, cancel := context.WithTimeout(ctx, spec.Timeout)
	defer cancel()
	wait, err := deps.Start(bctx, "qemu-system-x86_64", QemuArgs(spec, overlay, serial)...)
	if err != nil {
		return fail("qemu could not start: " + err.Error())
	}
	exited := make(chan error, 1)
	go func() { exited <- wait() }()
	// stop kills the guest (cancel) and waits for it, so nothing runs on after Boot.
	stop := func() {
		cancel()
		if err := <-exited; err != nil {
			// The guest was killed on purpose; its exit error says nothing new.
			return
		}
	}

	for {
		log, _ := os.ReadFile(serial) //nolint:gosec // the work folder of this boot
		if line, ok := Ready(string(log), spec.Ready); ok {
			stop()
			out.Pass, out.Line, out.Elapsed = true, line, now().Sub(start)
			return out, nil
		}
		select {
		case err := <-exited:
			tail := Tail(string(log), LogTail)
			if err == nil {
				err = errors.New("the guest powered off")
			}
			if bctx.Err() != nil {
				return fail(fmt.Sprintf("no line matching %q within %s\n%s", spec.Ready, spec.Timeout, tail))
			}
			return fail(fmt.Sprintf("qemu ended before the guest was ready: %v\n%s", err, tail))
		default:
		}
		if bctx.Err() != nil {
			stop()
			log, _ = os.ReadFile(serial) //nolint:gosec // as above
			return fail(fmt.Sprintf("no line matching %q within %s\n%s", spec.Ready, spec.Timeout, Tail(string(log), LogTail)))
		}
		deps.Sleep(bctx, time.Second)
	}
}

// UseBase makes sure the prepared base disk of the image exists and takes a
// lease on the image and the base, so prune leaves both while the VM runs. The
// returned function ends the lease.
func UseBase(ctx context.Context, deps Deps, cacheDir, distro, image, digest string) (string, func(), error) {
	base, err := EnsureBase(ctx, deps, cacheDir, distro, image, digest)
	if err != nil {
		return "", nil, err
	}
	release, err := Lease(cacheDir, image, base)
	if err != nil {
		return "", nil, err
	}
	return base, release, nil
}
