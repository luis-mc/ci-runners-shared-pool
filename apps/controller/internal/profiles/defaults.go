package profiles

import "github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry"

// BaseProfileID is the profile of a repository whose stack maps to nothing.
const BaseProfileID = "base"

// DefaultBase is the base image of the standard profiles.
const DefaultBase = "ubuntu:24.04"

// Defaults returns the standard profiles crsp seeds (R5.77). The order is the
// order a polyglot stack is resolved in (see ProfileForStack). Base digests
// are empty: the mirror (REG9) or the first build resolves them.
func Defaults() []Profile {
	pins := func() map[string]string { return map[string]string{"runner": "2.330.0", "gh": "2.62.0"} }
	std := func(id, title string, stacks []string, tc ...registry.Toolchain) Profile {
		return Profile{
			ID: id, Kind: registry.KindStandard, Title: title, BaseName: DefaultBase, Pins: pins(),
			Toolchains: tc, Labels: []string{"crsp-" + id}, Stacks: stacks, BuiltIn: true,
		}
	}
	return []Profile{
		std("node-web", "Node and web", []string{StackNode},
			registry.Toolchain{Tool: "node", Version: "22.11.0"}, registry.Toolchain{Tool: "pnpm", Version: "10.34.5"}),
		std("rust-cross", "Rust and cross builds", []string{StackRust},
			registry.Toolchain{Tool: "rust", Version: "1.84.0"}),
		std("go", "Go", []string{StackGo},
			registry.Toolchain{Tool: "go", Version: "1.24.0"}),
		std("python", "Python", []string{StackPython},
			registry.Toolchain{Tool: "python", Version: "3.13.0"}),
		std(BaseProfileID, "Base", nil),
	}
}
