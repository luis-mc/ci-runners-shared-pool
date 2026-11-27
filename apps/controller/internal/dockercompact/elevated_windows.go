//go:build windows

package dockercompact

import "golang.org/x/sys/windows"

func elevated() bool { return windows.GetCurrentProcessToken().IsElevated() }
