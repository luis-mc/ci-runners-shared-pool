package tray

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Autostart turns the per-user autostart entry on or off (R5.21).
type Autostart interface {
	Enabled() (bool, error)
	Set(on bool) error
}

// XDGAutostart writes a .desktop file in $XDG_CONFIG_HOME/autostart (Linux desktop).
type XDGAutostart struct {
	Dir  string // autostart folder
	Exec string // path of crsp
}

// NewXDGAutostart uses $XDG_CONFIG_HOME/autostart, default ~/.config/autostart.
func NewXDGAutostart(exe string) (*XDGAutostart, error) {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		base = filepath.Join(h, ".config")
	}
	return &XDGAutostart{Dir: filepath.Join(base, "autostart"), Exec: exe}, nil
}

func (a *XDGAutostart) path() string { return filepath.Join(a.Dir, "crsp-tray.desktop") }

// Enabled reports whether the entry exists.
func (a *XDGAutostart) Enabled() (bool, error) { return exists(a.path()) }

// Set writes or removes the entry.
func (a *XDGAutostart) Set(on bool) error {
	if !on {
		return remove(a.path())
	}
	body := fmt.Sprintf(`[Desktop Entry]
Type=Application
Name=CI Runners Shared Pool
Comment=Tray icon for the crsp service
Exec=%s tray
Terminal=false
X-GNOME-Autostart-enabled=true
`, quoteDesktopExec(a.Exec))
	return writeFile(a.path(), body)
}

func quoteDesktopExec(p string) string {
	if strings.ContainsAny(p, " \t\"'\\$`") {
		return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "`", "\\`", `$`, `\$`).Replace(p) + `"`
	}
	return p
}

// LaunchAgent writes a plist in ~/Library/LaunchAgents with RunAtLoad (macOS).
type LaunchAgent struct {
	Dir  string
	Exec string
}

// LaunchAgentLabel is the launchd label of the tray agent.
const LaunchAgentLabel = "dev.luismc.crsp.tray"

// NewLaunchAgent uses ~/Library/LaunchAgents.
func NewLaunchAgent(exe string) (*LaunchAgent, error) {
	h, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return &LaunchAgent{Dir: filepath.Join(h, "Library", "LaunchAgents"), Exec: exe}, nil
}

func (a *LaunchAgent) path() string { return filepath.Join(a.Dir, LaunchAgentLabel+".plist") }

// Enabled reports whether the plist exists.
func (a *LaunchAgent) Enabled() (bool, error) { return exists(a.path()) }

// Set writes or removes the plist.
func (a *LaunchAgent) Set(on bool) error {
	if !on {
		return remove(a.path())
	}
	var x strings.Builder
	_ = xmlEscape(&x, a.Exec)
	body := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>` + LaunchAgentLabel + `</string>
  <key>ProgramArguments</key>
  <array><string>` + x.String() + `</string><string>tray</string></array>
  <key>RunAtLoad</key><true/>
  <key>ProcessType</key><string>Interactive</string>
</dict>
</plist>
`
	return writeFile(a.path(), body)
}

func xmlEscape(b *strings.Builder, s string) error {
	_, err := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").WriteString(b, s)
	return err
}

func exists(p string) (bool, error) {
	_, err := os.Stat(p)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

func remove(p string) error {
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func writeFile(p, body string) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, []byte(body), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}
