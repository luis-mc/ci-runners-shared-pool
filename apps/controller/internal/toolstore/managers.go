package toolstore

import (
	"errors"
	"fmt"
	"strings"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/addresses"
)

// GOOS values the store serves. Linux keeps the image profiles and has no store.
const (
	Windows = "windows"
	Darwin  = "darwin"
)

// ErrNoRecipe says the plan names a tool that has no install recipe on this OS.
var ErrNoRecipe = errors.New("toolstore: no install recipe for this tool on this OS")

// StepKind is what one install step does.
type StepKind string

// The step kinds.
const (
	// StepRun runs a program with Argv and Env; the program is the tool's own manager.
	StepRun StepKind = "run"
	// StepDownload fetches URL into the file To (relative to the version folder) and checks it against the
	// SHA-256 the project publishes at the final URL plus ChecksumSuffix.
	StepDownload StepKind = "download"
	// StepUnpack unpacks the downloaded archive File into the version folder, dropping StripDirs leading
	// folders (regular files and folders only; a link or ".." path refuses the archive).
	StepUnpack StepKind = "unpack"
)

// Step is one step of a recipe.
type Step struct {
	Kind StepKind
	// Argv is the command line of a StepRun. Argv[0] may be a path relative to the version folder.
	Argv []string
	Env  map[string]string
	// URL, ChecksumSuffix and To belong to StepDownload. ChecksumSuffix is ".sha256" or ".sha256.txt" when
	// the project publishes a checksum file next to the archive; "" when it publishes none, and the marker
	// then records "unchecked".
	URL, ChecksumSuffix, To string
	// SumsURL and SumsName replace ChecksumSuffix when the project publishes one list of checksums for a
	// version (Node's SHASUMS256.txt): the SHA-256 is the line of that list whose file name is SumsName.
	SumsURL, SumsName string
	// File and StripDirs belong to StepUnpack.
	File      string
	StripDirs int
}

// Recipe is how one tool version is installed into a staging folder Dest: the manager's name and its steps.
// Every manager home (PYENV_ROOT, SDKMAN_DIR, RUSTUP_HOME) is inside Dest, so a
// version folder holds everything its tool needs and removing the folder removes the version.
type Recipe struct {
	Manager string
	Steps   []Step
}

func archSuffix(arch string) string {
	if arch == "arm64" {
		return "arm64"
	}
	return "amd64"
}

// Sources are the base addresses the recipes download from. DefaultSources gives the official ones from
// internal/addresses; an owner's mirror replaces them. No base has a trailing slash.
type Sources struct {
	RustupDist, GoDownload, Adoptium, DotnetInstall, PnpmReleases, NodeDist string
}

// DefaultSources are the official download addresses.
func DefaultSources() Sources {
	return Sources{RustupDist: addresses.DefaultRustupDistURL, GoDownload: addresses.DefaultGoDownloadURL,
		Adoptium: addresses.DefaultAdoptiumURL, DotnetInstall: addresses.DefaultDotnetInstallURL, PnpmReleases: addresses.DefaultPnpmReleaseURL,
		NodeDist: addresses.DefaultNodeDistURL}
}

// RecipeFor builds the install recipe of tool for goos and arch ("amd64" or "arm64") into dest, a staging
// folder. It is pure: it reads and runs nothing. The managers are the tool's own (owner decision 21 Nov):
// rustup, pyenv (pyenv-win), SDKMAN on macOS, the official archive for Go, the dotnet-install script for
// .NET, the official JDK archive on Windows. Node is the official nodejs.org archive of the version, checked
// against that version's SHASUMS256.txt, with no version manager (owner decision 22 Nov). Xcode has no recipe
// on any OS: the owner installs it and the store only finds it (xcode.go). A tool with no manager on an OS
// is unpacked from the official archive, checked where a checksum is published.
func RecipeFor(goos, arch string, src Sources, t Tool, dest string) (Recipe, error) {
	if !ValidVersion(t.Version) {
		return Recipe{}, fmt.Errorf("toolstore: %q is not a valid version", t.Version)
	}
	if goos != Windows && goos != Darwin {
		return Recipe{}, fmt.Errorf("toolstore: no store on %s", goos)
	}
	win, v := goos == Windows, t.Version
	j := func(p ...string) string { return join(goos, append([]string{dest}, p...)...) }
	sh := func(env map[string]string, script string) Step {
		return Step{Kind: StepRun, Argv: []string{"/bin/bash", "-c", script}, Env: env}
	}
	switch t.Tool {
	case ToolRust:
		env := map[string]string{"RUSTUP_HOME": j("rustup"), "CARGO_HOME": j("cargo")}
		triple, exe, ext := "x86_64-apple-darwin", "rustup-init", ""
		if arch == "arm64" {
			triple = "aarch64-apple-darwin"
		}
		if win {
			triple, exe, ext = "x86_64-pc-windows-msvc", "rustup-init.exe", ".exe"
			if arch == "arm64" {
				triple = "aarch64-pc-windows-msvc"
			}
		}
		return Recipe{"rustup", []Step{
			{Kind: StepDownload, URL: src.RustupDist + "/" + triple + "/rustup-init" + ext, ChecksumSuffix: ".sha256", To: exe},
			{Kind: StepRun, Argv: []string{j(exe), "-y", "--no-modify-path", "--profile", "minimal", "--default-toolchain", v}, Env: env},
		}}, nil
	case ToolNode:
		osName, ext := "darwin", ".tar.gz"
		if win {
			osName, ext = "win", ".zip"
		}
		cpu := map[bool]string{true: "arm64", false: "x64"}[arch == "arm64"]
		file := "node-v" + v + "-" + osName + "-" + cpu + ext
		base := src.NodeDist + "/v" + v
		return Recipe{"archive", []Step{
			{Kind: StepDownload, URL: base + "/" + file, SumsURL: base + "/SHASUMS256.txt", SumsName: file, To: file},
			{Kind: StepUnpack, File: file, StripDirs: 1},
		}}, nil
	case ToolPython:
		if win {
			env := map[string]string{"PYENV": j("pyenv-win"), "PYENV_ROOT": j("pyenv-win"), "PYENV_HOME": j("pyenv-win")}
			return Recipe{"pyenv-win", []Step{{Kind: StepRun, Argv: []string{"pyenv", "install", v}, Env: env}}}, nil
		}
		env := map[string]string{"PYENV_ROOT": j("pyenv")}
		return Recipe{"pyenv", []Step{{Kind: StepRun, Argv: []string{"pyenv", "install", v}, Env: env}}}, nil
	case ToolJava:
		if win {
			url := src.Adoptium + "/jdk-" + v + "/windows/" + map[bool]string{true: "aarch64", false: "x64"}[arch == "arm64"] + "/jdk/hotspot/normal/eclipse"
			return Recipe{"archive", []Step{
				{Kind: StepDownload, URL: url, ChecksumSuffix: ".sha256.txt", To: "jdk.zip"},
				{Kind: StepUnpack, File: "jdk.zip", StripDirs: 1},
			}}, nil
		}
		env := map[string]string{"SDKMAN_DIR": j("sdkman")}
		return Recipe{"sdkman", []Step{sh(env, `. "$SDKMAN_DIR/bin/sdkman-init.sh" && sdk install java `+v)}}, nil
	case ToolGo:
		name, file := "go"+v+"."+goos+"-"+archSuffix(arch), ""
		file = name + ".tar.gz"
		if win {
			file = name + ".zip"
		}
		return Recipe{"archive", []Step{
			{Kind: StepDownload, URL: src.GoDownload + "/" + file, ChecksumSuffix: ".sha256", To: file},
			{Kind: StepUnpack, File: file, StripDirs: 1},
		}}, nil
	case ToolDotnet:
		if win {
			return Recipe{"dotnet-install", []Step{
				{Kind: StepDownload, URL: src.DotnetInstall + "/dotnet-install.ps1", To: "dotnet-install.ps1"},
				{Kind: StepRun, Argv: []string{"powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", j("dotnet-install.ps1"), "-Version", v, "-InstallDir", j("dotnet"), "-NoPath"}},
			}}, nil
		}
		return Recipe{"dotnet-install", []Step{
			{Kind: StepDownload, URL: src.DotnetInstall + "/dotnet-install.sh", To: "dotnet-install.sh"},
			{Kind: StepRun, Argv: []string{"/bin/bash", j("dotnet-install.sh"), "--version", v, "--install-dir", j("dotnet"), "--no-path"}},
		}}, nil
	case ToolPnpm:
		// pnpm has no manager of its own here: the release binary, no checksum file published.
		file := map[bool]string{true: "pnpm-win-x64.exe", false: "pnpm-macos-" + map[bool]string{true: "arm64", false: "x64"}[arch == "arm64"]}[win]
		to := map[bool]string{true: "pnpm.exe", false: "pnpm"}[win]
		return Recipe{"archive", []Step{{Kind: StepDownload, URL: src.PnpmReleases + "/v" + v + "/" + file, To: to}}}, nil
	case ToolXcode:
		return Recipe{}, fmt.Errorf("%w: Xcode is installed by the owner, never by the store", ErrNoRecipe)
	}
	return Recipe{}, fmt.Errorf("%w: %s on %s", ErrNoRecipe, t.Tool, goos)
}

// Layout is what a job needs of one installed version: folders for PATH (in order) and variables.
type Layout struct {
	Path []string
	Env  map[string]string
}

// LayoutFor says where the tools of an installed version are, relative to its folder dir. The manager's own
// home variables are not part of it: a job never runs a manager (R5.117).
func LayoutFor(goos string, t Tool, dir string) Layout {
	j := func(p ...string) string { return join(goos, append([]string{dir}, p...)...) }
	v := t.Version
	win := goos == Windows
	switch t.Tool {
	case ToolRust:
		// The job's CARGO_HOME is its own (jobhome.go); the toolchain and the proxies are read from the store.
		return Layout{Path: []string{j("cargo", "bin")}, Env: map[string]string{"RUSTUP_HOME": j("rustup"), "RUSTUP_TOOLCHAIN": v, "RUSTUP_AUTO_INSTALL": "0"}}
	case ToolNode:
		if win {
			return Layout{Path: []string{dir}}
		}
		return Layout{Path: []string{j("bin")}}
	case ToolPython:
		if win {
			return Layout{Path: []string{j("pyenv-win", "versions", v), j("pyenv-win", "versions", v, "Scripts")}}
		}
		return Layout{Path: []string{j("pyenv", "versions", v, "bin")}}
	case ToolJava:
		home := j("sdkman", "candidates", "java", v)
		if win {
			home = dir
		}
		return Layout{Path: []string{join(goos, home, "bin")}, Env: map[string]string{"JAVA_HOME": home}}
	case ToolGo:
		return Layout{Path: []string{j("bin")}, Env: map[string]string{"GOROOT": dir, "GOTOOLCHAIN": "local"}}
	case ToolDotnet:
		return Layout{Path: []string{j("dotnet")}, Env: map[string]string{"DOTNET_ROOT": j("dotnet"), "DOTNET_CLI_TELEMETRY_OPTOUT": "1"}}
	case ToolPnpm:
		return Layout{Path: []string{dir}}
	}
	return Layout{} // Xcode has no folder in the store: BuildJobHome sets DEVELOPER_DIR from the Xcode found
}

// join joins path parts with the separator of goos, so a test on Linux can check a Windows path.
func join(goos string, parts ...string) string {
	sep := "/"
	if goos == Windows {
		sep = `\`
	}
	var out []string
	for i, p := range parts {
		if i > 0 {
			p = strings.Trim(p, `/\`)
		} else {
			p = strings.TrimRight(p, `/\`)
		}
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, sep)
}
