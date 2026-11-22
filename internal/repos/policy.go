package repos

import (
	"errors"
	"sort"
	"strings"

	"github.com/luis-mc/ci-runners-shared-pool/internal/auth"
	"github.com/luis-mc/ci-runners-shared-pool/internal/runners"
)

// PolicyUpdate is a partial change; nil fields keep their value (R5.25).
type PolicyUpdate struct {
	Priority        *int
	MaxParallelJobs *int
	AllowForkPRs    *bool
	// ConfirmForkRisk is the yes to the fork warning (R5.26).
	ConfirmForkRisk bool
	Labels          *[]string
	RunnerGroups    *[]string
	BudgetMemoryPct *int
	BudgetCPUPct    *int
	// ExtraPackages is the whole list as text, one name per line (runners
	// R5.51). A bad name refuses the update with its line number.
	ExtraPackages *string
	// HostedFallback turns the hosted fallback on or off (github-integration
	// R5.42). Turning it on needs ConfirmFallbackCost.
	HostedFallback *bool
	// ConfirmFallbackCost is the yes to the question that says GitHub may bill
	// the fallback minutes of a private repository.
	ConfirmFallbackCost bool
	// FallbackCapMinutes is the monthly cap; 0 clears it.
	FallbackCapMinutes *int
	// HostedLabels sets the hosted label of the OSes named; an empty label puts
	// the default back.
	HostedLabels *map[string]string
	// RunnerKinds sets which runners take the repository's jobs:
	// RunnerKindsEphemeralOnly, RunnerKindsAnyRunner, or RunnerKindsDefault to
	// clear the owner's choice (github-integration R5.60).
	RunnerKinds *string
}

// RunnerKindsDefault in a PolicyUpdate clears the owner's choice.
const RunnerKindsDefault = "default"

// ErrForkNeedsConfirm: turning fork pull requests on asks first.
var ErrForkNeedsConfirm = errors.New("fork pull request code would run on your machines; confirm to turn it on")

// ErrFallbackNeedsConfirm: turning the hosted fallback on asks first, because
// GitHub may bill the minutes.
var ErrFallbackNeedsConfirm = errors.New("GitHub may bill the minutes that run on GitHub-hosted runners for a private repository; confirm to turn the fallback on")

// Apply returns p changed by u, validated (R5.25 to R5.27).
func (u PolicyUpdate) Apply(p Policy) (Policy, error) {
	if u.Priority != nil {
		if *u.Priority < -100 || *u.Priority > 100 {
			return p, auth.Invalid("priority must be between -100 and 100")
		}
		p.Priority = *u.Priority
	}
	if u.MaxParallelJobs != nil {
		if *u.MaxParallelJobs < 1 || *u.MaxParallelJobs > 256 {
			return p, auth.Invalid("maximum parallel jobs must be between 1 and 256")
		}
		p.MaxParallelJobs = *u.MaxParallelJobs
	}
	if u.AllowForkPRs != nil {
		if *u.AllowForkPRs && !p.AllowForkPRs && !u.ConfirmForkRisk {
			return p, ErrForkNeedsConfirm
		}
		p.AllowForkPRs = *u.AllowForkPRs
	}
	if u.Labels != nil {
		if err := CheckLabels(*u.Labels); err != nil {
			return p, err
		}
		p.Labels = normalizeList(*u.Labels)
	}
	if u.RunnerGroups != nil {
		for _, g := range *u.RunnerGroups {
			if strings.TrimSpace(g) == "" {
				return p, auth.Invalid("a runner group name must not be empty")
			}
		}
		p.RunnerGroups = normalizeList(*u.RunnerGroups)
	}
	if u.ExtraPackages != nil {
		names, err := runners.ParseExtraPackages(*u.ExtraPackages)
		if err != nil {
			return p, auth.Invalid("%s", err.Error())
		}
		p.ExtraPackages = names
	}
	if u.HostedFallback != nil {
		if *u.HostedFallback && !p.HostedFallback && !u.ConfirmFallbackCost {
			return p, ErrFallbackNeedsConfirm
		}
		p.HostedFallback = *u.HostedFallback
	}
	if u.FallbackCapMinutes != nil {
		if *u.FallbackCapMinutes < 0 || *u.FallbackCapMinutes > 10_000_000 {
			return p, auth.Invalid("the monthly cap must be 0 (no cap) or a number of minutes")
		}
		p.FallbackCapMinutes = *u.FallbackCapMinutes
	}
	if u.HostedLabels != nil {
		labels := map[string]string{}
		for k, v := range p.HostedLabels {
			labels[k] = v
		}
		for k, v := range *u.HostedLabels {
			k = strings.ToLower(k)
			if osName(k) == "" {
				return p, auth.Invalid("hosted label: %q is not linux, windows or macos", k)
			}
			v = strings.TrimSpace(v)
			if v == "" {
				delete(labels, k)
				continue
			}
			if strings.ContainsAny(v, " \t,\"'") || strings.EqualFold(v, "self-hosted") {
				return p, auth.Invalid("hosted label for %s must be one GitHub-hosted label such as ubuntu-latest", k)
			}
			labels[k] = v
		}
		if len(labels) == 0 {
			labels = nil
		}
		p.HostedLabels = labels
	}
	if u.RunnerKinds != nil {
		switch k := strings.ToLower(strings.TrimSpace(*u.RunnerKinds)); k {
		case RunnerKindsEphemeralOnly, RunnerKindsAnyRunner:
			p.RunnerKinds = k
		case RunnerKindsDefault, "":
			p.RunnerKinds = ""
		default:
			return p, auth.Invalid("runner kinds must be ephemeral_only, any_runner or default")
		}
	}
	for _, b := range []struct {
		name string
		in   *int
		set  *int
	}{{"memory budget", u.BudgetMemoryPct, &p.BudgetMemoryPct}, {"CPU budget", u.BudgetCPUPct, &p.BudgetCPUPct}} {
		if b.in == nil {
			continue
		}
		if *b.in < 0 || *b.in > 100 {
			return p, auth.Invalid("%s must be between 1 and 100 (0 clears it)", b.name)
		}
		*b.set = *b.in
	}
	return p, nil
}

func normalizeList(in []string) []string {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[strings.ToLower(s)] {
			continue
		}
		seen[strings.ToLower(s)] = true
		out = append(out, s)
	}
	return out
}

func sortStrings(s []string) { sort.Strings(s) }
