package update

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// D15: Cancel while the update waits for jobs ends it with nothing changed.
func TestCancelWhileWaitingForJobsChangesNothing(t *testing.T) {
	h := newHarness(t, "windows", false, true)
	ctx := context.Background()
	if _, err := h.m.Apply(ctx, KindUpdate, true, "tray:S-1-5-21"); err != nil {
		t.Fatal(err)
	}
	if sg, ok := ReadStage(h.m.o.Dir); !ok || sg.Name != StageWaitingForJobs || sg.StartedBy != StartedByTray {
		t.Fatalf("stage before cancel: %+v %v", sg, ok)
	}
	st, err := h.m.Cancel(ctx, "tray:S-1-5-21")
	if err != nil {
		t.Fatal(err)
	}
	if st.State != StateIdle || st.Stage != nil {
		t.Fatalf("after cancel: state %s stage %+v", st.State, st.Stage)
	}
	if h.d.cancelCount() != 1 {
		t.Fatalf("the drain must end: %d", h.d.cancelCount())
	}
	if _, err := os.Stat(filepath.Join(h.m.o.Dir, LockFile)); !os.IsNotExist(err) {
		t.Fatal("the lock must be released")
	}
	if err := h.m.OnIdle(ctx, KindUpdate); err != nil || len(h.spawns) != 0 {
		t.Fatalf("a late idle signal must install nothing: %v %v", err, h.spawns)
	}
	if _, err := h.m.Apply(ctx, KindUpdate, true, "octo"); err != nil {
		t.Fatalf("a new update can start: %v", err)
	}
}

// D15: from Installing on there is no cancel.
func TestCancelRefusedFromInstallingOn(t *testing.T) {
	h := newHarness(t, "windows", false, true)
	ctx := context.Background()
	h.m.o.StartMSI = func(context.Context, string, string) (int, error) { return 1, nil }
	h.m.o.MSILog = filepath.Join(h.dir, "msi.log")
	if _, err := h.m.Cancel(ctx, "x"); !errors.Is(err, ErrNotCancellable) {
		t.Fatalf("idle: %v", err)
	}
	if _, err := h.m.Apply(ctx, KindUpdate, true, "octo"); err != nil {
		t.Fatal(err)
	}
	if err := h.m.OnIdle(ctx, KindUpdate); err != nil {
		t.Fatal(err)
	}
	if _, err := h.m.Cancel(ctx, "x"); !errors.Is(err, ErrNotCancellable) {
		t.Fatalf("installing: %v", err)
	}
}

// D15: Cancel while downloading stops the download and leaves no stage.
func TestCancelWhileDownloading(t *testing.T) {
	h := newHarness(t, "linux", false, true)
	ctx := context.Background()
	started, release := make(chan struct{}), make(chan struct{})
	h.m.o.Fetch = func(c context.Context, _, _ string) error {
		close(started)
		select {
		case <-c.Done():
			return c.Err()
		case <-release:
			return nil
		}
	}
	done := make(chan error, 1)
	go func() { _, err := h.m.Apply(ctx, KindUpdate, true, "octo"); done <- err }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("download did not start")
	}
	if _, err := h.m.Cancel(ctx, "octo"); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, ErrCancelled) {
		t.Fatalf("apply: %v", err)
	}
	if _, ok := ReadStage(h.m.o.Dir); ok {
		t.Fatal("no stage after a cancel")
	}
	if st := h.m.Status(ctx); st.State != StateIdle {
		t.Fatalf("state %s", st.State)
	}
}

func TestStartedByFollowsTheActorAndSurvivesTheRestart(t *testing.T) {
	for actor, want := range map[string]string{"auto-update": StartedByAuto, FollowActor: StartedByAuto, "tray:S-1": StartedByTray, "octo": StartedByConsole} {
		if got := StartedByActor(actor); got != want {
			t.Errorf("%s: %s", actor, got)
		}
	}
	dir := t.TempDir()
	if err := WriteStage(dir, Stage{Name: StageRestarting, Version: "1.5.0", StartedBy: StartedByTray}); err != nil {
		t.Fatal(err)
	}
	// A new process (the helper) writes done: the origin carries over.
	env := Env{}
	env.fill()
	p := Plan{Kind: string(KindUpdate), To: "1.5.0", Dir: dir, GOOS: "windows", InstallKind: KindMSI}
	_ = RunPlan(context.Background(), p, env)
	sg, ok := ReadStage(dir)
	if !ok || sg.StartedBy != StartedByTray {
		t.Fatalf("stage %+v", sg)
	}
}
