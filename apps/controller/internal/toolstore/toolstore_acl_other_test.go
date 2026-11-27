//go:build !windows

package toolstore

// resetACL does nothing: the seal is mode bits, which unsealDir restores.
func resetACL(string) {}
