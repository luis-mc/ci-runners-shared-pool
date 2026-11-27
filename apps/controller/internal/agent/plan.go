package agent

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"unicode/utf16"
)

// Account names (machines R5.44, D41): the agent runs as LocalSystem on
// Windows and as root on Linux and macOS, like the controller, so it can install
// its own update after the drain (operations R5.41). Runner jobs do not: each
// runs under its own non-admin per-runner account (runners R5.14).
const (
	WindowsAccount = "LocalSystem"
	LinuxAccount   = "root"
	MacAccount     = "root"
	// ServiceName is the service, unit or launchd label.
	ServiceName = "crsp-agent"
	// LaunchdLabel is the macOS LaunchDaemon label.
	LaunchdLabel = "dev.crsp.agent"
)

// ErrNotConfirmed is returned when the user did not say yes to the install plan
// (R5.45): nothing ran.
var ErrNotConfirmed = errors.New("the install plan was not confirmed; nothing was changed")

// ErrNotElevated is returned before the plan is shown when it has admin steps
// and the process has no admin rights: nothing ran. Use NotElevatedError for the
// words with the way to fix it.
var ErrNotElevated = errors.New("the install step needs administrator rights")

// NotElevatedError is the one plain line that stops an install without admin
// rights, naming what to do on this OS. It matches ErrNotElevated.
func NotElevatedError(goos string) error {
	how := "run this command again with sudo"
	if goos == "windows" {
		how = "open PowerShell as Administrator (right-click it, Run as administrator) and run this command again"
	}
	return fmt.Errorf("%w: %s. Nothing was changed", ErrNotElevated, how)
}

// InstallOptions are what the install step needs.
type InstallOptions struct {
	// Binary is the path of the crsp binary to run as the service.
	Binary     string
	Controller string // host:8461
	// Fingerprint and JoinCode come from the install command (R5.3).
	Fingerprint string
	JoinCode    string
	DataDir     string
}

// File is a file the plan writes.
type File struct {
	Path string
	Body string
	Mode os.FileMode
}

// Step is one action of the plan.
type Step struct {
	// Admin is true when the step needs administrator rights.
	Admin bool
	// Undo is how to remove what the step made, shown when a later step fails.
	Undo string
	// What is the words shown before the yes (R5.45): one admin action.
	What     string
	Commands [][]string
	Files    []File
}

// Plan is the install step: every admin action it will take, listed first.
type Plan struct {
	OS      string
	Account string
	Steps   []Step
}

// DefaultDataDir is the machine-wide data folder of the agent on goos. The
// service runs as LocalSystem or root, so it never lives in a user profile.
func DefaultDataDir(goos string) string { return defaultDataDir(goos) }

// Executor runs a plan's actions. Production runs real commands; tests record.
type Executor interface {
	Run(ctx context.Context, argv []string) error
	WriteFile(ctx context.Context, f File) error
}

// psEncoded makes the argv of a PowerShell call whose script travels as an
// encoded command, so no quoting of a command line is involved (R5.47, learning 5:
// PowerShell 5.1 mangles quoted arguments to native programs).
func psEncoded(script string) []string {
	// No progress records: they would travel as CLIXML on stderr.
	u := utf16.Encode([]rune("$ProgressPreference='SilentlyContinue'\n" + script))
	b := make([]byte, 0, len(u)*2)
	for _, c := range u {
		b = append(b, byte(c), byte(c>>8))
	}
	return []string{"powershell.exe", "-NoProfile", "-NonInteractive", "-OutputFormat", "Text", "-EncodedCommand", base64.StdEncoding.EncodeToString(b)}
}

// BuildInstallPlan lists what the install step does on goos. It is a pure
// function: nothing runs until Run is called with a yes (R5.45).
func BuildInstallPlan(goos string, o InstallOptions) (Plan, error) {
	if o.Binary == "" || o.Controller == "" || o.Fingerprint == "" {
		return Plan{}, errors.New("the install plan needs the binary path, the controller address and the fingerprint")
	}
	if o.DataDir == "" {
		o.DataDir = defaultDataDir(goos)
	}
	switch goos {
	case "linux":
		return linuxPlan(o), nil
	case "darwin":
		return macPlan(o), nil
	case "windows":
		return windowsPlan(o), nil
	}
	return Plan{}, fmt.Errorf("no install plan for %s", goos)
}

func defaultDataDir(goos string) string {
	switch goos {
	case "windows":
		return `C:\ProgramData\crsp-agent`
	case "darwin":
		return "/Library/Application Support/crsp-agent"
	}
	return "/var/lib/crsp-agent"
}

func joinArgs(o InstallOptions) []string {
	args := []string{"agent", "join", "--controller", o.Controller, "--fingerprint", o.Fingerprint, "--data-dir", o.DataDir}
	if o.JoinCode != "" {
		args = append(args, "--join-code", o.JoinCode)
	}
	return args
}

func linuxPlan(o InstallOptions) Plan {
	p := Plan{OS: "linux", Account: LinuxAccount}
	// No NoNewPrivileges: the agent switches to each runner's own account and
	// installs its own update, and both need a setuid helper.
	unit := fmt.Sprintf(`[Unit]
Description=CRSP agent
After=network-online.target
Wants=network-online.target

[Service]
User=%s
ExecStart=%s agent run --controller %s --data-dir %s
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
`, LinuxAccount, o.Binary, o.Controller, o.DataDir)
	p.Steps = append(p.Steps,
		Step{Admin: true, What: "create the data folder " + o.DataDir + " (readable by root only)",
			Undo:     "remove the data folder: rm -r " + o.DataDir,
			Commands: [][]string{{"install", "-d", "-m", "0700", "-o", "root", "-g", "root", o.DataDir}}},
		Step{Admin: true, What: "write the systemd unit /etc/systemd/system/crsp-agent.service (runs as " + LinuxAccount + ", starts at boot, restarts after a crash)",
			Undo:  "remove the unit: rm /etc/systemd/system/crsp-agent.service",
			Files: []File{{Path: "/etc/systemd/system/crsp-agent.service", Body: unit, Mode: 0o644}}},
		Step{Admin: true, What: "join the controller with the one-time code",
			Commands: [][]string{append([]string{o.Binary}, joinArgs(o)...)}},
		Step{Admin: true, What: "start the service now and at every boot",
			Commands: [][]string{{"systemctl", "daemon-reload"}, {"systemctl", "enable", "--now", ServiceName}}},
	)
	return p
}

func macPlan(o InstallOptions) Plan {
	p := Plan{OS: "macos", Account: MacAccount}
	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>Label</key><string>%s</string>
<key>UserName</key><string>%s</string>
<key>ProgramArguments</key><array><string>%s</string><string>agent</string><string>run</string><string>--controller</string><string>%s</string><string>--data-dir</string><string>%s</string></array>
<key>RunAtLoad</key><true/>
<key>KeepAlive</key><true/>
</dict></plist>
`, LaunchdLabel, MacAccount, o.Binary, o.Controller, o.DataDir)
	plistPath := "/Library/LaunchDaemons/" + LaunchdLabel + ".plist"
	p.Steps = append(p.Steps,
		Step{Admin: true, What: "create the data folder " + o.DataDir + " (readable by root only)",
			Undo:     "remove the data folder: rm -r '" + o.DataDir + "'",
			Commands: [][]string{{"install", "-d", "-m", "0700", "-o", "root", "-g", "wheel", o.DataDir}}},
		Step{Admin: true, What: "write the LaunchDaemon " + plistPath + " (runs as " + MacAccount + ", starts at boot, restarts after a crash)",
			Undo:  "remove the LaunchDaemon: rm " + plistPath,
			Files: []File{{Path: plistPath, Body: plist, Mode: 0o644}}},
		Step{Admin: true, What: "join the controller with the one-time code",
			Commands: [][]string{append([]string{o.Binary}, joinArgs(o)...)}},
		Step{Admin: true, What: "load the LaunchDaemon now and at every boot",
			Commands: [][]string{{"launchctl", "bootstrap", "system", plistPath}}},
	)
	return p
}

func windowsPlan(o InstallOptions) Plan {
	p := Plan{OS: "windows", Account: WindowsAccount}
	bin := fmt.Sprintf("%s agent run --controller %s --data-dir %s", o.Binary, o.Controller, o.DataDir)
	// The service and its restart policy are registered with PowerShell cmdlets,
	// the script travels encoded, never as a cmd-quoted command line (R5.47).
	// New-Service without a credential makes a LocalSystem service.
	create := fmt.Sprintf("$ErrorActionPreference='Stop'\nNew-Service -Name '%s' -DisplayName 'CRSP agent' -BinaryPathName '%s' -StartupType Automatic | Out-Null\n",
		ServiceName, strings.ReplaceAll(bin, "'", "''"))
	folder := fmt.Sprintf("$ErrorActionPreference='Stop'\nNew-Item -ItemType Directory -Force -Path '%[1]s' | Out-Null\n& icacls.exe '%[1]s' /inheritance:r /grant:r '*S-1-5-18:(OI)(CI)F' '*S-1-5-32-544:(OI)(CI)F' | Out-Null\nif ($LASTEXITCODE -ne 0) { throw 'icacls failed' }\n", o.DataDir)
	p.Steps = []Step{
		{Admin: true, What: "create the data folder " + o.DataDir + " and give it to SYSTEM and Administrators only",
			Undo:     "remove the data folder: Remove-Item -Recurse -Force '" + o.DataDir + "' (in PowerShell as Administrator)",
			Commands: [][]string{psEncoded(folder)}},
		{Admin: true, What: "register the Windows service " + ServiceName + " that starts at boot, running as " + WindowsAccount,
			Undo: "remove the service: sc.exe delete " + ServiceName, Commands: [][]string{psEncoded(create)}},
		{Admin: true, What: "make the service restart after a crash",
			Commands: [][]string{{"sc.exe", "failure", ServiceName, "reset=", "86400", "actions=", "restart/5000/restart/5000/restart/30000"}}},
		{Admin: true, What: "join the controller with the one-time code",
			Commands: [][]string{append([]string{o.Binary}, joinArgs(o)...)}},
		{Admin: true, What: "start the service", Commands: [][]string{psEncoded("$ErrorActionPreference='Stop'\nStart-Service -Name '" + ServiceName + "'\n")}},
	}
	return p
}

// Describe is the text shown before the yes: each admin action, one per line.
func (p Plan) Describe() string {
	var b strings.Builder
	fmt.Fprintf(&b, "The install step needs administrator rights for %d actions. The agent then runs as %s, like the controller, so it can install its own update. Runner jobs run under their own accounts without admin rights:\n", p.adminCount(), p.Account)
	for i, s := range p.Steps {
		if s.Admin {
			fmt.Fprintf(&b, "  %d. %s\n", i+1, s.What)
		}
	}
	return b.String()
}

func (p Plan) adminCount() int {
	n := 0
	for _, s := range p.Steps {
		if s.Admin {
			n++
		}
	}
	return n
}

// RunElevated is Run for a process whose admin rights are known. When the plan
// has admin steps and elevated is false it stops first: nothing is shown, asked
// or run (R5.45).
func (p Plan) RunElevated(ctx context.Context, elevated bool, confirm func(description string) bool, ex Executor) error {
	if p.adminCount() > 0 && !elevated {
		return NotElevatedError(p.goos())
	}
	return p.Run(ctx, confirm, ex)
}

func (p Plan) goos() string {
	if p.OS == "macos" {
		return "darwin"
	}
	return p.OS
}

// Run lists the plan, asks for a yes through confirm and only then runs each
// step. Without a yes nothing runs (R5.45). A step that fails stops the plan and
// says which one, what the earlier steps left behind and how to remove it.
func (p Plan) Run(ctx context.Context, confirm func(description string) bool, ex Executor) error {
	if confirm == nil || !confirm(p.Describe()) {
		return ErrNotConfirmed
	}
	for i, s := range p.Steps {
		if err := runStep(ctx, s, ex); err != nil {
			return fmt.Errorf("step %d (%s): %w%s", i+1, s.What, err, p.leftBehind(i))
		}
	}
	return nil
}

func runStep(ctx context.Context, s Step, ex Executor) error {
	for _, f := range s.Files {
		if err := ex.WriteFile(ctx, f); err != nil {
			return err
		}
	}
	for _, c := range s.Commands {
		if err := ex.Run(ctx, c); err != nil {
			return err
		}
	}
	return nil
}

// leftBehind says what the steps before step index failed made, and how to
// remove it.
func (p Plan) leftBehind(failed int) string {
	var undo []string
	for i := failed - 1; i >= 0; i-- {
		if p.Steps[i].Undo != "" {
			undo = append(undo, p.Steps[i].Undo)
		}
	}
	if len(undo) == 0 {
		return ""
	}
	return ". The earlier steps stay done. To undo them: " + strings.Join(undo, "; ")
}

var (
	psErrText   = regexp.MustCompile(`(?s)<S S="Error">(.*?)</S>`)
	psEscape    = regexp.MustCompile(`_x([0-9A-Fa-f]{4})_`)
	psPosition  = regexp.MustCompile(`\s+(At line:|At char:|\+ CategoryInfo|\+ FullyQualifiedErrorId).*$`)
	psCmdPrefix = regexp.MustCompile(`^[A-Za-z][\w-]*\s:\s+`)
	xmlEntities = strings.NewReplacer("&lt;", "<", "&gt;", ">", "&quot;", `"`, "&apos;", "'", "&amp;", "&")
)

// PlainCommandOutput turns the output of a failed command into one plain line.
// PowerShell can still write its errors as CLIXML ("#< CLIXML" and XML objects);
// the exception message is taken from it, without the cmdlet name and the script
// position. Other output is trimmed to one line.
func PlainCommandOutput(out string) string {
	out = strings.TrimSpace(out)
	if strings.HasPrefix(out, "#< CLIXML") {
		var parts []string
		for _, m := range psErrText.FindAllStringSubmatch(out, -1) {
			parts = append(parts, m[1])
		}
		out = strings.Join(parts, "")
		out = xmlEntities.Replace(out)
		out = psEscape.ReplaceAllStringFunc(out, func(m string) string {
			switch strings.ToUpper(m) {
			case "_X000D_", "_X000A_":
				return " "
			}
			return ""
		})
	}
	out = strings.Join(strings.Fields(out), " ")
	out = psPosition.ReplaceAllString(out, "")
	return strings.TrimSpace(psCmdPrefix.ReplaceAllString(out, ""))
}

// CommandError is the error of a failed command: the plain line of its output,
// or the exit error when it printed nothing.
func CommandError(argv []string, out []byte, err error) error {
	if msg := PlainCommandOutput(string(out)); msg != "" {
		return errors.New(msg)
	}
	return fmt.Errorf("%s: %w", argv[0], err)
}
