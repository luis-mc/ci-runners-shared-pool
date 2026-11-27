package dockercompact

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	dockerDistro  = "docker-desktop"
	dataMount     = "/mnt/docker-desktop-disk"
	dockerAppPath = `C:\Program Files\Docker\Docker\Docker Desktop.exe`
)

// windows is the compact of Docker Desktop's WSL2 disk files, in this order:
// find the files, trim inside the distro, stop Docker Desktop, shut WSL down,
// compact each file, start Docker Desktop and wait until `docker info` answers.
// The start runs after any failure from the stop on.
func (c *run) windows(ctx context.Context) error {
	if !c.h.Elevated() {
		return ErrNotElevated
	}
	c.step("find")
	paths := c.findDisks(ctx)
	for _, p := range paths {
		if size, ok := c.h.FileSize(p); ok {
			c.r.Files = append(c.r.Files, DiskFile{Path: p, Before: size, After: size})
			c.say("disk file %s is %s", p, FormatBytes(size))
		}
	}
	if len(c.r.Files) == 0 {
		return errors.New("no Docker Desktop disk file was found; nothing was stopped")
	}
	if used, ok := c.usedInside(ctx); ok {
		c.r.UsedBefore = used
		c.say("Docker uses %s inside", FormatBytes(used))
	}

	// Whether /dev/kvm is there now: after the restart it must be again, or the
	// pool's microVM jobs fail (caches R5.58).
	kvmBefore := c.kvmPresent(ctx)
	c.r.KVMBefore = kvmBefore

	c.step("trim")
	if _, err := c.exec(ctx, TrimTimeout, "wsl", "-d", dockerDistro, "-e", "fstrim", "-av"); err != nil {
		// A failed trim only means less is reclaimed; the compact still works.
		c.say("trim did not finish: %v", err)
	}

	var failure error
	c.step("stop")
	if err := c.stopDocker(ctx); err != nil {
		failure = fmt.Errorf("stop Docker Desktop: %w", err)
	}
	if failure == nil {
		c.step("shutdown")
		if _, err := c.exec(ctx, StopTimeout, "wsl", "--shutdown"); err != nil {
			failure = fmt.Errorf("wsl --shutdown: %w", err)
		}
	}
	if failure == nil {
		for i := range c.r.Files {
			c.step("compact")
			if err := c.compact(ctx, c.r.Files[i].Path); err != nil {
				failure = fmt.Errorf("compact %s: %w", c.r.Files[i].Path, err)
				break
			}
			if size, ok := c.h.FileSize(c.r.Files[i].Path); ok {
				c.r.Files[i].After = size
				c.say("disk file %s: %s to %s", c.r.Files[i].Path, FormatBytes(c.r.Files[i].Before), FormatBytes(size))
			}
		}
	}
	{
		// Docker comes back whatever happened above. A fresh context: the one
		// we got may be the one that ran out.
		if err := c.startDocker(context.WithoutCancel(ctx)); err != nil {
			if failure == nil {
				failure = err
			} else {
				failure = errors.Join(failure, err)
			}
		}
	}
	if used, ok := c.usedInside(context.WithoutCancel(ctx)); ok {
		c.r.UsedAfter = used
	}
	if kvmBefore && c.r.DockerRunning {
		// The restart unloads the KVM module: load it again and check.
		c.step("kvm")
		if err := c.restoreKVM(context.WithoutCancel(ctx)); err != nil {
			failure = errors.Join(failure, err)
		}
	}
	return failure
}

const kvmProbe = "test -e /dev/kvm"

// kvmPresent says whether /dev/kvm exists in the Docker VM.
func (c *run) kvmPresent(ctx context.Context) bool {
	res, err := c.h.Run(ctx, probeTimeout, "wsl", "-d", dockerDistro, "-e", "sh", "-c", kvmProbe)
	return err == nil && res.ExitCode == 0
}

// restoreKVM loads kvm_intel (or kvm_amd) and checks that /dev/kvm is back. The
// old pool did the same by hand after a compact. A machine that had no KVM
// before is never asked (caches R5.58).
func (c *run) restoreKVM(ctx context.Context) error {
	if c.kvmPresent(ctx) {
		c.r.KVMAfter = true
		c.say("/dev/kvm is present")
		return nil
	}
	_, _ = c.exec(ctx, probeTimeout, "wsl", "-d", dockerDistro, "-e", "sh", "-c", "modprobe kvm_intel || modprobe kvm_amd")
	if c.kvmPresent(ctx) {
		c.r.KVMAfter = true
		c.say("loaded the KVM module; /dev/kvm is present")
		return nil
	}
	return errors.New("/dev/kvm is not back after the restart; microVM jobs will fail until it is (load kvm_intel or kvm_amd in the Docker VM)")
}

// findDisks lists the disk files Docker Desktop uses, with no duplicates, from
// its settings, the WSL registry and the default folders.
func (c *run) findDisks(ctx context.Context) []string {
	var found []string
	seen := map[string]bool{}
	add := func(p string) {
		p = strings.TrimSpace(p)
		if p == "" || !strings.HasSuffix(strings.ToLower(p), ".vhdx") {
			return
		}
		key := strings.ToLower(strings.ReplaceAll(p, "/", `\`))
		if !seen[key] {
			seen[key] = true
			found = append(found, p)
		}
	}
	local, roaming := c.h.Getenv("LOCALAPPDATA"), c.h.Getenv("APPDATA")
	// Docker Desktop settings: the data folder or WSL distro folder, when moved.
	if roaming != "" {
		for _, name := range []string{"settings-store.json", "settings.json"} {
			txt, err := c.h.ReadFile(winJoin(roaming, "Docker", name))
			if err != nil {
				continue
			}
			for _, dir := range settingsFolders(txt) {
				add(winJoin(dir, "DockerDesktopWSL", "disk", "docker_data.vhdx"))
				add(winJoin(dir, "disk", "docker_data.vhdx"))
				add(winJoin(dir, "docker_data.vhdx"))
				add(winJoin(dir, "ext4.vhdx"))
			}
		}
	}
	// WSL: the folder of each distro whose name starts with docker-desktop.
	res, err := c.h.Run(ctx, probeTimeout, "reg", "query", `HKCU\Software\Microsoft\Windows\CurrentVersion\Lxss`, "/s")
	if err == nil && res.ExitCode == 0 {
		for _, dir := range lxssBasePaths(normalize(res.Out)) {
			add(winJoin(dir, "ext4.vhdx"))
			add(winJoin(dir, "docker_data.vhdx"))
		}
	}
	if local != "" {
		add(winJoin(local, "Docker", "wsl", "disk", "docker_data.vhdx"))
		add(winJoin(local, "Docker", "wsl", "data", "ext4.vhdx"))
		add(winJoin(local, "Docker", "wsl", "main", "ext4.vhdx"))
	}
	var existing []string
	for _, p := range found {
		if _, ok := c.h.FileSize(p); ok {
			existing = append(existing, p)
		}
	}
	return existing
}

// settingsFolders reads the folders Docker Desktop settings name for its data.
func settingsFolders(txt string) []string {
	var m map[string]json.RawMessage
	if json.Unmarshal([]byte(strings.TrimPrefix(txt, "\ufeff")), &m) != nil {
		return nil
	}
	var out []string
	for _, k := range []string{"CustomWslDistroDir", "customWslDistroDir", "dataFolder", "DataFolder", "wslDistroDir"} {
		raw, ok := m[k]
		if !ok {
			continue
		}
		var s string
		if json.Unmarshal(raw, &s) == nil && strings.TrimSpace(s) != "" {
			out = append(out, s)
		}
	}
	return out
}

// lxssBasePaths reads `reg query ...\Lxss /s`: for each distro key it keeps the
// BasePath of the ones named docker-desktop*. CRLF and UTF-16 leftovers are
// removed before it gets here.
func lxssBasePaths(out string) []string {
	var paths []string
	var name, base string
	flush := func() {
		if strings.HasPrefix(strings.ToLower(name), dockerDistro) && base != "" {
			paths = append(paths, strings.TrimPrefix(base, `\\?\`))
		}
		name, base = "", ""
	}
	for _, ln := range strings.Split(out, "\n") {
		ln = strings.TrimSpace(strings.TrimRight(ln, "\r"))
		switch {
		case strings.HasPrefix(strings.ToUpper(ln), "HKEY_"):
			flush()
		default:
			f := strings.Fields(ln)
			if len(f) >= 3 && f[1] == "REG_SZ" {
				val := strings.Join(f[2:], " ")
				switch f[0] {
				case "DistributionName":
					name = val
				case "BasePath":
					base = val
				}
			}
		}
	}
	flush()
	return paths
}

// usedInside is the bytes used on the Docker data disk inside the distro.
func (c *run) usedInside(ctx context.Context) (int64, bool) {
	for _, mount := range []string{dataMount, "/var/lib/docker"} {
		res, err := c.h.Run(ctx, probeTimeout, "wsl", "-d", dockerDistro, "-e", "df", "-P", "-B1", mount)
		if err != nil || res.ExitCode != 0 {
			continue
		}
		if used, ok := parseDfUsed(normalize(res.Out)); ok {
			return used, true
		}
	}
	return 0, false
}

// parseDfUsed reads the Used column of the last line of `df -P -B1`.
func parseDfUsed(out string) (int64, bool) {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) < 2 {
		return 0, false
	}
	f := strings.Fields(lines[len(lines)-1])
	if len(f) < 4 {
		return 0, false
	}
	n, err := strconv.ParseInt(f[2], 10, 64)
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}

func (c *run) stopDocker(ctx context.Context) error {
	_, err := c.exec(ctx, StopTimeout, "docker", "desktop", "stop")
	if err == nil {
		return nil
	}
	c.say("docker desktop stop did not work (%v); closing the app", err)
	if _, err := c.exec(ctx, StopTimeout, "taskkill", "/IM", "Docker Desktop.exe", "/T", "/F"); err != nil {
		return err
	}
	return nil
}

// compact shrinks one file: Optimize-VHD when the Hyper-V tools exist,
// diskpart through a script file otherwise.
func (c *run) compact(ctx context.Context, path string) error {
	if res, err := c.h.Run(ctx, probeTimeout, "powershell", "-NoProfile", "-NonInteractive", "-Command", "if (Get-Command Optimize-VHD -ErrorAction SilentlyContinue) { exit 0 } else { exit 3 }"); err == nil && res.ExitCode == 0 {
		cmd := "Optimize-VHD -Path " + psQuote(path) + " -Mode Full -ErrorAction Stop"
		_, err := c.exec(ctx, CompactTimeout, "powershell", "-NoProfile", "-NonInteractive", "-Command", cmd)
		return err
	}
	script, err := c.h.WriteTemp("crsp-compact-*.txt", diskpartScript(path, true))
	if err != nil {
		return fmt.Errorf("write the diskpart script: %w", err)
	}
	defer c.h.Remove(script)
	_, runErr := c.exec(ctx, CompactTimeout, "diskpart", "/s", script)
	if runErr != nil {
		// Never leave the file attached: Docker could not start on it.
		detach, werr := c.h.WriteTemp("crsp-detach-*.txt", diskpartDetach(path))
		if werr == nil {
			defer c.h.Remove(detach)
			if _, derr := c.exec(context.WithoutCancel(ctx), StopTimeout, "diskpart", "/s", detach); derr != nil {
				c.say("detach after the failure: %v", derr)
			}
		}
	}
	return runErr
}

func diskpartScript(path string, readonly bool) string {
	attach := "attach vdisk"
	if readonly {
		attach += " readonly"
	}
	return strings.Join([]string{`select vdisk file="` + path + `"`, attach, "compact vdisk", "detach vdisk", "exit"}, "\r\n") + "\r\n"
}

func diskpartDetach(path string) string {
	return strings.Join([]string{`select vdisk file="` + path + `"`, "detach vdisk", "exit"}, "\r\n") + "\r\n"
}

// startDocker starts Docker Desktop and waits until `docker info` answers.
func (c *run) startDocker(ctx context.Context) error {
	c.step("start")
	if _, err := c.exec(ctx, StartTimeout, "docker", "desktop", "start"); err != nil {
		c.say("docker desktop start did not work (%v); starting the app", err)
		if _, err2 := c.exec(ctx, StartTimeout, "cmd", "/c", "start", "", dockerAppPath); err2 != nil {
			return fmt.Errorf("start Docker Desktop: %w", err2)
		}
	}
	c.step("wait")
	return c.waitReady(ctx)
}

func (c *run) waitReady(ctx context.Context) error {
	for waited := time.Duration(0); ; waited += PollEvery {
		res, err := c.h.Run(ctx, probeTimeout, "docker", "info")
		if err == nil && res.ExitCode == 0 {
			c.r.DockerRunning = true
			c.say("docker info answers")
			return nil
		}
		if waited >= ReadyTimeout {
			return fmt.Errorf("`docker info` did not answer within %s; start Docker Desktop by hand", ReadyTimeout)
		}
		if err := c.h.Sleep(ctx, PollEvery); err != nil {
			return fmt.Errorf("waiting for Docker: %w", err)
		}
	}
}

func winJoin(parts ...string) string {
	return strings.Join(parts, `\`)
}

func psQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
