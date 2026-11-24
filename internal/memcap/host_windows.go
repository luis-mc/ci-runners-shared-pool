//go:build windows

package memcap

func windowsEnforcer() Enforcer { return JobObjectEnforcer{} }
