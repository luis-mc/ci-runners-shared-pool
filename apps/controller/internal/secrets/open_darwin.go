//go:build darwin

package secrets

import "context"

// Open returns the macOS Keychain store.
func Open(_ context.Context, _ string) (Store, error) {
	return NewKeychain(nil), nil
}
