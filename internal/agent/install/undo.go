package install

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	agentv1 "github.com/luis-mc/ci-runners-shared-pool/internal/gen/agent/v1"
)

var pkgNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9.+-]*$`)

// Undo removes only what the recorded list says a run added (DEP10, D-DEP-7).
// It never removes software that was there before, and it never touches a WSL
// distro other than the crsp-owned one. The final event lists what is left.
func (r *Runner) Undo(ctx context.Context, req *agentv1.DepsUndo) {
	j := &job{r: r, id: req.GetJobId(), name: req.GetMachineName(), role: req.GetMachineRole(), os: r.cfg.Sys.GOOS(), phases: []string{"undo"}}
	sys := r.cfg.Sys
	var left []*agentv1.DepsAdded
	var pkgs []string
	var failed []string
	fail := func(a *agentv1.DepsAdded, why string) {
		left = append(left, a)
		failed = append(failed, why)
	}
	if len(req.GetAdded()) == 0 {
		j.status("undo", "Nothing was recorded as added, so nothing is removed.")
		j.final("undo", "done", "", "")
		return
	}
	// Order: stop the service, remove packages, then the source and the key.
	by := func(kind string) []*agentv1.DepsAdded {
		var out []*agentv1.DepsAdded
		for _, a := range req.GetAdded() {
			if a.GetKind() == kind {
				out = append(out, a)
			}
		}
		return out
	}
	known := map[string]bool{"systemd-enable": true, "apt-package": true, "apt-repo-file": true, "apt-key": true, "wsl-distro": true, "wsl-feature": true, "windows-installer": true}
	for _, a := range req.GetAdded() {
		if !known[a.GetKind()] {
			fail(a, fmt.Sprintf("crsp does not know how to undo %q", a.GetKind()))
		}
	}
	step := func(a *agentv1.DepsAdded, argv []string, what string) {
		if ctx.Err() != nil {
			left = append(left, a)
			return
		}
		if err := r.mustRun(ctx, j, "undo", what, argv); err != nil {
			fail(a, err.Error())
		}
	}
	for _, a := range by("systemd-enable") {
		if a.GetRef() != "docker" {
			fail(a, "crsp only turns off the docker service")
			continue
		}
		step(a, []string{"systemctl", "disable", "--now", "docker"}, "Turning the Docker service off")
	}
	var pkgAdded []*agentv1.DepsAdded
	for _, a := range by("apt-package") {
		if !pkgNameRe.MatchString(a.GetRef()) {
			fail(a, fmt.Sprintf("%q is not a package name", a.GetRef()))
			continue
		}
		pkgs = append(pkgs, a.GetRef())
		pkgAdded = append(pkgAdded, a)
	}
	if len(pkgs) > 0 {
		step(pkgAdded[0], append([]string{"apt-get", "remove", "-y"}, pkgs...), "Removing "+strings.Join(pkgs, ", "))
		// step records only the first on failure; keep the rest left too.
		if len(left) > 0 && left[len(left)-1] == pkgAdded[0] {
			left = append(left, pkgAdded[1:]...)
		}
	}
	for _, a := range by("apt-repo-file") {
		r.removeFixed(j, a, RepoPath, &left, &failed)
	}
	for _, a := range by("apt-key") {
		r.removeFixed(j, a, KeyPath, &left, &failed)
	}
	for _, a := range by("wsl-distro") {
		if a.GetRef() != DistroName {
			fail(a, "crsp only removes its own WSL distro, "+DistroName)
			continue
		}
		step(a, []string{"wsl", "--unregister", DistroName}, "Removing the "+DistroName+" WSL distro")
	}
	for _, a := range by("wsl-feature") {
		var list []string
		code, _ := r.cfgRunCapture(ctx, j, []string{"wsl", "--list", "--quiet"}, &list)
		other := false
		for _, l := range list {
			if l = strings.TrimSpace(l); l != "" && !strings.EqualFold(l, DistroName) {
				other = true
			}
		}
		if code != 0 || other {
			fail(a, "WSL stays: other WSL distros use it (or they could not be listed). Turn it off in Windows Features yourself if you want it gone")
			continue
		}
		step(a, []string{"wsl", "--uninstall"}, "Removing WSL")
	}
	for _, a := range by("windows-installer") {
		fail(a, a.GetRef()+" stays: uninstall it from Windows Settings, Apps")
	}
	_ = sys
	j.added = left
	if len(failed) > 0 {
		j.final("undo", "failed", "Not everything could be removed: "+strings.Join(failed, "; ")+".", "")
		return
	}
	j.final("undo", "done", "", "")
}

func (r *Runner) removeFixed(j *job, a *agentv1.DepsAdded, want string, left *[]*agentv1.DepsAdded, failed *[]string) {
	if a.GetRef() != want {
		*left = append(*left, a)
		*failed = append(*failed, fmt.Sprintf("crsp only removes %s", want))
		return
	}
	j.status("undo", "Removing "+want)
	if err := r.cfg.Sys.Remove(want); err != nil {
		*left = append(*left, a)
		*failed = append(*failed, err.Error())
	}
}
