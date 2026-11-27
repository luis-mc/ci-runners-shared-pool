//go:build !linux && !darwin && !windows

package secrets

import (
	"context"
	"path/filepath"
)

// Open returns the 0600 file store under dataDir.
func Open(_ context.Context, dataDir string) (Store, error) {
	return NewFile(filepath.Join(dataDir, "secrets"))
}
