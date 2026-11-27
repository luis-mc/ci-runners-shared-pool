package main

import (
	"context"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/github"
)

// R5.6: while the App or its key is gone, the poller has no targets and the
// loops are not ready; a running job is not in either list.
func TestGitHubWorkStopsWhileTheAppIsGone(t *testing.T) {
	gh := github.New(github.Config{})
	gh.UseApp(7, "Iv23liFAKECLIENTID00")
	gone := false
	tasks := githubLoopTasks(gh, nil, nil, func() bool { return gone })
	if !tasks.Ready() {
		t.Fatal("a live App: the loops run")
	}
	gone = true
	if tasks.Ready() {
		t.Fatal("a gone App: the loops stop")
	}
	d := &demandRunner{gh: gh, env: demandEnv{appGone: func() bool { return gone }}}
	if ts, err := d.targets(context.Background()); err != nil || len(ts) != 0 {
		t.Fatalf("a gone App: nothing is polled: %v %v", ts, err)
	}
}
