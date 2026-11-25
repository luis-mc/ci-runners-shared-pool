package runners

import (
	"sort"
	"time"
)

// Start-time budgets (runners R6.5). The median is over at least
// StartSamples starts, each from the job being assigned to the runner being
// online in GitHub, with a warm image.
const (
	ContainerStartBudget = 15 * time.Second
	MicroVMStartBudget   = 5 * time.Second // M6, not built
	StartSamples         = 20
)

// StartMedian is the median of the start times; zero for none.
func StartMedian(starts []time.Duration) time.Duration {
	if len(starts) == 0 {
		return 0
	}
	s := append([]time.Duration(nil), starts...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	n := len(s)
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}

// WithinStartBudget is true when there are enough starts and their median is
// within the budget.
func WithinStartBudget(starts []time.Duration, budget time.Duration) bool {
	return len(starts) >= StartSamples && StartMedian(starts) <= budget
}
