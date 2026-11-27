//go:build windows

package tray

import (
	"golang.org/x/sys/windows/registry"
)

const runKey = `Software\Microsoft\Windows\CurrentVersion\Run`

// RunKeyAutostart is a value under HKCU\...\Run (R5.21).
type RunKeyAutostart struct{ Exec string }

// Enabled reports whether the value exists.
func (a RunKeyAutostart) Enabled() (bool, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	if err != nil {
		return false, nil
	}
	defer func() { _ = k.Close() }()
	if _, _, err := k.GetStringValue("crsp-tray"); err != nil {
		return false, nil
	}
	return true, nil
}

// Set writes or removes the value.
func (a RunKeyAutostart) Set(on bool) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer func() { _ = k.Close() }()
	if !on {
		if err := k.DeleteValue("crsp-tray"); err != nil && err != registry.ErrNotExist {
			return err
		}
		return nil
	}
	return k.SetStringValue("crsp-tray", `"`+a.Exec+`" tray`)
}

// DefaultAutostart returns the Run-key writer.
func DefaultAutostart(exe string) (Autostart, error) { return RunKeyAutostart{Exec: exe}, nil }
