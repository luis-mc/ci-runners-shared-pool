package tray

import (
	"os"
	"path/filepath"
)

// LogDir is the service log folder (R5.13). CRSP_LOG_DIR overrides it.
func LogDir() string { return logDirFor(runtimeGOOS(), os.Getenv, os.UserHomeDir) }

func logDirFor(goos string, env func(string) string, home func() (string, error)) string {
	if d := env("CRSP_LOG_DIR"); d != "" {
		return d
	}
	switch goos {
	case "windows":
		pd := env("ProgramData")
		if pd == "" {
			pd = `C:\ProgramData`
		}
		return filepath.Join(pd, "crsp", "logs")
	case "darwin":
		return "/Library/Logs/crsp"
	default:
		return "/var/log/crsp"
	}
}
