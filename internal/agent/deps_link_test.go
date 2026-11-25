package agent

import (
	"context"
	"testing"
)

func TestDepsManagerOneJobAndStop(t *testing.T) {
	var m depsManager
	ctx1, c1 := context.WithCancel(context.Background())
	if !m.begin("j1", c1) {
		t.Fatal("first job refused")
	}
	_, c2 := context.WithCancel(context.Background())
	if m.begin("j2", c2) {
		t.Fatal("second job started while the first runs")
	}
	if m.stop("other") {
		t.Fatal("stopped a job that is not running")
	}
	if !m.stop("j1") || ctx1.Err() == nil {
		t.Fatal("stop did not cancel the job")
	}
	m.end()
	_, c3 := context.WithCancel(context.Background())
	if !m.begin("j3", c3) {
		t.Fatal("slot not freed")
	}
}
