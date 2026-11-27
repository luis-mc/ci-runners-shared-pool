//go:build !windows

package dockercompact

import "os"

func elevated() bool { return os.Geteuid() == 0 }
