package machines

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf16"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/joinfile"
)

// Join over SSH to a Windows machine that runs the OpenSSH server (machines
// R5.3a, CRSP123, owner decision D70). The default shell there is cmd or
// PowerShell; every script travels as an encoded PowerShell command, so both
// shells take it the same way and no quoting of a command line is involved.

// PackageRef is the Windows installer a machine without crsp.exe downloads: its
// address and the SHA-256 it must have. The machine checks the hash.
type PackageRef struct {
	Name   string
	URL    string
	SHA256 string
	// SelfSigned says the address is the controller's own. The machine then
	// accepts only the certificate chain that holds the pinned fingerprint (the
	// same one the join command carries, CRSP133); the SHA-256 check still
	// decides what is installed. A false value keeps the normal TLS check (the
	// GitHub release).
	SelfSigned bool
	// PinSHA256 is that fingerprint. joinWindows fills it from the controller's
	// own; a source does not set it.
	PinSHA256 string
}

// WindowsPackageSource says where the Windows installer of the controller's own
// version is. arch is "amd64" or "arm64". It is the controller's own package
// endpoint when the console is reachable (CRSP122), else the GitHub release
// asset with its SHA256SUMS line (cmd/crsp).
type WindowsPackageSource interface {
	WindowsPackage(ctx context.Context, arch string) (PackageRef, error)
}

// ErrNotAdmin is returned when the SSH user is not an administrator of the
// Windows machine: the install changes services and system folders.
var ErrNotAdmin = errors.New("the SSH user is not an administrator on this machine")

var (
	shaRe   = regexp.MustCompile(`^[0-9a-f]{64}$`)
	pathRe  = regexp.MustCompile(`^[A-Za-z]:\\[^\r\n"'<>|*?%$` + "`" + `]+$`)
	winRoot = regexp.MustCompile(`^[A-Za-z0-9._\-]+$`)
)

func psEncodedCommand(script string) string {
	u := utf16.Encode([]rune("$ProgressPreference='SilentlyContinue'\n$ErrorActionPreference='Stop'\n" + script))
	b := make([]byte, 0, len(u)*2)
	for _, c := range u {
		b = append(b, byte(c), byte(c>>8))
	}
	return "powershell.exe -NoProfile -NonInteractive -EncodedCommand " + base64.StdEncoding.EncodeToString(b)
}

func psQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// winProbeScript prints one marker line per fact: admin or not (net session
// only works in an elevated session), the processor architecture and where
// crsp.exe is, if anywhere.
const winProbeScript = `
$ErrorActionPreference='Continue'
& net.exe session *> $null
if ($LASTEXITCODE -eq 0) { 'CRSP_ADMIN' } else { 'CRSP_NOT_ADMIN' }
'CRSP_ARCH=' + $env:PROCESSOR_ARCHITECTURE
$exe = ''
$c = Get-Command crsp.exe -ErrorAction SilentlyContinue
if ($c) { $exe = $c.Source } elseif (Test-Path (Join-Path $env:ProgramFiles 'crsp\crsp.exe')) { $exe = Join-Path $env:ProgramFiles 'crsp\crsp.exe' }
'CRSP_EXE=' + $exe
`

// winProbe is what the probe found.
type winProbe struct {
	Shell string // "cmd" or "PowerShell"
	Admin bool
	Arch  string // "amd64" or "arm64"
	Exe   string
}

// shellOf reads the answer of `echo %OS%`: cmd expands it to Windows_NT,
// PowerShell prints it as it is.
func shellOf(out string) string {
	if strings.Contains(out, "Windows_NT") {
		return "cmd"
	}
	return "PowerShell"
}

func parseProbe(shell, out string) (winProbe, error) {
	p := winProbe{Shell: shell}
	var sawAdmin bool
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line) // CRLF-safe
		switch {
		case line == "CRSP_ADMIN":
			p.Admin, sawAdmin = true, true
		case line == "CRSP_NOT_ADMIN":
			sawAdmin = true
		case strings.HasPrefix(line, "CRSP_ARCH="):
			switch strings.ToUpper(strings.TrimPrefix(line, "CRSP_ARCH=")) {
			case "AMD64", "X64":
				p.Arch = "amd64"
			case "ARM64":
				p.Arch = "arm64"
			}
		case strings.HasPrefix(line, "CRSP_EXE="):
			exe := strings.TrimPrefix(line, "CRSP_EXE=")
			if exe != "" && !pathRe.MatchString(exe) {
				return p, errors.New("the path of crsp.exe on the machine is not usable")
			}
			p.Exe = exe
		}
	}
	if !sawAdmin || p.Arch == "" {
		return p, errors.New("the machine did not answer the check as a Windows OpenSSH server does")
	}
	return p, nil
}

// winDownloadScript downloads the installer, checks its SHA-256 before it runs
// anything, installs it quietly and prints where crsp.exe is. From the
// controller's own address it accepts only the pinned certificate (CRSP133).
func winDownloadScript(ref PackageRef) string {
	trust := ""
	if ref.SelfSigned {
		trust = joinfile.PowerShellPin + "[CrspPin]::Enable(" + psQuote(ref.PinSHA256) + ")\n"
	}
	return trust + fmt.Sprintf(`
$msi = Join-Path $env:TEMP %s
Invoke-WebRequest -UseBasicParsing -Uri %s -OutFile $msi
try {
  $h = (Get-FileHash -Algorithm SHA256 -LiteralPath $msi).Hash.ToLower()
  if ($h -ne %s) { throw 'the downloaded installer does not match its SHA-256; nothing was installed' }
  $p = Start-Process -FilePath msiexec.exe -ArgumentList @('/i', $msi, '/qn', '/norestart') -Wait -PassThru
  if ($p.ExitCode -ne 0 -and $p.ExitCode -ne 3010) { throw ('msiexec ended with ' + $p.ExitCode) }
} finally { Remove-Item -LiteralPath $msi -Force -ErrorAction SilentlyContinue }
$exe = Join-Path $env:ProgramFiles 'crsp\crsp.exe'
if (-not (Test-Path $exe)) { throw 'crsp.exe is not where the installer puts it' }
'CRSP_EXE=' + $exe
`, psQuote(ref.Name), psQuote(ref.URL), psQuote(ref.SHA256))
}

// winInstallScript runs the agent install without a prompt and passes on its
// exit code.
func winInstallScript(exe, args string) string {
	return fmt.Sprintf(`
$ErrorActionPreference='Continue'
& %s %s
exit $LASTEXITCODE
`, psQuote(exe), args)
}

// joinWindows runs the Windows steps. progress reports each one by its words.
func (s *Service) joinWindows(ctx context.Context, in SSHJoinInput, installArgs string, progress func(string)) (string, error) {
	out, err := s.o.SSH.Run(ctx, in.SSHTarget, "echo %OS%")
	if err != nil {
		return out, fmt.Errorf("could not tell the shell of the machine: %w", err)
	}
	shell := shellOf(out)
	progress("shell: " + shell)
	out, err = s.o.SSH.Run(ctx, in.SSHTarget, psEncodedCommand(winProbeScript))
	if err != nil {
		return out, fmt.Errorf("the administrator check did not run: %w", err)
	}
	probe, err := parseProbe(shell, out)
	if err != nil {
		return out, err
	}
	if !probe.Admin {
		return "", fmt.Errorf("%w: sign in as an administrator, or run the copied command in PowerShell as Administrator", ErrNotAdmin)
	}
	progress("administrator: yes")
	exe := probe.Exe
	if exe == "" {
		if s.o.WindowsPackages == nil {
			return "", errors.New("crsp.exe is not on the machine and this controller has no installer to send")
		}
		ref, err := s.o.WindowsPackages.WindowsPackage(ctx, probe.Arch)
		if err != nil {
			return "", fmt.Errorf("crsp.exe is not on the machine and its installer could not be found: %w", err)
		}
		if ref.SelfSigned {
			ref.PinSHA256 = strings.ToLower(strings.ReplaceAll(s.o.Fingerprint, ":", ""))
			if !shaRe.MatchString(ref.PinSHA256) {
				return "", errors.New("this controller has no certificate fingerprint to pin the installer download to")
			}
		}
		if !shaRe.MatchString(ref.SHA256) || !winRoot.MatchString(ref.Name) || !strings.HasPrefix(ref.URL, "https://") {
			return "", errors.New("the installer source gave an address or a checksum that is not valid")
		}
		progress("installing crsp from " + ref.Name)
		out, err = s.o.SSH.Run(ctx, in.SSHTarget, psEncodedCommand(winDownloadScript(ref)))
		if err != nil {
			return out, fmt.Errorf("the installer did not finish: %w", err)
		}
		probe, err = parseProbe(shell, "CRSP_ADMIN\nCRSP_ARCH="+probe.Arch+"\n"+out)
		if err != nil || probe.Exe == "" {
			return out, errors.New("the installer did not say where crsp.exe is")
		}
		exe = probe.Exe
	}
	progress("agent install")
	return s.o.SSH.Run(ctx, in.SSHTarget, psEncodedCommand(winInstallScript(exe, installArgs)))
}
