//go:build !windows

package hooks

import "github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/scheduler/slots"

// processInfo is only needed on Windows, where a shell may sit between the
// hook and the runner worker.
func processInfo(int64) (slots.ProcessInfo, bool) { return slots.ProcessInfo{}, false }
