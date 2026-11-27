package profiles

import (
	"path"
	"sort"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry"
)

// Stack names DetectStacks returns.
const (
	StackNode   = "node"
	StackRust   = "rust"
	StackGo     = "go"
	StackPython = "python"
)

// stackByFile maps the base name of a stack file to its stack.
var stackByFile = map[string]string{
	"package.json": StackNode, "pnpm-lock.yaml": StackNode, "package-lock.json": StackNode, "yarn.lock": StackNode,
	".nvmrc": StackNode, ".node-version": StackNode,
	"Cargo.toml": StackRust, "Cargo.lock": StackRust, "rust-toolchain.toml": StackRust, "rust-toolchain": StackRust,
	"go.mod": StackGo, "go.sum": StackGo, "go.work": StackGo,
	"pyproject.toml": StackPython, "requirements.txt": StackPython, "Pipfile": StackPython,
	"poetry.lock": StackPython, "uv.lock": StackPython, ".python-version": StackPython,
}

// DetectStacks names the stacks the stack files show, sorted. Files the
// rules do not know (Dockerfile, compose files) show no stack.
func DetectStacks(files []registry.StackFile) []string {
	seen := map[string]bool{}
	for _, f := range files {
		if s, ok := stackByFile[path.Base(f.Path)]; ok {
			seen[s] = true
		}
	}
	out := make([]string, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// ProfileForStack picks the profile whose Stacks list covers the stacks. With
// several stacks the first stack in the order of profiles wins, custom
// profiles (the repository's own) before standard ones, so the answer never depends on map order. No
// match gives the base profile (ok false when it is missing too).
func ProfileForStack(all []Profile, stacks []string) (Profile, bool) {
	want := map[string]bool{}
	for _, s := range stacks {
		want[s] = true
	}
	ordered := append([]Profile(nil), all...)
	sort.SliceStable(ordered, func(i, j int) bool {
		return ordered[i].Kind == registry.KindCustom && ordered[j].Kind != registry.KindCustom
	})
	for _, p := range ordered {
		for _, s := range p.Stacks {
			if want[s] {
				return p, true
			}
		}
	}
	for _, p := range all {
		if p.ID == BaseProfileID {
			return p, true
		}
	}
	return Profile{}, false
}
