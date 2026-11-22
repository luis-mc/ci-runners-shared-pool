package toolstore

import (
	"context"
	"errors"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"
)

// Xcode is installed by the owner, never by the store: the store does not download it and never asks for an
// Apple ID (owner decision 22 Nov). It lists the Xcode apps found on the Mac and selects the one a job needs
// through DEVELOPER_DIR in the job's own environment, never through a global xcode-select.

// XcodeApp is one installed Xcode.
type XcodeApp struct {
	// App is the bundle, such as /Applications/Xcode-16.2.app.
	App string
	// Version is what xcodebuild -version says ("16.2").
	Version string
}

// DeveloperDir is the value of DEVELOPER_DIR that selects this Xcode.
func (x XcodeApp) DeveloperDir() string { return path.Join(x.App, "Contents", "Developer") }

// ErrXcodeMissing says the Xcode a job needs is not installed on this Mac.
var ErrXcodeMissing = errors.New("toolstore: the Xcode a job needs is not installed")

// XcodeFinder finds the installed Xcodes. The zero value uses the real Mac (DefaultXcodeFinder); tests give
// the two seams.
type XcodeFinder struct {
	// Glob lists paths matching a pattern, as filepath.Glob does.
	Glob func(pattern string) ([]string, error)
	// Version runs xcodebuild -version with DEVELOPER_DIR set to dev and returns its output.
	Version func(ctx context.Context, dev string) (string, error)
}

// XcodePattern is where the owner puts Xcode apps.
const XcodePattern = "/Applications/Xcode*.app"

// List returns the Xcodes whose version could be read, newest first. An app that cannot say its version
// (damaged, never opened) is left out.
func (f XcodeFinder) List(ctx context.Context) []XcodeApp {
	f = f.withDefaults()
	paths, _ := f.Glob(XcodePattern)
	var out []XcodeApp
	for _, app := range paths {
		x := XcodeApp{App: app}
		text, err := f.Version(ctx, x.DeveloperDir())
		if err != nil {
			continue
		}
		if x.Version = ParseXcodeVersion(text); x.Version != "" {
			out = append(out, x)
		}
	}
	sort.Slice(out, func(i, j int) bool { return versionLess(out[j].Version, out[i].Version) })
	return out
}

// Find returns the newest installed Xcode that is the requested version: "16.2" is 16.2 (also 16.2.0), and
// "16" is any 16.x. ErrXcodeMissing, worded for the owner, when there is none.
func (f XcodeFinder) Find(ctx context.Context, version string) (XcodeApp, error) {
	have := f.List(ctx)
	for _, x := range have {
		if xcodeMatches(version, x.Version) {
			return x, nil
		}
	}
	var names []string
	for _, x := range have {
		names = append(names, x.Version)
	}
	found := "no Xcode is installed"
	if len(names) > 0 {
		found = "installed: " + strings.Join(names, ", ")
	}
	return XcodeApp{}, fmt.Errorf("%w: %s", ErrXcodeMissing, XcodeInstallHint(version)+" ("+found+")")
}

// XcodeInstallHint says which Xcode to install, in words for the owner.
func XcodeInstallHint(version string) string {
	return "Xcode " + version + " is not installed on this Mac. Install Xcode " + version +
		" in /Applications (Xcode-" + version + ".app); the runner never downloads Xcode"
}

// ParseXcodeVersion reads the version from the output of xcodebuild -version ("Xcode 16.2\nBuild version
// 16C5032a"); "" when there is none.
func ParseXcodeVersion(out string) string {
	for _, line := range strings.Split(out, "\n") {
		if w := strings.Fields(line); len(w) == 2 && w[0] == "Xcode" && ValidVersion(w[1]) {
			return w[1]
		}
	}
	return ""
}

func trimZeros(v string) string {
	p := strings.Split(v, ".")
	for len(p) > 1 && p[len(p)-1] == "0" {
		p = p[:len(p)-1]
	}
	return strings.Join(p, ".")
}

func xcodeMatches(want, have string) bool {
	w, h := trimZeros(strings.TrimPrefix(want, "v")), trimZeros(have)
	return w == h || strings.HasPrefix(h, w+".")
}

// versionLess compares dotted versions number by number.
func versionLess(a, b string) bool {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var x, y int
		if i < len(pa) {
			x, _ = strconv.Atoi(pa[i])
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(pb[i])
		}
		if x != y {
			return x < y
		}
	}
	return false
}
