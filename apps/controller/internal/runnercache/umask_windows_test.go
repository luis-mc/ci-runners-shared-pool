//go:build windows

package runnercache

func setUmask(m int) int { return 0 }
