package tray

import (
	"fmt"
	"sync"
)

// Words of the notification a runner service that did not start raises
// (runners R5.89). The words are in the design system's content.md, "Runner
// service words".
const (
	ProblemTitle = "A runner did not start"
	// ProblemAction is the last sentence of the body: the console has the Start button.
	ProblemAction = "Open the console and press Start."
)

// ProblemBody is the notification text: the runner, why it did not start, and what to do.
func ProblemBody(p RunnerProblem) string {
	return fmt.Sprintf("%s did not start. %s %s", p.Runner, p.Reason, ProblemAction)
}

// Alerted remembers which runner problems the tray already told the person
// about, so one problem is one notification, not one at every poll. A problem
// that clears (the service runs) is forgotten, so it tells again if it comes back.
type Alerted struct {
	mu   sync.Mutex
	seen map[string]bool
}

// Due returns the problems of the status that were not told yet, and records
// them. Problems missing from the status are forgotten.
func (a *Alerted) Due(st Status) []RunnerProblem {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := map[string]bool{}
	var out []RunnerProblem
	for _, p := range st.RunnerProblems {
		key := p.Service
		if key == "" {
			key = p.Runner
		}
		now[key] = true
		if !a.seen[key] {
			out = append(out, p)
		}
	}
	a.seen = now
	return out
}
