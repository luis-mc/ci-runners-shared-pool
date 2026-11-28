package runners

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// CRSP387, runners R5.157: the Start button asks the machine to start the
// service of the runner and records it.
func TestStartRunnerStartsTheService(t *testing.T) {
	r, rec := lostRig(t)
	if _, err := r.svc.StartRunner(context.Background(), rec.ID, "owner"); err != nil {
		t.Fatal(err)
	}
	if len(r.ag.started) != 1 || r.ag.started[0] != rec.ServiceName {
		t.Fatalf("started %v, want %s", r.ag.started, rec.ServiceName)
	}
	if !strings.Contains(strings.Join(r.au.entries, "\n"), "runner.started") {
		t.Errorf("not recorded: %v", r.au.entries)
	}
}

// A service that starts and stops at once is not a start: the owner gets the
// reason the agent gave (R5.156).
func TestStartRunnerAnswersWithTheReason(t *testing.T) {
	r, rec := lostRig(t)
	r.ag.startErr = errors.New("the service started and stopped again at once. The runner service says: Permission to read the directory contents is required")
	_, err := r.svc.StartRunner(context.Background(), rec.ID, "owner")
	var re *RefusalError
	if !errors.As(err, &re) || !strings.Contains(re.Reason, "Permission to read the directory contents") {
		t.Fatalf("err = %v", err)
	}
}

func TestStartRunnerRefusesWhatItCannotStart(t *testing.T) {
	r, rec := lostRig(t)
	rec.Managed = false
	_ = r.store.DeleteRunner(context.Background(), rec.ID)
	if err := r.store.InsertRunner(context.Background(), rec); err != nil {
		t.Fatal(err)
	}
	var re *RefusalError
	if _, err := r.svc.StartRunner(context.Background(), rec.ID, "owner"); !errors.As(err, &re) {
		t.Fatalf("a runner CRSP did not make is refused: %v", err)
	}
}

func TestCanStartOnlyForAStoppedPersistentRunner(t *testing.T) {
	p := Record{Managed: true, Kind: "persistent"}
	if !CanStart(p, Stopped) || CanStart(p, Idle) || CanStart(p, Failed) {
		t.Error("Start is offered for stopped only")
	}
	if CanStart(Record{Managed: true, Kind: "ephemeral"}, Stopped) || CanStart(Record{Kind: "persistent"}, Stopped) {
		t.Error("not for an ephemeral runner or one CRSP did not make")
	}
}
