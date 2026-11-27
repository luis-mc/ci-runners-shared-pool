package agent

import (
	"os"
	"runtime"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/load"
)

// Sys is every OS call detection makes, behind one interface so tests feed
// fixed output (as internal/load does with Probe).
type Sys interface {
	load.Probe
	Hostname() (string, error)
	// Disk is total and free bytes of the volume holding path.
	Disk(path string) (total, free int64, err error)
	// Readable reports whether the agent account can open path for reading and
	// writing, such as /dev/kvm.
	ReadWritable(path string) bool
	// Arch is x64 or arm64 (or the raw GOARCH).
	Arch() string
}

// OSSys is the real Sys.
type OSSys struct{ load.OSProbe }

var _ Sys = OSSys{}

// Hostname implements Sys.
func (OSSys) Hostname() (string, error) { return os.Hostname() }

// ReadWritable implements Sys.
func (OSSys) ReadWritable(path string) bool {
	f, err := os.OpenFile(path, os.O_RDWR, 0) //nolint:gosec // fixed device paths
	if err != nil {
		return false
	}
	_ = f.Close()
	return true
}

// Arch implements Sys.
func (OSSys) Arch() string { return archName(runtime.GOARCH) }

func archName(goarch string) string {
	switch goarch {
	case "amd64":
		return "x64"
	case "arm64":
		return "arm64"
	}
	return goarch
}
