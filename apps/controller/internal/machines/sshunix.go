package machines

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// Agent push to Linux and macOS over SSH, and the manual command (CRSP304,
// operations R5.130 and R5.131). When the machine has no `crsp`, the install
// command first downloads the agent installer of the machine's CPU from the
// controller's own LAN link (or the release), checks its SHA-256 before it opens
// the archive and runs the installer's install.sh. Over SSH the owner never sees
// an installer; the audit log holds the step, never a secret.

func shQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// agentRefs asks the source for the installer of each CPU of an OS and checks
// what it answers; both are needed because the CPU of the machine is read there.
func (s *Service) agentRefs(ctx context.Context, osName string) ([2]PackageRef, error) {
	var picks [2]PackageRef
	for i, arch := range []string{"amd64", "arm64"} {
		ref, err := s.o.AgentInstallers.AgentInstaller(ctx, osName, arch)
		if err != nil {
			return picks, fmt.Errorf("crsp may not be on the machine and its installer could not be found: %w", err)
		}
		if !shaRe.MatchString(ref.SHA256) || !winRoot.MatchString(ref.Name) || !strings.HasPrefix(ref.URL, "https://") || strings.ContainsAny(ref.URL, "\r\n") {
			return picks, errors.New("the installer source gave an address or a checksum that is not valid")
		}
		picks[i] = ref
	}
	return picks, nil
}

// unixScript is the shell text that installs crsp when it is missing and then
// runs cmd. sudo is "sudo -n" over SSH (nobody can type a password) and "sudo" in
// a command a person pastes.
func unixScript(picks [2]PackageRef, sudo, cmd string) string {
	lines := []string{
		"if ! command -v crsp >/dev/null 2>&1; then",
		`  case "$(uname -m)" in`,
	}
	for i, pat := range []string{"x86_64|amd64", "aarch64|arm64"} {
		p := picks[i]
		k := ""
		if p.SelfSigned {
			k = "k" // the SHA-256, taken in the owner's session, decides what is installed
		}
		lines = append(lines, fmt.Sprintf("    %s) N=%s; U=%s; S=%s; K=%s ;;", pat, shQuote(p.Name), shQuote(p.URL), shQuote(p.SHA256), shQuote(k)))
	}
	lines = append(lines,
		"    *) echo 'this CPU is not supported by the agent installer' >&2; exit 1 ;;",
		"  esac",
		`  d="$(mktemp -d)" || exit 1`,
		`  trap 'rm -rf "$d"' EXIT`,
		`  ( cd "$d" && curl -fsSL${K} -o "$N" "$U" && `+
			`if command -v sha256sum >/dev/null 2>&1; then echo "$S  $N" | sha256sum -c - >/dev/null; else echo "$S  $N" | shasum -a 256 -c - >/dev/null; fi && `+
			`tar xzf "$N" && `+sudo+` sh crsp-agent/install.sh ) || { echo 'the agent installer did not finish' >&2; exit 1; }`,
		"fi",
		cmd,
	)
	return strings.Join(lines, "\n") + "\n"
}

// unixInstallCommand wraps the install command cmd of an SSH join. The whole is
// one `sh -c` argument, because the login shell of the machine can be any shell.
func (s *Service) unixInstallCommand(ctx context.Context, osName, cmd string) (string, error) {
	picks, err := s.agentRefs(ctx, osName)
	if err != nil {
		return "", err
	}
	return "sh -c " + shQuote(unixScript(picks, "sudo -n", cmd)), nil
}

// manualInstall is the command a person pastes: the same download and check,
// then the install command. Linux and macOS get one `sh -c` line, Windows a
// PowerShell block that picks the installer of its CPU.
func (s *Service) manualInstall(ctx context.Context, osName, cmd string) (string, error) {
	picks, err := s.agentRefs(ctx, osName)
	if err != nil {
		return "", err
	}
	if osName != "windows" {
		return "sh -c " + shQuote(unixScript(picks, "sudo", cmd)), nil
	}
	pin := strings.ToLower(strings.ReplaceAll(s.o.Fingerprint, ":", ""))
	var b strings.Builder
	b.WriteString("$crsp = Join-Path $env:ProgramFiles 'crsp\\crsp.exe'\n")
	b.WriteString("if (-not (Test-Path $crsp)) {\n")
	for i, arch := range []string{"AMD64", "ARM64"} {
		ref := picks[i]
		if ref.SelfSigned {
			if !shaRe.MatchString(pin) {
				return "", errors.New("this controller has no certificate fingerprint to pin the installer download to")
			}
			ref.PinSHA256 = pin
		}
		kw := "if"
		if i == 1 {
			kw = "elseif"
		}
		fmt.Fprintf(&b, "%s ($env:PROCESSOR_ARCHITECTURE -eq '%s') {\n%s}\n", kw, arch, winDownloadScript(ref))
	}
	b.WriteString("}\n")
	b.WriteString("& $crsp " + strings.TrimPrefix(cmd, "crsp.exe ") + "\n")
	return b.String(), nil
}
