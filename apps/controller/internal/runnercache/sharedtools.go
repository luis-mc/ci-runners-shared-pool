package runnercache

import (
	"fmt"
	"path"
	"strings"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend/sys"
)

// Shared tool folders of the runners of one repository on one machine
// (caches R5.57). On 20 Nov a second runner on a machine got `Permission
// denied` (errno 13) on the folders the first runner's user had made. crsp
// therefore makes the folders itself, once, with group access for every runner
// user of the repository, and gives each runner its own build-output folder
// inside them so two builds never write one target.

// The shared folders under the shared tools root.
const (
	SharedCargoTarget = "cargo-target"
	SharedCargoHome   = "cargo-home"
	SharedRustupHome  = "rustup-home"
	SharedXwin        = "xwin"
	SharedToolCache   = "tool-cache"
)

// EnvXwinCache is the folder cargo-xwin downloads the Windows SDK into.
const EnvXwinCache = "XWIN_CACHE_DIR"

// SharedToolFolders are the folders every runner user of the repository may
// read and write, in the order they are made.
var SharedToolFolders = []string{SharedCargoTarget, SharedCargoHome, SharedRustupHome, SharedXwin, SharedToolCache}

// SharedTools says where the shared folders are and which group owns them.
// The zero value is off: a runner keeps its own folders (R5.39).
type SharedTools struct {
	// Root is the shared tools folder of the repository on this machine.
	Root string
	// Group is the group every runner user of the repository belongs to.
	Group string
}

// On says whether shared tool folders are used.
func (s SharedTools) On() bool { return s.Root != "" && s.Group != "" }

// RepoGroup is the group name of the runner users of one repository.
func RepoGroup(repoID string) string { return "crsp-repo-" + repoID }

// BuildDir is the build-output folder of one runner inside the shared Rust
// target folder: `cargo-target/<runner>`.
func (s SharedTools) BuildDir(join func(...string) string, runner string) string {
	return join(s.Root, SharedCargoTarget, runner)
}

// Env is what a runner using the shared folders exports to its jobs. Only the
// build output is the runner's own; downloads are shared.
func (s SharedTools) Env(join func(...string) string, runner string) map[string]string {
	return map[string]string{
		EnvRustDir:   s.BuildDir(join, runner),
		EnvCargoDir:  join(s.Root, SharedCargoHome),
		EnvRustupDir: join(s.Root, SharedRustupHome),
		EnvXwinCache: join(s.Root, SharedXwin),
		EnvToolCache: join(s.Root, SharedToolCache),
	}
}

// ValidRunnerFolderName says whether a runner name is one plain folder name.
func ValidRunnerFolderName(name string) bool {
	return name != "" && name != "." && name != ".." && !strings.ContainsAny(name, `/\:`)
}

// LinuxSharePlan is the calls that make the shared folders and give the group
// read and write access to what is there now and to everything made later:
// group ownership, the setgid bit (new files keep the group), group write, and
// POSIX ACLs including default ACLs, so a file made under a user's umask of
// 022 is still writable by the group. Run it as root; it is safe to repeat.
func LinuxSharePlan(s SharedTools, runner string) ([]sys.Command, error) {
	if !s.On() {
		return nil, nil
	}
	if !ValidRunnerFolderName(runner) {
		return nil, fmt.Errorf("%q is not a plain runner name", runner)
	}
	dirs := []string{s.Root}
	for _, f := range SharedToolFolders {
		dirs = append(dirs, path.Join(s.Root, f))
	}
	build := s.BuildDir(path.Join, runner)
	cmds := []sys.Command{
		{Name: "mkdir", Args: append([]string{"-p", "--"}, append(dirs[1:], build)...)},
		{Name: "chgrp", Args: []string{"-R", s.Group, s.Root}},
		{Name: "chmod", Args: []string{"-R", "g+rwX", s.Root}},
		// setgid on every folder: a file made later keeps the group.
		{Name: "find", Args: []string{s.Root, "-type", "d", "-exec", "chmod", "g+s", "{}", "+"}},
		{Name: "setfacl", Args: []string{"-R", "-m", "g:" + s.Group + ":rwX", s.Root}},
		{Name: "setfacl", Args: []string{"-R", "-d", "-m", "g:" + s.Group + ":rwX", s.Root}},
	}
	return cmds, nil
}

// WindowsSharePlan is the equivalent on Windows: the group gets Modify,
// inherited by every file and folder made later (the Windows form of a default
// ACL). The runner accounts all belong to `crsp-runners` (runners R5.14), so
// that group is the one given access there.
func WindowsSharePlan(s SharedTools) []sys.Command {
	if !s.On() {
		return nil
	}
	cmds := make([]sys.Command, 0, len(SharedToolFolders)+1)
	for _, f := range append([]string{""}, SharedToolFolders...) {
		dir := s.Root
		if f != "" {
			dir = s.Root + `\` + f
		}
		cmds = append(cmds, sys.Command{Name: "cmd", Args: []string{"/c", "if", "not", "exist", dir, "mkdir", dir}})
	}
	cmds = append(cmds, sys.Command{Name: "icacls", Args: []string{s.Root, "/grant", s.Group + ":(OI)(CI)(M)", "/T", "/C"}})
	return cmds
}

// FixCommands is the same fix as text, for the Doctor to show (caches R5.57).
func FixCommands(goos string, s SharedTools, runner string) string {
	var cmds []sys.Command
	if goos == "windows" {
		cmds = WindowsSharePlan(s)
	} else {
		cmds, _ = LinuxSharePlan(s, runner)
	}
	lines := make([]string, 0, len(cmds))
	for _, c := range cmds {
		lines = append(lines, c.String())
	}
	return strings.Join(lines, " && ")
}

// ProbeCommand makes the Linux call that tries to create and remove a file in
// dir as the runner's own user, which is the test that failed on 20 Nov (an
// agent running as root would always succeed).
func ProbeCommand(user, dir string) sys.Command {
	return sys.Command{Name: "runuser", Args: []string{"-u", user, "--", "sh", "-c",
		`f=$(mktemp "$1/.crsp-probe-XXXXXX") && rm -f "$f"`, "sh", dir}}
}

// FolderProbe is what one shared folder gave when the runner's user tried to
// write it: Err is empty when it could.
type FolderProbe struct {
	Dir string
	Err string
}
