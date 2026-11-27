// Package runners holds the runner record rules: generic labels, pinned runner
// versions, account names, states, plans, drain and removal (runners R5.1 to
// R5.7, R5.13, R5.36, R5.37, R6.4).
package runners

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/luis-mc/ci-runners-shared-pool/internal/sizelabel"
)

// routingLabelRE is the shape of a per-job routing label, crsp-<slug>-<4 hex>
// (github-integration R5.101). A JIT runner gets its job's label when it is made
// and the label is read-only afterwards (runners R5.103).
var routingLabelRE = regexp.MustCompile(`^crsp-[a-z0-9]+(-[a-z0-9]+)*-[0-9a-f]{4}$`)

// IsRoutingLabel reports whether l has the shape of a per-job routing label.
func IsRoutingLabel(l string) bool { return routingLabelRE.MatchString(strings.ToLower(l)) }

// MaxLabels is the most labels one runner may carry, read-only ones included;
// GitHub answers the 101st add with HTTP 500.
const MaxLabels = 100

// EphemeralLabel is carried by every ephemeral runner (a fresh container made
// for one job) and by no persistent runner. A repository set to "ephemeral
// only" gets it in its CRSP_RUNS_ON variables, so GitHub itself never offers its
// jobs to a persistent runner (github-integration R5.60, runners R5.91).
const EphemeralLabel = "ephemeral"

// Fixed generic labels (runners R5.36).
var fixedLabels = map[string]bool{
	"self-hosted":  true,
	"linux":        true,
	"windows":      true,
	"macos":        true,
	"x64":          true,
	"arm64":        true,
	"docker":       true,
	"kvm":          true,
	"gpu":          true,
	EphemeralLabel: true,
}

// capabilityLabels are added only when the agent has checked they work.
var capabilityLabels = map[string]bool{"docker": true, "kvm": true, "gpu": true}

// LabelError names a refused label and why.
type LabelError struct {
	Label  string
	Reason string
}

func (e *LabelError) Error() string {
	return fmt.Sprintf("label %q refused: %s", e.Label, e.Reason)
}

// ValidateLabels refuses any label that is not generic (runners R5.36; port of
// dev-standards test_dl316_windows_only_generic.py). poolName is the one
// allowed pool label; verified lists the capability labels the agent checked.
// It returns the labels sorted without repeats.
func ValidateLabels(labels []string, poolName string, verified map[string]bool) ([]string, error) {
	seen := map[string]bool{}
	for _, raw := range labels {
		l := strings.TrimSpace(raw)
		switch {
		case l == "":
			return nil, &LabelError{Label: raw, Reason: "empty"}
		case l == poolName && poolName != "":
		case IsRoutingLabel(l):
		case sizelabel.IsSizeLabel(l):
			if _, err := sizelabel.Parse([]string{l}); err != nil {
				return nil, &LabelError{Label: l, Reason: err.Error()}
			}
		case !fixedLabels[l]:
			return nil, &LabelError{Label: l, Reason: "not a generic label (allowed: self-hosted, OS, architecture, docker, kvm, gpu and the pool name)"}
		case capabilityLabels[l] && !verified[l]:
			return nil, &LabelError{Label: l, Reason: "the agent has not checked that this works on the machine"}
		}
		seen[l] = true
	}
	if len(seen) > MaxLabels {
		return nil, &LabelError{Label: fmt.Sprintf("%d labels", len(seen)), Reason: fmt.Sprintf("a runner carries at most %d labels", MaxLabels)}
	}
	out := make([]string, 0, len(seen))
	for l := range seen {
		out = append(out, l)
	}
	sort.Strings(out)
	return out, nil
}

// DefaultLabels are the labels a persistent runner gets when none are asked.
func DefaultLabels(os, arch string) []string {
	return []string{"self-hosted", os, arch}
}
