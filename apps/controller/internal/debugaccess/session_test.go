package debugaccess

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/store"
)

// R5.4
func TestUserAndJobSessionCaps(t *testing.T) {
	e := newEnv(t)
	e.turnOn(nil)
	e.mustOpen(admin, 1)
	e.mustOpen(admin, 2)
	if _, err := e.open(admin, 3); codeOf(err) != CodeUserCap {
		t.Fatalf("third session: %v", err)
	}
	_, err := e.open(owner, 1)
	if codeOf(err) != CodeJobCap || !strings.Contains(err.Error(), "admin1") {
		t.Fatalf("second on a job: %v", err)
	}
	// Two at once never both pass the caps.
	e2 := newEnv(t)
	e2.turnOn(nil)
	var wg sync.WaitGroup
	errs := make([]error, 4)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = e2.svc.Open(context.Background(), owner, OpenRequest{JobID: 5, Reason: "same job", Source: "web"})
		}(i)
	}
	wg.Wait()
	ok := 0
	for _, err := range errs {
		if err == nil {
			ok++
		}
	}
	if ok != 1 {
		t.Fatalf("%d opens passed on one job", ok)
	}
}

// R5.17, R5.18
func TestIdleLimit(t *testing.T) {
	e := newEnv(t)
	e.turnOn(func(r *RepoSettings) { r.IdleMin = 5; r.LimitMin = 30 })
	d := e.mustOpen(admin, 1)
	if d.EndsAt != e.now.Add(30*time.Minute) || d.IdleMin != 5 {
		t.Fatalf("session = %+v", d)
	}
	e.now = e.now.Add(4 * time.Minute)
	if err := e.svc.Touch(e.ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	e.now = e.now.Add(4 * time.Minute)
	if n, _ := e.svc.Sweep(e.ctx); n != 0 {
		t.Fatal("ended before the idle limit")
	}
	e.now = e.now.Add(time.Minute)
	if n, err := e.svc.Sweep(e.ctx); n != 1 || err != nil {
		t.Fatalf("sweep = %d, %v", n, err)
	}
	got, _ := e.svc.Session(e.ctx, d.ID)
	if got.EndReason != EndIdle || got.EndedBy != "system" {
		t.Fatalf("ended = %+v", got)
	}
	// The time limit.
	d2 := e.mustOpen(admin, 2)
	for i := 0; i < 6; i++ {
		e.now = e.now.Add(5 * time.Minute)
		_ = e.svc.Touch(e.ctx, d2.ID)
	}
	if _, err := e.svc.Sweep(e.ctx); err != nil {
		t.Fatal(err)
	}
	got, _ = e.svc.Session(e.ctx, d2.ID)
	if got.EndReason != EndTimeLimit {
		t.Fatalf("time limit: %+v", got)
	}
}

// R5.19
func TestExtendOnceNotPastLimit(t *testing.T) {
	e := newEnv(t)
	e.turnOn(nil)
	d := e.mustOpen(admin, 1)
	if _, err := e.svc.Extend(e.ctx, admin2, d.ID); codeOf(err) != CodeNotYours {
		t.Fatalf("other user extends: %v", err)
	}
	if _, err := e.svc.Extend(e.ctx, viewer, d.ID); codeOf(err) != CodeViewer {
		t.Fatalf("viewer extends: %v", err)
	}
	x, err := e.svc.Extend(e.ctx, admin, d.ID)
	if err != nil || x.EndsAt != d.EndsAt.Add(15*time.Minute) {
		t.Fatalf("extend = %+v, %v", x, err)
	}
	if _, err := e.svc.Extend(e.ctx, admin, d.ID); codeOf(err) != CodeExtended {
		t.Fatalf("second extend: %v", err)
	}
	if got := e.audits(ActionExtended); len(got) != 1 {
		t.Fatalf("extended rows = %d", len(got))
	}
	// A 120-minute session cannot pass 120 minutes.
	e.turnOn(func(r *RepoSettings) { r.LimitMin = 120 })
	d2 := e.mustOpen(admin, 2)
	if _, err := e.svc.Extend(e.ctx, admin, d2.ID); codeOf(err) != CodePastLimit {
		t.Fatalf("extend past 120: %v", err)
	}
}

// R5.20
func TestJobEndClosesAfterNotice(t *testing.T) {
	e := newEnv(t)
	e.turnOn(nil)
	d := e.mustOpen(admin, 1)
	if err := e.svc.JobEnded(e.ctx, 1); err != nil {
		t.Fatal(err)
	}
	got, _ := e.svc.Session(e.ctx, d.ID)
	if got.EndsAt != e.now.Add(60*time.Second) {
		t.Fatalf("ends at %v", got.EndsAt)
	}
	if _, err := e.svc.Extend(e.ctx, admin, d.ID); codeOf(err) != CodeEnded {
		t.Fatalf("extend after the job ended: %v", err)
	}
	e.now = e.now.Add(59 * time.Second)
	if n, _ := e.svc.Sweep(e.ctx); n != 0 {
		t.Fatal("ended before the notice")
	}
	e.now = e.now.Add(time.Second)
	if n, _ := e.svc.Sweep(e.ctx); n != 1 {
		t.Fatal("not ended after the notice")
	}
	got, _ = e.svc.Session(e.ctx, d.ID)
	if got.EndReason != EndJobEnded {
		t.Fatalf("reason = %q", got.EndReason)
	}
	if len(e.audits(ActionNotice)) != 1 {
		t.Fatal("no notice row")
	}
}

// R6.4: at start, sessions the agent does not list are ended.
func TestReconcileAtStart(t *testing.T) {
	e := newEnv(t)
	e.turnOn(nil)
	a := e.mustOpen(admin, 1)
	b := e.mustOpen(admin, 2)
	n, err := e.svc.Reconcile(e.ctx, func(d store.DebugSession) bool { return d.ID == b.ID })
	if err != nil || n != 1 {
		t.Fatalf("reconcile = %d, %v", n, err)
	}
	if got, _ := e.svc.Session(e.ctx, a.ID); got.EndReason != EndLinkLost {
		t.Fatalf("a = %+v", got)
	}
	if n, _ := e.svc.Reconcile(e.ctx, nil); n != 1 {
		t.Fatal("nil live must end every open session")
	}
}

// R5.26: the kill switch ends every open session and refuses new ones; an admin ends any one session.
func TestKillSwitchEndsAll(t *testing.T) {
	e := newEnv(t)
	e.turnOn(nil)
	a := e.mustOpen(admin, 1)
	e.mustOpen(admin2, 2)
	e.mustOpen(owner, 3)
	if got, err := e.svc.End(e.ctx, admin2, a.ID); err != nil || got.EndReason != EndAdmin {
		t.Fatalf("admin ends another's: %+v, %v", got, err)
	}
	if _, err := e.svc.End(e.ctx, viewer, a.ID); codeOf(err) != CodeViewer {
		t.Fatalf("viewer ends: %v", err)
	}
	off := PoolSettings{Enabled: false, Record: true}
	if _, err := e.svc.SetPool(e.ctx, admin, off, true); codeOf(err) != CodeOwnerOnly {
		t.Fatalf("admin kill switch: %v", err)
	}
	if _, err := e.svc.SetPool(e.ctx, owner, off, false); codeOf(err) != CodeNeedsConfirm {
		t.Fatalf("no confirm: %v", err)
	}
	n, err := e.svc.SetPool(e.ctx, owner, off, true)
	if err != nil || n != 2 {
		t.Fatalf("kill switch ended %d, %v", n, err)
	}
	if open, _ := e.db.OpenDebugSessions(e.ctx); len(open) != 0 {
		t.Fatalf("still open: %d", len(open))
	}
	if _, err := e.open(owner, 4); codeOf(err) != CodePoolOff {
		t.Fatalf("open after kill switch: %v", err)
	}
	if len(e.audits(ActionPoolSet)) != 1 {
		t.Fatal("no pool row")
	}
}

// R5.24, R6.4: every step writes its audit row; the opened row carries who, job, repository, machine, backend,
// reason, source and address.
func TestEveryStepIsOneAuditRowInOneTransaction(t *testing.T) {
	e := newEnv(t)
	e.turnOn(nil)
	d := e.mustOpen(admin, 1)
	_, _ = e.open(viewer, 2)
	if _, err := e.svc.Extend(e.ctx, admin, d.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.End(e.ctx, admin, d.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.End(e.ctx, admin, d.ID); codeOf(err) != CodeEnded {
		t.Fatalf("second end: %v", err)
	}
	for action, want := range map[string]int{ActionOpened: 1, ActionRefused: 1, ActionExtended: 1, ActionEnded: 1, ActionRepoSet: 1} {
		if got := e.audits(action); len(got) != want {
			t.Errorf("%s rows = %d, want %d", action, len(got), want)
		}
	}
	op := e.audits(ActionOpened)[0]
	if op.Actor != "admin1" || op.TargetType != "debug_session" || op.TargetID != d.ID || op.Source != "web" {
		t.Fatalf("opened row = %+v", op)
	}
	// A failed write rolls the session back: a duplicate id cannot leave a session without its row.
	e.svc.NewID = func() string { return d.ID }
	if _, err := e.open(admin, 2); err == nil {
		t.Fatal("duplicate id accepted")
	}
	if len(e.audits(ActionOpened)) != 1 {
		t.Fatal("an audit row without its session")
	}
}

// The pool's Record switch off: the session row says no transcript and no recorder is made (R5.25).
func TestRecordOffSaysNoTranscript(t *testing.T) {
	e := newEnv(t)
	e.turnOn(nil)
	if _, err := e.svc.SetPool(e.ctx, owner, PoolSettings{Enabled: true, Record: false}, false); err != nil {
		t.Fatal(err)
	}
	d := e.mustOpen(admin, 1)
	if d.Recorded || e.svc.NewRecorder(d) != nil {
		t.Fatalf("recorded = %v", d.Recorded)
	}
}
