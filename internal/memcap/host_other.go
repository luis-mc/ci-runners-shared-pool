//go:build !windows

package memcap

// windowsEnforcer is the job object enforcer; off Windows there is none, and
// ForHost never asks for it there.
func windowsEnforcer() Enforcer { return &Fake{} }
