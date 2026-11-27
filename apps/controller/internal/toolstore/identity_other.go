//go:build !windows && !darwin

package toolstore

import "context"

// NoIdentity wipes nothing: Linux has neither a per-user registry nor a keychain the store cares about.
type NoIdentity struct{}

var _ Identity = NoIdentity{}

// Wipe implements Identity.
func (NoIdentity) Wipe(context.Context, string) error { return nil }
