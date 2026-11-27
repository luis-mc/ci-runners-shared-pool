//go:build linux

package secrets

import (
	"context"
	"path/filepath"
)

// Open returns the Secret Service when one answers and the 0600 file store
// under dataDir otherwise.
func Open(ctx context.Context, dataDir string) (Store, error) {
	ss := NewSecretService(nil)
	if ss.Available(ctx) {
		return ss, nil
	}
	return NewFile(filepath.Join(dataDir, "secrets"))
}
