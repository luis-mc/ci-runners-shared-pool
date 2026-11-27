package tray

import (
	"strings"
	"testing"
)

func problem(name, reason string) RunnerProblem {
	return RunnerProblem{Runner: name, Service: "actions.runner.o-r." + name, Account: `.\` + name, Reason: reason}
}

// runners R5.89: one notification for each runner service that did not start,
// not one at every poll; it tells again when the problem came back after it cleared.
func TestRunnerProblemNotifiesOnceUntilItClears(t *testing.T) {
	a := &Alerted{}
	one := Status{RunnerProblems: []RunnerProblem{problem("crsp-a", "Logon failure")}}
	if got := a.Due(one); len(got) != 1 || got[0].Runner != "crsp-a" {
		t.Fatalf("first poll: %+v", got)
	}
	if got := a.Due(one); len(got) != 0 {
		t.Fatalf("the same problem again: %+v", got)
	}
	two := Status{RunnerProblems: []RunnerProblem{problem("crsp-a", "Logon failure"), problem("crsp-b", "disabled")}}
	if got := a.Due(two); len(got) != 1 || got[0].Runner != "crsp-b" {
		t.Fatalf("a second runner: %+v", got)
	}
	if got := a.Due(Status{}); len(got) != 0 {
		t.Fatalf("cleared: %+v", got)
	}
	if got := a.Due(one); len(got) != 1 {
		t.Fatalf("back after clearing: %+v", got)
	}
}

func TestRunnerProblemBodyNamesRunnerReasonAndAction(t *testing.T) {
	b := ProblemBody(problem("crsp-gameroompc-1", "Logon failure: the user has not been granted the requested logon type at this computer."))
	for _, w := range []string{"crsp-gameroompc-1 did not start", "Logon failure", ProblemAction} {
		if !strings.Contains(b, w) {
			t.Errorf("body lacks %q: %s", w, b)
		}
	}
}

func TestStatusFromCarriesRunnerProblems(t *testing.T) {
	st := statusFrom(Response{RunnerProblems: []RunnerProblem{problem("crsp-a", "x")}})
	if len(st.RunnerProblems) != 1 {
		t.Fatalf("%+v", st)
	}
}
