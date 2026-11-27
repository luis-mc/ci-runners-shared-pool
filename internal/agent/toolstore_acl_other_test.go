//go:build !windows

package agent

// resetACL does nothing: the seal is mode bits, which unsealAll restores.
func resetACL(string) {}
