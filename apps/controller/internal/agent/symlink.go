package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	agentv1 "github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/gen/agent/v1"
)

// SymlinkProber reads whether the agent's account can make symbolic links.
// Tests feed a fake.
type SymlinkProber func(ctx context.Context, sys Sys) *agentv1.SymlinkReport

// makeSymlink is os.Symlink; a test replaces it to fake a refused link.
var makeSymlink = os.Symlink

// ProbeSymlink is the real SymlinkProber (doctor R5.42, runners R5.68). On
// every OS other than Windows the answer is yes. On Windows it makes one link
// in a temporary folder, which works with the right
// `SeCreateSymbolicLinkPrivilege` or with Developer Mode, and it reads
// Developer Mode from the registry. It never grants a right, never turns
// Developer Mode on and removes the temporary folder.
func ProbeSymlink(ctx context.Context, sys Sys) *agentv1.SymlinkReport {
	if sys.GOOS() != "windows" {
		return &agentv1.SymlinkReport{Known: true, CanLink: true}
	}
	rep := &agentv1.SymlinkReport{Known: true, DeveloperMode: readDeveloperMode(sys)}
	dir, err := os.MkdirTemp("", "crsp-symlink-")
	if err != nil {
		return &agentv1.SymlinkReport{Error: "cannot make a temporary folder: " + err.Error()}
	}
	defer func() { _ = os.RemoveAll(dir) }()
	if err := ctx.Err(); err != nil {
		return &agentv1.SymlinkReport{Error: err.Error()}
	}
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("x"), 0o600); err != nil {
		return &agentv1.SymlinkReport{Error: "cannot write a probe file: " + err.Error()}
	}
	if err := makeSymlink(target, filepath.Join(dir, "link")); err != nil {
		// A refused link is the answer, not a failure to read.
		rep.CanLink = false
		return rep
	}
	rep.CanLink = true
	return rep
}

// readDeveloperMode reads the Windows Developer Mode switch (registry value
// AllowDevelopmentWithoutDevLicense = 1). A read that fails counts as off.
func readDeveloperMode(sys Sys) bool {
	res, ok := sys.Run([]string{"reg", "query", `HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\AppModelUnlock`, "/v", "AllowDevelopmentWithoutDevLicense"})
	if !ok || res.Code != 0 {
		return false
	}
	for _, line := range strings.Split(res.Out, "\n") {
		f := strings.Fields(line)
		if len(f) >= 3 && f[0] == "AllowDevelopmentWithoutDevLicense" {
			return strings.EqualFold(f[2], "0x1")
		}
	}
	return false
}
