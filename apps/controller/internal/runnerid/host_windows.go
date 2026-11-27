//go:build windows

package runnerid

import "context"

// HostDefault: Windows accounts have no numeric uid; nothing overlaps.
func HostDefault() Host { return func(context.Context, int) (bool, error) { return false, nil } }
