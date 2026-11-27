//go:build !windows

package update

import "os"

func privileged() bool { return os.Geteuid() == 0 }
