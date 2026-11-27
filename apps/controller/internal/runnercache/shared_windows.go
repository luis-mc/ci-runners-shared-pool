//go:build windows

package runnercache

// applySharedMode does nothing on Windows: the access list of the root is set
// by the agent (SharedACLPlan) and folders inherit it.
func applySharedMode(string) error { return nil }
