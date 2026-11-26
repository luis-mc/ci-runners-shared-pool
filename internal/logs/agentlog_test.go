package logs

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func agentLogger(f *Forwarder) *slog.Logger {
	return slog.New(NewAgentHandler(f.Add, nil)).With("component", "agent", "machine", "m1")
}

// TestSetupStepsCaptured: every setup step and the agent's own lines become
// stored lines with time, level, machine, component and the runner (R5.20).
func TestSetupStepsCaptured(t *testing.T) {
	b, err := openBuffer(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	f := &Forwarder{Buf: b}
	l := agentLogger(f)
	for _, step := range SetupSteps {
		SetupStep(l, "r1", step, nil)
	}
	SetupStep(l, "r1", "register", errors.New("token refused"))
	l.With("runner", "r1", "job", "42").Warn("disk low", "free_mib", 12)
	l.Debug("below the level, not kept")
	lines, _, _, err := b.Take(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != len(SetupSteps)+2 {
		t.Fatalf("%d lines, want %d", len(lines), len(SetupSteps)+2)
	}
	for i, step := range SetupSteps {
		got := lines[i]
		if got.Message != "runner setup: "+step || got.Component != SetupComponent || got.Runner != "r1" || got.Machine != "m1" || got.Level != slog.LevelInfo || got.At.IsZero() {
			t.Errorf("step %d: %+v", i, got)
		}
	}
	if e := lines[len(SetupSteps)]; e.Level != slog.LevelError || !strings.Contains(e.Attrs, "token refused") {
		t.Errorf("failed step: %+v", e)
	}
	w := lines[len(lines)-1]
	if w.Job != "42" || w.Runner != "r1" || w.Component != "agent" || !strings.Contains(w.Attrs, "free_mib") {
		t.Errorf("own line: %+v", w)
	}
}

// TestAgentLinesBatchOverTheLink: lines wait on disk while no link is attached,
// go in order when one is, and stay when a send fails (R5.20, R5.23).
func TestAgentLinesBatchOverTheLink(t *testing.T) {
	b, _ := openBuffer(t.TempDir(), 1<<20)
	f := &Forwarder{Buf: b}
	l := agentLogger(f)
	l.Info("one")
	l.Info("two")
	ctx := context.Background()
	if err := f.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	var sent []string
	fail := true
	detach := f.Attach(func(_ context.Context, lines []Line, _ int64) error {
		if fail {
			return errors.New("link down")
		}
		for _, x := range lines {
			sent = append(sent, x.Message)
		}
		return nil
	})
	if err := f.Flush(ctx); err == nil || len(sent) != 0 {
		t.Fatalf("a failed send must report and keep the lines: %v %v", err, sent)
	}
	l.Info("three")
	fail = false
	if err := f.Flush(ctx); err != nil || strings.Join(sent, ",") != "one,two,three" {
		t.Fatalf("sent %v err %v", sent, err)
	}
	detach()
	l.Info("four")
	if err := f.Flush(ctx); err != nil || len(sent) != 3 {
		t.Fatalf("detached link must send nothing: %v %v", sent, err)
	}
}
