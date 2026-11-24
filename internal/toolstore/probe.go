package toolstore

// Lack is one tool of the plan the store does not have: the doctor finding says "this runner lacks rust
// 1.85, which the workflow uses" before a job fails (R5.119).
type Lack struct {
	Tool, Version string
	// Hint says what the owner does about it; set for Xcode, which the owner installs.
	Hint string
}

// ProbeItem is one tool of the plan and whether the store has it.
type ProbeItem struct {
	Tool, Version string
	Present       bool
	// Hint is set when the tool is missing and only the owner can add it (Xcode): which one to install.
	Hint string
}

// Probe compares a plan with the store and reads nothing else. It never installs.
func (s *Store) Probe(p Plan) []ProbeItem {
	out := make([]ProbeItem, 0, len(p))
	for _, t := range p {
		it := ProbeItem{Tool: t.Tool, Version: t.Version, Present: s.Has(t)}
		if !it.Present && t.Tool == ToolXcode {
			it.Hint = XcodeInstallHint(t.Version)
		}
		out = append(out, it)
	}
	return out
}

// Lacks returns the tools of the probe that are not present.
func Lacks(items []ProbeItem) []Lack {
	var out []Lack
	for _, it := range items {
		if !it.Present {
			out = append(out, Lack{it.Tool, it.Version, it.Hint})
		}
	}
	return out
}
