package update

import (
	"context"
	"testing"
	"time"
)

type fakeFleet struct {
	agents []Agent
	asked  map[string][]string
}

func (f *fakeFleet) Agents(context.Context) ([]Agent, error) { return f.agents, nil }
func (f *fakeFleet) AskUpdate(id, v string) error {
	if f.asked == nil {
		f.asked = map[string][]string{}
	}
	f.asked[id] = append(f.asked[id], v)
	return nil
}

func TestFollowerAsksOnlyOlderStableAgentsOneAtATime(t *testing.T) {
	now := time.Now()
	fl := &fakeFleet{agents: []Agent{
		{ID: "b-old", Version: "1.4.0"},
		{ID: "a-old", Version: "1.4.0"},
		{ID: "same", Version: "1.5.0"},
		{ID: "newer", Version: "1.6.0"},
		{ID: "dev", Version: "dev"},
	}}
	f := &Follower{Fleet: fl, Current: "1.5.0", Now: func() time.Time { return now }}
	got := f.Tick(context.Background())
	if len(got) != 1 || got[0] != "a-old" || fl.asked["a-old"][0] != "1.5.0" {
		t.Fatalf("the first tick asks one agent: %v", got)
	}
	// The next tick asks the one asked least recently, so a postponing agent never starves the rest.
	if got = f.Tick(context.Background()); len(got) != 1 || got[0] != "b-old" {
		t.Fatalf("the second tick asks the other: %v", got)
	}
	for _, id := range []string{"same", "newer", "dev"} {
		if len(fl.asked[id]) != 0 {
			t.Errorf("%s must not be asked", id)
		}
	}
}

func TestFollowerWaitsWhileAnotherAgentUpdates(t *testing.T) {
	now := time.Now()
	fl := &fakeFleet{agents: []Agent{
		{ID: "busy", Version: "1.4.0", Updating: true, UpdatingSince: now.Add(-10 * time.Minute)},
		{ID: "next", Version: "1.4.0"},
	}}
	f := &Follower{Fleet: fl, Current: "1.5.0", Now: func() time.Time { return now }}
	if got := f.Tick(context.Background()); len(got) != 0 {
		t.Fatalf("asked %v while one agent updates", got)
	}
	// An update report older than the active window no longer counts: the agent is asked again.
	fl.agents[0].UpdatingSince = now.Add(-3 * time.Hour)
	if got := f.Tick(context.Background()); len(got) != 1 {
		t.Fatalf("asked %v after a stale report", got)
	}
}

func TestFollowerStaysQuietForADevController(t *testing.T) {
	fl := &fakeFleet{agents: []Agent{{ID: "a", Version: "1.4.0"}}}
	for _, cur := range []string{"dev", "edge-abc1234", ""} {
		if got := (&Follower{Fleet: fl, Current: cur}).Tick(context.Background()); len(got) != 0 {
			t.Fatalf("%q asked %v", cur, got)
		}
	}
}
