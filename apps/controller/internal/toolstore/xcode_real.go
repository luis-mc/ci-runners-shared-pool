package toolstore

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
)

// DefaultXcodeFinder reads /Applications and asks each app's own xcodebuild for its version. Only compiled
// and vetted in tests (it needs a Mac).
func DefaultXcodeFinder() XcodeFinder { return XcodeFinder{}.withDefaults() }

func (f XcodeFinder) withDefaults() XcodeFinder {
	if f.Glob == nil {
		f.Glob = filepath.Glob
	}
	if f.Version == nil {
		f.Version = func(ctx context.Context, dev string) (string, error) {
			cmd := exec.CommandContext(ctx, filepath.Join(dev, "usr", "bin", "xcodebuild"), "-version")
			cmd.Env = append(os.Environ(), "DEVELOPER_DIR="+dev)
			out, err := cmd.Output()
			return string(out), err
		}
	}
	return f
}
