package container

import (
	"regexp"
	"strings"

	"github.com/luis-mc/ci-runners-shared-pool/images"
)

// Tool is one tool an image holds, with its pinned version when it has one.
type Tool struct {
	Name    string
	Version string
}

var (
	fromLine = regexp.MustCompile(`(?m)^FROM\s+ubuntu:([0-9.]+)`)
	argLine  = regexp.MustCompile(`(?m)^ARG\s+([A-Z_0-9]+)=(\S+)`)
)

// BaseTools lists what the base image always holds: the distribution, then the
// tools of the "base" layer, with the versions pinned in
// images/linux-base/Dockerfile (ARGs). It reads the embedded file, so the list
// cannot drift from the build.
func BaseTools() []Tool {
	var text string
	if b, err := images.FS.ReadFile("linux-base/Dockerfile"); err == nil {
		text = string(b)
	}
	args := map[string]string{}
	for _, m := range argLine.FindAllStringSubmatch(text, -1) {
		args[m[1]] = m[2]
	}
	version := map[string]string{
		"node":     args["NODE_VERSION"],
		"gh":       args["GH_VERSION"],
		"corepack": args["COREPACK_VERSION"],
	}
	var out []Tool
	if m := fromLine.FindStringSubmatch(text); m != nil {
		out = append(out, Tool{Name: "Ubuntu", Version: m[1]})
	}
	tools, _ := LayerTools("base")
	for _, t := range tools {
		out = append(out, Tool{Name: t, Version: majorOf(t, version[t])})
	}
	return out
}

// majorOf shortens node's version to its major ("26"), the way the screen
// says it; other tools keep the full pinned version.
func majorOf(name, v string) string {
	if name == "node" {
		return strings.SplitN(v, ".", 2)[0]
	}
	return v
}

// OptionalLayers are the layers a user can turn on: every layer but "base".
func OptionalLayers() []Layer {
	var out []Layer
	for _, l := range Layers {
		if l.Name != "base" {
			out = append(out, l)
		}
	}
	return out
}

// LayerAdds names the tools a layer adds, without the "$" variables (the
// playwright layer is a folder of browsers).
func LayerAdds(l Layer) []string {
	var out []string
	for _, t := range l.Tools {
		if strings.HasPrefix(t, "$") {
			continue
		}
		out = append(out, t)
	}
	if l.Name == "playwright" {
		out = []string{"chromium", "firefox", "webkit"}
	}
	return out
}
