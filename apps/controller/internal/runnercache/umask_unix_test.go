//go:build !windows

package runnercache

import "syscall"

func setUmask(m int) int { return syscall.Umask(m) }
