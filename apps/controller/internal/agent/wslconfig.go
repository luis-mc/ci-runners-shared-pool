package agent

import (
	agentv1 "github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/gen/agent/v1"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/poolsize"
)

// WSLConfigScript prints the `.wslconfig` of the person at the console, or of
// the account the agent runs as when nobody is signed in. The agent runs as
// LocalSystem (decision D41), whose own profile never holds the file, so the
// console user's profile is tried first. It prints nothing when no file exists.
const WSLConfigScript = `$ErrorActionPreference='SilentlyContinue';` +
	`$u=(Get-CimInstance Win32_ComputerSystem).UserName;$p=$null;` +
	`if($u){$p=Join-Path $env:SystemDrive ('Users\'+$u.Split('\')[-1]+'\.wslconfig')};` +
	`if(-not $p -or -not (Test-Path $p)){$p=Join-Path $env:USERPROFILE '.wslconfig'};` +
	`if(Test-Path $p){Get-Content -Raw $p}`

// applyWSLConfig fills the WSL facts from `.wslconfig` (machines R5.54). It
// reads a file and never writes one: changing the file is the owner's call,
// and a change needs `wsl --shutdown`. Elsewhere than Windows it does nothing.
func applyWSLConfig(f *agentv1.Facts, sys Sys) {
	if sys.GOOS() != "windows" {
		return
	}
	res, ok := sys.Run([]string{"powershell", "-NoProfile", "-Command", WSLConfigScript})
	if !ok || res.Code != 0 || len(res.Out) == 0 {
		return
	}
	c := poolsize.ParseWSLConfig(res.Out)
	f.WslConfigPresent = true
	f.WslMemoryBytes = c.MemoryBytes
	f.WslProcessors = int32(c.Processors) //nolint:gosec // small
	f.WslNestedVirtualization = c.NestedVirtualization
}
