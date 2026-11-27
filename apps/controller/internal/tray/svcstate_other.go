//go:build !linux && !darwin && !windows

package tray

import "context"

// ServiceState is not available on this OS.
func ServiceState(context.Context) (Status, bool) { return Status{}, false }
