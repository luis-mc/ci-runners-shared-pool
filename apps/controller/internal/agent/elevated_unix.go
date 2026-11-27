//go:build !windows

package agent

import "os"

// Elevated reports whether the process has admin rights: root.
func Elevated() bool { return os.Geteuid() == 0 }
