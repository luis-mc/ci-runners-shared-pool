package update

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/update/lifecycle"
)

type fakeEngine struct {
	priv     ed25519.PrivateKey
	manifest map[string]interface{}
	sum      string
	prepErr  error
	applied  []string
	checks   int
}

func (f *fakeEngine) LatestManifest(context.Context) ([]byte, Release, error) {
	f.checks++
	body, _ := lifecycle.Canonical(f.manifest)
	sig := hex.EncodeToString(ed25519.Sign(f.priv, body))
	raw, _ := json.Marshal(map[string]interface{}{"manifest": f.manifest, "signature": sig})
	return raw, Release{Version: f.manifest["version"].(string)}, nil
}

func (f *fakeEngine) PrepareVersion(context.Context, string) (string, error) { return f.sum, f.prepErr }

func (f *fakeEngine) Apply(_ context.Context, _ Kind, _ bool, actor string) (Status, error) {
	f.applied = append(f.applied, actor)
	return Status{}, nil
}

type svcFixture struct {
	svc    *Service
	eng    *fakeEngine
	now    time.Time
	rows   []lifecycle.Row
	dir    string
	paired bool
	idle   bool
}

func newSvc(t *testing.T, paired, idle bool) *svcFixture {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	f := &svcFixture{now: time.Unix(1_800_000_000, 0), dir: t.TempDir(), paired: paired, idle: idle}
	f.eng = &fakeEngine{priv: priv, sum: "abc", manifest: map[string]interface{}{
		"product": "crsp", "channel": "stable", "version": "1.1.0", "required": false,
		"expires_at": float64(1_900_000_000), "url": "https://github.com/x", "sha256": "abc"}}
	f.svc = NewService(context.Background(), ServiceOptions{
		Engine: f.eng, Current: "1.0.0", Key: pub, Dir: f.dir,
		Paired: func(context.Context) bool { return f.paired },
		Idle:   func(context.Context) bool { return f.idle },
		Audit:  func(_ context.Context, r lifecycle.Row) { f.rows = append(f.rows, r) },
		Now:    func() time.Time { return f.now },
	})
	return f
}

func (f *svcFixture) advance(d time.Duration) {
	f.now = f.now.Add(d)
	f.svc.Tick(context.Background())
}

func TestServiceNoCheckWhileUnpaired(t *testing.T) {
	f := newSvc(t, false, true)
	f.advance(24 * time.Hour)
	if f.eng.checks != 0 || f.svc.Status(context.Background()).State != "unpaired" {
		t.Fatalf("checks=%d state=%s", f.eng.checks, f.svc.Status(context.Background()).State)
	}
	if _, err := f.svc.Check(context.Background(), "me"); !errors.Is(err, ErrNotPaired) {
		t.Fatalf("manual check: %v", err)
	}
	f.paired = true
	f.advance(time.Second)
	if st := f.svc.Status(context.Background()); st.State != "up_to_date" {
		t.Fatalf("state after pairing: %s", st.State)
	}
}

func TestServiceAutomaticInstallWhenIdle(t *testing.T) {
	f := newSvc(t, true, true)
	f.advance(11 * time.Minute) // the first check is 1 to 10 minutes after start
	st := f.svc.Status(context.Background())
	if st.State != "installing" || st.Available != "1.1.0" {
		t.Fatalf("state %s available %s", st.State, st.Available)
	}
	if len(f.eng.applied) != 1 || f.eng.applied[0] != "auto-update" {
		t.Fatalf("applied %v", f.eng.applied)
	}
	if _, err := os.Stat(filepath.Join(f.dir, installFile)); err != nil {
		t.Fatalf("install record: %v", err)
	}
	if len(f.rows) == 0 || f.rows[0].Kind != "check" || f.rows[0].Actor != "updater" || f.rows[0].Result != "ok" {
		t.Fatalf("rows %+v", f.rows)
	}
}

func TestServiceWaitsForJobsThenInstalls(t *testing.T) {
	f := newSvc(t, true, false)
	f.advance(11 * time.Minute)
	if st := f.svc.Status(context.Background()); st.State != "waiting" || !st.WaitingForJobs || len(f.eng.applied) != 0 {
		t.Fatalf("%+v applied %v", st, f.eng.applied)
	}
	f.idle = true
	f.advance(time.Second)
	if st := f.svc.Status(context.Background()); st.State != "installing" {
		t.Fatalf("state %s", st.State)
	}
}

func TestServiceAskFirstAndRemoteInstall(t *testing.T) {
	f := newSvc(t, true, true)
	if _, err := f.svc.SetPolicy(context.Background(), lifecycle.PolicyAskFirst, nil); err != nil {
		t.Fatal(err)
	}
	f.advance(11 * time.Minute)
	if st := f.svc.Status(context.Background()); st.State != "review" || len(f.eng.applied) != 0 {
		t.Fatalf("%+v", st)
	}
	if _, err := f.svc.Install(context.Background(), "luis", true, "9.9.9"); !errors.Is(err, ErrNoOffer) {
		t.Fatalf("wrong version: %v", err)
	}
	if _, err := f.svc.Install(context.Background(), "luis", true, "1.1.0"); err != nil {
		t.Fatal(err)
	}
	var rec installRecord
	b, _ := os.ReadFile(filepath.Join(f.dir, installFile))
	_ = json.Unmarshal(b, &rec)
	if rec.Actor != "luis" || !rec.Remote || len(f.eng.applied) != 1 || f.eng.applied[0] != "luis" {
		t.Fatalf("record %+v applied %v", rec, f.eng.applied)
	}
}

func TestServiceBadSignatureIsRefused(t *testing.T) {
	f := newSvc(t, true, true)
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	f.eng.priv = other
	f.advance(11 * time.Minute)
	st := f.svc.Status(context.Background())
	last := f.rows[len(f.rows)-1]
	if st.State != "refused" || last.Kind != "refused" || last.Detail != "bad_signature" || len(f.eng.applied) != 0 {
		t.Fatalf("%s %+v", st.State, last)
	}
}

func TestServiceManifestFailureReasonFromPrepare(t *testing.T) {
	f := newSvc(t, true, true)
	f.eng.prepErr = &ManifestFailure{Reason: "expired"}
	f.advance(11 * time.Minute)
	last := f.rows[len(f.rows)-1]
	if last.Kind != "refused" || last.Detail != "expired" {
		t.Fatalf("%+v", last)
	}
}

func TestServiceRestoreAfterFailedInstall(t *testing.T) {
	dir := t.TempDir()
	at := time.Unix(1_800_000_000, 0)
	if err := WriteResult(dir, Result{OK: false, Kind: "update", From: "1.0.0", To: "1.1.0", Error: "health", At: at}); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(installRecord{Actor: "luis", Remote: true, Version: "1.1.0"})
	_ = os.WriteFile(filepath.Join(dir, installFile), b, 0o600)
	if err := RecordFailure(dir, "1.1.0", at, false); err != nil {
		t.Fatal(err)
	}
	var rows []lifecycle.Row
	svc := NewService(context.Background(), ServiceOptions{Current: "1.0.0", Dir: dir, Now: func() time.Time { return at },
		Audit: func(_ context.Context, r lifecycle.Row) { rows = append(rows, r) }})
	st := svc.Status(context.Background())
	if st.State != "rolled_back" || st.Failures == nil || st.Failures.Count != 1 || !st.Failures.RetryAt.Equal(at.Add(time.Hour)) {
		t.Fatalf("%+v %+v", st, st.Failures)
	}
	if len(rows) != 2 || rows[0].Kind != "install" || rows[0].Result != "rolled_back" || rows[0].Actor != "luis" || !rows[0].Remote || rows[1].Kind != "rollback" {
		t.Fatalf("rows %+v", rows)
	}
	if _, err := os.Stat(filepath.Join(dir, installFile)); err == nil {
		t.Fatal("the install record stays after it was audited")
	}
}

func TestServicePolicyIsKept(t *testing.T) {
	f := newSvc(t, true, true)
	if _, err := f.svc.SetPolicy(context.Background(), "sometimes", nil); err == nil {
		t.Fatal("a bad policy was accepted")
	}
	if _, err := f.svc.SetPolicy(context.Background(), lifecycle.PolicyAutomatic, &lifecycle.Window{Start: "25:00", End: "03:00"}); err == nil {
		t.Fatal("a bad window was accepted")
	}
	w := &lifecycle.Window{Start: "01:00", End: "05:00"}
	if _, err := f.svc.SetPolicy(context.Background(), lifecycle.PolicyAskFirst, w); err != nil {
		t.Fatal(err)
	}
	again := NewService(context.Background(), ServiceOptions{Current: "1.0.0", Dir: f.dir, Now: func() time.Time { return f.now }})
	if st := again.Status(context.Background()); st.Policy != lifecycle.PolicyAskFirst || st.Window == nil || st.Window.Start != "01:00" {
		t.Fatalf("%+v", st)
	}
}

// CRSP244: a manual check only looks. It never installs, under Automatic or Ask first.
func TestServiceManualCheckNeverInstalls(t *testing.T) {
	for _, policy := range []string{lifecycle.PolicyAutomatic, lifecycle.PolicyAskFirst} {
		f := newSvc(t, true, true)
		if _, err := f.svc.SetPolicy(context.Background(), policy, nil); err != nil {
			t.Fatal(err)
		}
		st, err := f.svc.Check(context.Background(), "luis")
		if err != nil {
			t.Fatal(err)
		}
		if st.State != "review" || st.Available != "1.1.0" || len(f.eng.applied) != 0 {
			t.Fatalf("%s: %+v applied %v", policy, st, f.eng.applied)
		}
		if _, err := f.svc.Install(context.Background(), "luis", false, "1.1.0"); err != nil || len(f.eng.applied) != 1 {
			t.Fatalf("%s: click install: %v applied %v", policy, err, f.eng.applied)
		}
	}
}

// The idle timer still installs under Automatic after a manual look was not taken.
func TestServiceTimerStillInstallsAutomaticAfterLook(t *testing.T) {
	f := newSvc(t, true, true)
	f.advance(11 * time.Minute)
	if len(f.eng.applied) != 1 || f.eng.applied[0] != "auto-update" {
		t.Fatalf("applied %v", f.eng.applied)
	}
}
