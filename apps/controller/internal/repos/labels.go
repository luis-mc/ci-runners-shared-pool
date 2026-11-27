package repos

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/auth"
)

var (
	osLabels   = map[string]string{"linux": "Linux", "windows": "Windows", "macos": "macOS"}
	archLabels = map[string]bool{"x64": true, "x86": true, "arm": true, "arm64": true}
	// capability labels and a pool name such as crsp-home.
	otherGeneric = map[string]bool{"self-hosted": true, "docker": true, "kvm": true, "gpu": true}
	poolLabelRE  = regexp.MustCompile(`^crsp-[a-z0-9]+(-[a-z0-9]+)*$`)
)

// IsGenericLabel reports whether a label is allowed on a runner or in a policy:
// self-hosted, an OS, an architecture, docker, kvm, gpu, or a pool name. Never
// a project name (R5.25, R5.27).
func IsGenericLabel(l string) bool {
	k := strings.ToLower(strings.TrimSpace(l))
	if k == "" {
		return false
	}
	if _, ok := osLabels[k]; ok {
		return true
	}
	return archLabels[k] || otherGeneric[k] || poolLabelRE.MatchString(k)
}

// CheckLabels refuses a label list with a non-generic label, naming each (R5.27).
func CheckLabels(labels []string) error {
	var bad []string
	for _, l := range labels {
		if !IsGenericLabel(l) {
			bad = append(bad, fmt.Sprintf("%q", l))
		}
	}
	if len(bad) > 0 {
		return auth.Invalid("labels must be generic (self-hosted, an OS, an architecture, docker, kvm, gpu, or a pool name like crsp-home); not allowed: %s", strings.Join(bad, ", "))
	}
	return nil
}

// NonGeneric lists the labels of a runner that are not generic, for the
// cleanup question of R5.27.
func NonGeneric(labels []string) []string {
	var out []string
	for _, l := range labels {
		if !IsGenericLabel(l) {
			out = append(out, l)
		}
	}
	return out
}

// WorkflowsNaming returns the workflow files whose runs-on names the label
// literally. A leftover label is removed from a runner only after a yes, and
// only when this list is empty (R5.27).
func WorkflowsNaming(label string, files map[string][]byte) []string {
	var out []string
	for path, content := range files {
		for _, j := range ParseRunsOn(content) {
			for _, l := range j.Literal {
				if strings.EqualFold(l, label) {
					out = append(out, path)
					break
				}
			}
		}
	}
	sortStrings(out)
	return out
}
