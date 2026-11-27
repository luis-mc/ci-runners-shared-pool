package reconcile

import (
	"context"
	"testing"
)

func TestCrspOtherHosted(t *testing.T) { // R5.39
	c := NewClassifier(fakeRunners{"crsp-1": true})
	for _, tc := range []struct {
		name, runner, group string
		want                Origin
	}{
		{"crsp runner", "crsp-1", "Default", Crsp},
		{"crsp runner with no group", "crsp-1", "", Crsp},
		{"hosted group", "GitHub Actions 12", HostedGroup, Hosted},
		{"hosted group without a name", "", HostedGroup, Hosted},
		{"old pool", "old-pool-3", "Default", Other},
		{"old pool without a group", "old-pool-3", "", Other},
		{"not taken yet", "", "", Unknown},
		{"group but no runner", "", "Default", Unknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := c.Origin(context.Background(), tc.runner, tc.group)
			if err != nil || got != tc.want {
				t.Fatalf("Origin(%q, %q) = %q, %v; want %q", tc.runner, tc.group, got, err, tc.want)
			}
		})
	}
}

func TestStateAndResultMapping(t *testing.T) {
	if StateOf("in_progress") != "running" || StateOf("waiting") != "queued" || StateOf("completed") != "done" || StateOf("x") != "" {
		t.Fatal("state mapping")
	}
	if ResultOf("startup_failure") != "failure" || ResultOf("skipped") != "" || ResultOf("timed_out") != "timed_out" {
		t.Fatal("result mapping")
	}
}
