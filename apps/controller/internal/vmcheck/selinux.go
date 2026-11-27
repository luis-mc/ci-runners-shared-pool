package vmcheck

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// The SELinux checks the old pool ran in the guest (caches R5.46, `selinux`):
// Enforcing, an rpm installs, an optional policy module loads and is listed,
// the service runs in the context the policy gives it, and no AVC denial is in
// the audit log of this boot.

// SELinuxSpec is `crsp vm selinux`.
type SELinuxSpec struct {
	GuestSpec
	// RPM is installed in the guest when set (a local file).
	RPM string
	// Module is a compiled policy module (.pp) loaded when set.
	Module string
	// Service is a systemd unit started after the install; with ContextType
	// the type of its main process is checked.
	Service     string
	ContextType string
	RunTimeout  time.Duration
}

// SELinuxFamily says whether a distro has SELinux in its cloud image. Debian
// and Ubuntu ship AppArmor, so a check of SELinux there would only fail.
func SELinuxFamily(distro string) bool {
	return strings.HasPrefix(distro, "fedora") || strings.HasPrefix(distro, "rocky")
}

// Validate refuses what cannot be checked.
func (s SELinuxSpec) Validate() error {
	switch {
	case !SELinuxFamily(s.Distro):
		return fmt.Errorf("%s has no SELinux in its image; use fedora-* or rocky-*", s.Distro)
	case s.ContextType != "" && s.Service == "":
		return errors.New("--context needs --service")
	case s.Service != "" && !validUnit(s.Service):
		return fmt.Errorf("service name %q is not a unit name", s.Service)
	case s.ContextType != "" && !validWord(s.ContextType):
		return fmt.Errorf("context type %q is not a SELinux type", s.ContextType)
	case s.Module != "" && !strings.HasSuffix(s.Module, ".pp"):
		return errors.New("the policy module must be a compiled .pp file")
	case s.RPM != "" && !strings.HasSuffix(s.RPM, ".rpm"):
		return errors.New("the package must be an .rpm file")
	}
	return nil
}

func validUnit(s string) bool {
	return s != "" && strings.Trim(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_.@:-") == ""
}

func validWord(s string) bool {
	return s != "" && strings.Trim(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_") == ""
}

// ModuleName is the policy module's name: the file name without ".pp".
func ModuleName(file string) string { return strings.TrimSuffix(filepath.Base(file), ".pp") }

// ParseSemodule lists the module names in `semodule -l` output ("name version" per line).
func ParseSemodule(text string) []string {
	var out []string
	for _, l := range strings.Split(text, "\n") {
		if f := strings.Fields(l); len(f) > 0 {
			out = append(out, f[0])
		}
	}
	return out
}

// HasModule says whether the module is in the list.
func HasModule(list []string, name string) bool {
	for _, m := range list {
		if m == name {
			return true
		}
	}
	return false
}

// ContextOfType returns the type of a SELinux context label
// ("system_u:system_r:httpd_t:s0" gives "httpd_t"), "" when it is not one.
func ContextOfType(label string) string {
	f := strings.Split(strings.TrimSpace(label), ":")
	if len(f) < 4 {
		return ""
	}
	return f[2]
}

// ModeVerdict is the plain reason `getenforce` is not good, "" when it is.
func ModeVerdict(getenforce string) string { return SELinuxVerdict(getenforce, nil) }

// SELinux runs the checks in a fresh guest. Each check is a step; the first
// that fails ends the run, as `run` does.
func SELinux(ctx context.Context, cat Catalogue, spec SELinuxSpec, deps GuestDeps, now func() time.Time) (RunOutcome, error) {
	if err := spec.Validate(); err != nil {
		return RunOutcome{}, err
	}
	if spec.RunTimeout < 0 {
		return RunOutcome{}, errors.New("the run timeout cannot be negative")
	}
	if spec.RunTimeout == 0 {
		spec.RunTimeout = DefaultRunTimeout
	}
	return WithGuest(ctx, cat, spec.GuestSpec, deps, now, func(ctx context.Context, g *Guest) ([]Step, error) {
		rctx, cancel := context.WithTimeout(ctx, spec.RunTimeout)
		defer cancel()
		var steps []Step
		// bad records a check that ran and found a problem.
		bad := func(name, command, problem string) {
			steps = append(steps, Step{Name: name, Command: command, Problem: problem})
		}
		// ask runs a read-only command in the guest and returns its output.
		ask := func(name, command string) (string, bool) {
			out, code, err := g.Output(rctx, command)
			switch {
			case rctx.Err() != nil:
				steps = append(steps, Step{Name: name, Command: command, TimedOut: true})
			case err != nil:
				steps = append(steps, Step{Name: name, Command: command, Err: err.Error()})
			case code != 0:
				steps = append(steps, Step{Name: name, Command: command, Code: code})
			default:
				return out, true
			}
			return out, false
		}

		mode, ok := ask("selinux mode", "getenforce")
		if !ok {
			return steps, nil
		}
		if v := ModeVerdict(mode); v != "" {
			bad("selinux mode", "getenforce", v)
			return steps, nil
		}
		steps = append(steps, Step{Name: "selinux mode", Command: "getenforce"})

		var files []string
		if spec.RPM != "" {
			files = append(files, spec.RPM)
		}
		if spec.Module != "" {
			files = append(files, spec.Module)
		}
		for _, f := range files {
			if err := g.CopyIn(rctx, f); err != nil {
				return nil, err
			}
		}
		run := func(name, command string) bool {
			st := g.Do(rctx, name, command, now)
			steps = append(steps, st)
			return !st.Failed()
		}
		if spec.RPM != "" && !run("install rpm", InDir("sudo dnf install -y "+ShellQuote("./"+filepath.Base(spec.RPM)))) {
			return steps, nil
		}
		if spec.Module != "" {
			if !run("load module", InDir("sudo semodule -i "+ShellQuote(filepath.Base(spec.Module)))) {
				return steps, nil
			}
			list, ok := ask("module listed", "sudo semodule -l")
			if !ok {
				return steps, nil
			}
			if !HasModule(ParseSemodule(list), ModuleName(spec.Module)) {
				bad("module listed", "sudo semodule -l", "module "+ModuleName(spec.Module)+" is not in the loaded list")
				return steps, nil
			}
			steps = append(steps, Step{Name: "module listed", Command: "sudo semodule -l"})
		}
		if spec.Service != "" {
			if !run("start service", "sudo systemctl start "+ShellQuote(spec.Service)) {
				return steps, nil
			}
			if spec.ContextType != "" {
				pidCmd := "systemctl show -p MainPID --value " + ShellQuote(spec.Service)
				pid, ok := ask("service context", pidCmd)
				if !ok {
					return steps, nil
				}
				pid = strings.TrimSpace(pid)
				if pid == "" || strings.Trim(pid, "0123456789") != "" || pid == "0" {
					bad("service context", pidCmd, spec.Service+" has no main process (MainPID "+pid+")")
					return steps, nil
				}
				label, ok := ask("service context", "ps -Z -o label= -p "+pid)
				if !ok {
					return steps, nil
				}
				if got := ContextOfType(label); got != spec.ContextType {
					bad("service context", "ps -Z -p "+pid, fmt.Sprintf("%s runs as %q, expected type %q", spec.Service, strings.TrimSpace(label), spec.ContextType))
					return steps, nil
				}
				steps = append(steps, Step{Name: "service context", Command: "ps -Z -p " + pid})
			}
		}
		audit, ok := ask("audit log", "sudo cat /var/log/audit/audit.log")
		if !ok {
			return steps, nil
		}
		if avc := CountAVC(audit); len(avc) > 0 {
			bad("avc denials", "sudo cat /var/log/audit/audit.log", SELinuxVerdict("Enforcing", avc))
			return steps, nil
		}
		steps = append(steps, Step{Name: "avc denials", Command: "sudo cat /var/log/audit/audit.log"})
		return steps, nil
	})
}
