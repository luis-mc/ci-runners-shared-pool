package runners

import "fmt"

// State is a runner's one state (runners R5.3).
type State string

// Runner states.
const (
	Creating State = "creating"
	Idle     State = "idle"
	Busy     State = "busy"
	Draining State = "draining"
	Updating State = "updating"
	Offline  State = "offline"
	Failed   State = "failed"
	Removing State = "removing"
)

// Shown-only states (UX-19). A record never holds them: View gives one when
// GitHub or the agent says the stored idle or busy is not true.
const (
	NotRegistered State = "not_registered"
	Stopped       State = "stopped"
	Unknown       State = "unknown"
)

// transitions is the state diagram of the spec, section 3. A persistent runner
// goes busy to idle; an ephemeral one goes busy to removing.
var transitions = map[State][]State{
	Creating: {Idle, Failed},
	Idle:     {Busy, Draining, Offline},
	Busy:     {Idle, Removing, Draining},
	Draining: {Updating, Removing},
	Updating: {Idle},
	Offline:  {Idle},
	Failed:   {Removing},
	Removing: {},
}

// CanTransition says whether from to to is in the diagram.
func CanTransition(from, to State) bool {
	for _, s := range transitions[from] {
		if s == to {
			return true
		}
	}
	return false
}

// ErrBadTransition names a refused change.
type ErrBadTransition struct{ From, To State }

func (e ErrBadTransition) Error() string {
	return fmt.Sprintf("runner cannot go from %s to %s", e.From, e.To)
}

// Next checks and returns to.
func Next(from, to State) (State, error) {
	if !CanTransition(from, to) {
		return from, ErrBadTransition{from, to}
	}
	return to, nil
}
