package main

import (
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/machines"
)

func view(os string, online, accepting bool, state string) machines.View {
	return machines.View{Machine: machines.Machine{ID: os, OS: os, Online: online, Accepting: accepting, State: state}}
}

// A machine counts as up only when it is online and accepting jobs, so a
// paused machine (or one on battery) counts as down (github-integration R5.43).
func TestUpByOS(t *testing.T) {
	act := machines.StateActive
	got := upByOS([]machines.View{
		view("linux", true, false, act),  // paused: down
		view("windows", true, true, act), // up
		view("macos", false, true, act),  // offline: down
	})
	if got["Linux"] || !got["Windows"] || got["macOS"] {
		t.Fatalf("%v", got)
	}
	// One accepting machine is enough; an OS with no machine is absent.
	got = upByOS([]machines.View{view("linux", true, false, act), view("linux", true, true, act)})
	if !got["Linux"] {
		t.Fatalf("%v", got)
	}
	if _, ok := got["Windows"]; ok {
		t.Fatal("no machine, no key")
	}
}
