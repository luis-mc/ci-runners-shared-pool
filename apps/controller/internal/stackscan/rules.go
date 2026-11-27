// Package stackscan reads the stack files of a repository at the commit of a
// run (registry R5.73 to R5.76, IMG2). It never clones and never runs code: it
// lists the commit's tree once and reads only the files the rules below name,
// through the GitHub contents API. It gives REG3 the file list through
// registry.StackSource and keeps findings (path, kind, size), not file text
// beyond a small in-memory cache by commit.
package stackscan

import (
	"path"
	"regexp"
	"strings"
)

// Kind is what a stack file is.
type Kind string

const (
	KindManifest     Kind = "manifest"
	KindLock         Kind = "lock"
	KindVersion      Kind = "version"
	KindDockerfile   Kind = "dockerfile"
	KindCompose      Kind = "compose"
	KindDevcontainer Kind = "devcontainer"
	KindCRSP         Kind = "crsp"
)

// MaxDepth is the number of directories a stack file may sit below the root.
const MaxDepth = 3

var skipDirs = map[string]bool{
	"node_modules": true, "vendor": true, ".git": true, "dist": true, "build": true,
	"target": true, ".venv": true, "venv": true, "third_party": true, ".gradle": true,
}

var (
	reRequirements = regexp.MustCompile(`^requirements[A-Za-z0-9._-]*\.txt$`)
	reCompose      = regexp.MustCompile(`^(docker-)?compose[A-Za-z0-9._-]*\.ya?ml$`)
	reDockerfile   = regexp.MustCompile(`^(Dockerfile(\.[A-Za-z0-9._-]+)?|[A-Za-z0-9._-]+\.Dockerfile)$`)
)

var byName = map[string]Kind{
	"package.json": KindManifest, "go.mod": KindManifest, "go.work": KindManifest,
	"Cargo.toml": KindManifest, "pyproject.toml": KindManifest, "pom.xml": KindManifest,
	"build.gradle": KindManifest, "build.gradle.kts": KindManifest,
	"gradle-wrapper.properties": KindManifest, "mise.toml": KindManifest,
	"pnpm-lock.yaml": KindLock, "package-lock.json": KindLock, "yarn.lock": KindLock,
	"Cargo.lock": KindLock, "uv.lock": KindLock,
	".tool-versions": KindVersion, ".nvmrc": KindVersion, ".node-version": KindVersion,
	".python-version": KindVersion, "rust-toolchain": KindVersion, "rust-toolchain.toml": KindVersion,
}

// Classify says whether p (repository-relative, forward slashes) is a stack
// file and what kind. Workflows are not stack files: the stack hash is about
// what the image holds, not what the pipeline says.
func Classify(p string) (Kind, bool) {
	if p == "" || strings.HasPrefix(p, "/") || strings.HasPrefix(p, "./") || strings.Contains(p, `\`) {
		return "", false
	}
	dir, base := path.Split(p)
	dirs := strings.Split(strings.TrimSuffix(dir, "/"), "/")
	if dir == "" {
		dirs = nil
	}
	if len(dirs) > MaxDepth {
		return "", false
	}
	for _, d := range dirs {
		if skipDirs[d] {
			return "", false
		}
	}
	root := len(dirs) == 0
	switch base {
	case ".crsp.yml":
		return KindCRSP, root
	case ".devcontainer.json":
		return KindDevcontainer, root
	case "devcontainer.json":
		// .devcontainer/devcontainer.json or .devcontainer/<name>/devcontainer.json
		if len(dirs) >= 1 && dirs[0] == ".devcontainer" && len(dirs) <= 2 {
			return KindDevcontainer, true
		}
		return "", false
	}
	if k, ok := byName[base]; ok {
		return k, true
	}
	switch {
	case reRequirements.MatchString(base):
		return KindManifest, true
	case reCompose.MatchString(base):
		return KindCompose, true
	case reDockerfile.MatchString(base):
		return KindDockerfile, true
	}
	return "", false
}
