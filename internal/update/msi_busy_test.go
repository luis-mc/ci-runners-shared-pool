package update

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// busyRig is a Windows harness with a fake clock: Sleep moves the clock and sleeps for real never.
type busyRig struct {
	*harness
	clock  time.Time
	sleeps []time.Duration
	stages []Stage // the stage at each sleep
	starts int
	log    string
	busy   []string // answers of InstallerBusy in order ("" = free, else the name); then free
	probes int
	logs   []string // what each msiexec start leaves in the log
	alive  bool
}

func newBusyRig(t *testing.T) *busyRig {
	t.Helper()
	r := &busyRig{harness: newHarness(t, "windows", false, true), clock: time.Date(2026, 11, 21, 13, 0, 0, 0, time.UTC)}
	r.log = filepath.Join(r.dir, "logs", "msi-update.log")
	o := &r.m.o
	o.MSILog = r.log
	o.Now = func() time.Time { return r.clock }
	o.Sleep = func(d time.Duration) {
		r.sleeps = append(r.sleeps, d)
		s, _ := ReadStage(o.Dir)
		r.stages = append(r.stages, s)
		r.clock = r.clock.Add(d)
	}
	o.InstallerBusy = func() (bool, string) {
		i := r.probes
		r.probes++
		if i < len(r.busy) && r.busy[i] != "" {
			return true, r.busy[i]
		}
		return false, ""
	}
	o.ProcessAlive = func(int) bool { return r.alive }
	o.StartMSI = func(_ context.Context, _, log string) (int, error) {
		i := r.starts
		r.starts++
		if i < len(r.logs) && r.logs[i] != "" {
			_ = os.MkdirAll(filepath.Dir(log), 0o750)
			_ = os.WriteFile(log, []byte(r.logs[i]), 0o600)
		}
		return 77, nil
	}
	return r
}

// run drains the update and runs the supervisor to its end, synchronously.
func (r *busyRig) run(t *testing.T) {
	t.Helper()
	if _, err := r.m.Apply(context.Background(), KindUpdate, true, "octo"); err != nil {
		t.Fatal(err)
	}
	r.m.mu.Lock()
	p := r.m.pending
	r.m.pending = nil
	r.m.mu.Unlock()
	pid := 0
	if busy, _ := r.m.o.InstallerBusy(); !busy {
		var err error
		if pid, err = r.m.launchMSI(context.Background(), p); err != nil {
			t.Fatal(err)
		}
	}
	r.m.superviseMSI(p, pid)
}

const (
	logOK   = "MainEngineThread is returning 0\n"
	logBusy = "MainEngineThread is returning 1618\n"
	log1603 = "MainEngineThread is returning 1603\n"
)

func TestBusyInstallerWaitsEvery15SecondsThenInstalls(t *testing.T) {
	r := newBusyRig(t)
	r.busy = []string{"ComiclexHome-0.1.11712-x64", "ComiclexHome-0.1.11712-x64", "ComiclexHome-0.1.11712-x64"}
	r.logs = []string{logOK}
	r.run(t)
	if r.starts != 1 {
		t.Fatalf("msiexec started %d times", r.starts)
	}
	waits := 0
	for i, d := range r.sleeps {
		if d == MSIBusyRetry {
			waits++
			if s := r.stages[i]; s.Name != StageWaitingInstaller || s.WaitingFor != "ComiclexHome-0.1.11712-x64" || s.Version != "1.5.0" {
				t.Fatalf("while busy the stage is %+v", s)
			}
		}
	}
	// the first look (before the supervisor) was busy too: 3 answers, 2 left for the loop plus the one that frees it
	if waits != 2 {
		t.Fatalf("waited %d times: %v", waits, r.sleeps)
	}
	// restarting shows only after msiexec finished, and the old service that still runs is a failure
	last := r.stages[len(r.stages)-1]
	if last.Name != StageRestarting {
		t.Fatalf("after the install: %+v", last)
	}
	if s, _ := ReadStage(r.m.o.Dir); s.Name != StageFailed || !strings.Contains(s.Error, "still version 1.4.0") || s.LogPath != r.log {
		t.Fatalf("%+v", s)
	}
}

func TestBusyWaitHasNoLimit(t *testing.T) {
	r := newBusyRig(t)
	r.busy = make([]string, 500)
	for i := range r.busy {
		r.busy[i] = "Another"
	}
	r.logs = []string{logOK}
	r.run(t)
	if r.starts != 1 || r.clock.Sub(time.Date(2026, 11, 21, 13, 0, 0, 0, time.UTC)) < 499*MSIBusyRetry {
		t.Fatalf("starts %d, clock %v", r.starts, r.clock)
	}
	for _, s := range r.stages {
		if s.Name == StageFailed {
			t.Fatal("waiting must never fail")
		}
	}
}

func TestExit1618GoesBackToWaiting(t *testing.T) {
	r := newBusyRig(t)
	r.busy = []string{"", "Comiclex"} // free at the start, busy after msiexec said 1618
	r.logs = []string{logBusy, logOK}
	r.run(t)
	if r.starts != 2 {
		t.Fatalf("started %d times", r.starts)
	}
	seen := false
	for _, s := range r.stages {
		seen = seen || (s.Name == StageWaitingInstaller && s.WaitingFor == "Comiclex")
	}
	if !seen {
		t.Fatalf("no waiting stage: %+v", r.stages)
	}
	if s, _ := ReadStage(r.m.o.Dir); !strings.Contains(s.Error, "still version") {
		t.Fatalf("1618 is no failure by itself: %+v", s)
	}
}

func TestLogSayingAnotherInstallationEndedMsiexecIsBusy(t *testing.T) {
	r := newBusyRig(t)
	r.logs = []string{"Error 1618. Another installation is in progress. You must complete that installation before continuing.\n", logOK}
	r.alive = false // msiexec is gone and the log has no end line
	r.run(t)
	if r.starts != 2 {
		t.Fatalf("started %d times", r.starts)
	}
}

func TestRealMSIFailureEndsFailedWithTheLog(t *testing.T) {
	r := newBusyRig(t)
	r.logs = []string{log1603}
	r.run(t)
	if r.starts != 1 {
		t.Fatalf("a real failure is not retried: %d", r.starts)
	}
	s, _ := ReadStage(r.m.o.Dir)
	if s.Name != StageFailed || !strings.Contains(s.Error, "1603") || s.LogPath != r.log {
		t.Fatalf("%+v", s)
	}
	if _, err := os.Stat(filepath.Join(r.m.o.Dir, PendingMSIFile)); err == nil {
		t.Fatal("the pending record must go")
	}
	// the console can start it again
	if st := r.m.Status(context.Background()); st.State != StateFailed {
		t.Fatalf("%s", st.State)
	}
	if _, err := r.m.Apply(context.Background(), KindUpdate, true, "octo"); err != nil {
		t.Fatalf("Update to 1.5.0 again: %v", err)
	}
}

func TestMsiexecGoneWithoutAResultIsAFailure(t *testing.T) {
	r := newBusyRig(t)
	r.run(t) // msiexec leaves no log and is not alive
	if s, _ := ReadStage(r.m.o.Dir); s.Name != StageFailed || !strings.Contains(s.Error, "ended without a result") {
		t.Fatalf("%+v", s)
	}
}

func TestStaleRestartingAttemptIsFailedAtStart(t *testing.T) {
	r := newBusyRig(t)
	dir := r.m.o.Dir
	// what the owner's PC had: restarting 1.5.0, no msiexec, the service still on 1.4.0
	if err := WriteStage(dir, Stage{Name: StageRestarting, Percent: -1, Version: "1.5.0", StartedBy: StartedByAuto, Attempt: "adn0wu", AttemptKind: "update", At: r.clock}); err != nil {
		t.Fatal(err)
	}
	m := New(r.m.o)
	st := m.Status(context.Background())
	if st.Stage == nil || st.Stage.Name != StageFailed || st.Stage.FailedAt != StageRestarting || st.Stage.LogPath == "" || st.Stage.Attempt != "adn0wu" {
		t.Fatalf("%+v", st.Stage)
	}
	if st.State != StateFailed || !strings.Contains(st.Error, "1.5.0") {
		t.Fatalf("%s %q", st.State, st.Error)
	}
	if _, err := m.Apply(context.Background(), KindUpdate, true, "octo"); err != nil {
		t.Fatalf("Update to 1.5.0 again: %v", err)
	}
}

func TestStageOfTheRunningVersionIsLeftAlone(t *testing.T) {
	r := newBusyRig(t)
	dir := r.m.o.Dir
	_ = WriteStage(dir, Stage{Name: StageRestarting, Percent: -1, Version: "1.4.0", At: r.clock})
	New(r.m.o)
	if s, _ := ReadStage(dir); s.Name != StageRestarting {
		t.Fatalf("%+v", s)
	}
	_ = WriteStage(dir, Stage{Name: StageWaitingForJobs, Percent: -1, Version: "1.5.0", At: r.clock})
	New(r.m.o)
	if s, _ := ReadStage(dir); s.Name != StageWaitingForJobs {
		t.Fatalf("%+v", s)
	}
}

func TestRecoverMSIWithBusyLogDoesNotCountAsAFailure(t *testing.T) {
	now := time.Now()
	dir := t.TempDir()
	log := filepath.Join(dir, "msi-update.log")
	pendingFor(t, dir, log, now)
	_ = os.WriteFile(log, []byte(logBusy), 0o600)
	if !RecoverMSI(dir, "1.4.0", now) {
		t.Fatal("a restart after a busy installer settles the attempt")
	}
	r, _ := ReadResult(dir)
	if r.OK || !strings.Contains(r.Error, "another installer was running") {
		t.Fatalf("%+v", r)
	}
	if f, _ := ReadFailures(dir); f.Count != 0 {
		t.Fatalf("a busy installer is not a failed try: %+v", f)
	}
}

func TestBusyDetectionHelpers(t *testing.T) {
	if !MSIBusyExit(1618) || MSIBusyExit(1603) || MSIBusyExit(0) {
		t.Fatal("exit code")
	}
	if !MSILogBusy([]byte("MainEngineThread is returning 1618")) || !MSILogBusy([]byte("Another Installation is in progress.")) || MSILogBusy([]byte("returning 0")) {
		t.Fatal("log")
	}
	got := InstallerName([]string{"", `C:\Windows\system32\msiexec.exe /V`, `msiexec /i "C:\Users\a b\Downloads\ComiclexHome-0.1.11712-x64.msi" /qb`})
	if got != "ComiclexHome-0.1.11712-x64" {
		t.Fatalf("%q", got)
	}
	if InstallerName([]string{"msiexec.exe /V"}) != "" {
		t.Fatal("no file, no name")
	}
}

// A restart that finds an install record whose log never appeared, or went quiet, settles it.
func TestRecoverMSISettlesAnInstallThatNeverRanOrWentQuiet(t *testing.T) {
	now := time.Date(2026, 11, 21, 13, 7, 0, 0, time.UTC)
	dir := t.TempDir()
	log := filepath.Join(dir, "msi-update.log")

	pendingFor(t, dir, log, now) // no log yet: wait for it
	if RecoverMSI(dir, "1.4.0", now.Add(msiExitGrace-time.Second)) {
		t.Fatal("a start in progress is left alone")
	}
	if !RecoverMSI(dir, "1.4.0", now.Add(msiExitGrace+time.Second)) {
		t.Fatal("an installer that never wrote its log is a dead attempt")
	}
	if s, _ := ReadStage(dir); s.Name != StageFailed || s.LogPath != log {
		t.Fatalf("%+v", s)
	}

	pendingFor(t, dir, log, now) // a log without an end line that stopped growing
	_ = os.WriteFile(log, []byte("Action start 13:07:01: INSTALL.\n"), 0o600)
	_ = os.Chtimes(log, now, now)
	if RecoverMSI(dir, "1.4.0", now.Add(time.Minute)) {
		t.Fatal("a log that is still fresh is left alone")
	}
	if !RecoverMSI(dir, "1.4.0", now.Add(msiRestartGrace+time.Minute)) {
		t.Fatal("a quiet log is a dead attempt")
	}
}

// The console never reattaches to a dead attempt: after the failure a new yes is a new attempt.
func TestAfterAFailureANewAttemptStarts(t *testing.T) {
	r := newBusyRig(t)
	r.logs = []string{log1603}
	r.run(t)
	first := r.m.Status(context.Background()).Attempt
	if first == nil {
		t.Fatal("no attempt")
	}
	r.clock = r.clock.Add(time.Second)
	if _, err := r.m.Apply(context.Background(), KindUpdate, true, "octo"); err != nil {
		t.Fatal(err)
	}
	second := r.m.Status(context.Background())
	if second.Attempt == nil || second.Attempt.ID == first.ID || second.Error != "" {
		t.Fatalf("old %+v, new %+v, error %q", first, second.Attempt, second.Error)
	}
	if second.Stage == nil || second.Stage.Attempt != second.Attempt.ID || second.Stage.Name == StageFailed {
		t.Fatalf("the stage must belong to the new attempt: %+v", second.Stage)
	}
}
