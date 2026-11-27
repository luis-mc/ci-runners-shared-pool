package agent

import (
	"strings"
)

// Reasons for the kvm backend on WSL2 (machines R5.53).
const (
	ReasonNestedOff     = "nested virtualization is off in .wslconfig"
	ReasonNestedUnknown = "nested virtualization is not set in .wslconfig (WSL2 defaults to on, but the file was not found)"
)

// Nested is the state of nestedVirtualization in the user's .wslconfig.
type Nested int

// Nested values.
const (
	NestedUnset Nested = iota // no file, or the key is not there
	NestedOn
	NestedOff
)

// ParseWslconfig reads `nestedVirtualization` under [wsl2]. Keys and section
// names are not case sensitive; # and ; start a comment. The agent never writes
// this file without a yes (machines R5.53).
func ParseWslconfig(text string) Nested {
	section := ""
	state := NestedUnset
	for _, raw := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.ToLower(strings.TrimSpace(line[1 : len(line)-1]))
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok || section != "wsl2" || !strings.EqualFold(strings.TrimSpace(k), "nestedVirtualization") {
			continue
		}
		v = strings.TrimSpace(v)
		if i := strings.IndexAny(v, "#;"); i >= 0 {
			v = strings.TrimSpace(v[:i])
		}
		if strings.EqualFold(v, "true") {
			state = NestedOn
		} else {
			state = NestedOff
		}
	}
	return state
}

// WSLKVMReason is the backend reason for a missing /dev/kvm on WSL2. The empty
// string means nested virtualization is on or unset (the loader is the fix).
func WSLKVMReason(n Nested) string {
	if n == NestedOff {
		return ReasonNestedOff
	}
	return ""
}

// KVMLoadPlan says what the agent does about a missing /dev/kvm on WSL2: it
// loads the module only when nesting is not off, and only once /dev/kvm is
// there does the kvm label get added (machines R5.53, runners R5.36).
type KVMLoadPlan struct {
	Load   bool   // run the one-shot privileged loader
	Reason string // why not, or what remains
}

// PlanKVMLoad decides the loader step at every WSL start.
func PlanKVMLoad(devicePresent bool, n Nested) KVMLoadPlan {
	switch {
	case devicePresent:
		return KVMLoadPlan{}
	case n == NestedOff:
		return KVMLoadPlan{Reason: ReasonNestedOff}
	}
	return KVMLoadPlan{Load: true}
}
