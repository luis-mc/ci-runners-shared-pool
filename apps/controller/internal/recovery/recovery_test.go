package recovery_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/machines/secretstest"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/recovery"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/tlsca"
)

func signed(t *testing.T, now time.Time) (recovery.Answer, *tlsca.Authority) {
	t.Helper()
	ca, err := tlsca.LoadOrCreate(context.Background(), tlsca.Options{Dir: t.TempDir(), Secrets: secretstest.New()})
	if err != nil {
		t.Fatal(err)
	}
	a := recovery.Answer{Action: recovery.ActionUpdate, MachineID: "m1", Nonce: "n1", Version: "1.2.0", Asset: "crsp-linux-x64.tar.gz",
		SHA256: strings.Repeat("ab", 32), IssuedAt: now.Unix(), ExpiresAt: now.Add(time.Minute).Unix()}
	a.Signature, a.Chain, err = ca.Sign(a.Message())
	if err != nil {
		t.Fatal(err)
	}
	return a, ca
}

// R5.44: every check the agent makes before it trusts a target.
func TestCheckAnswer(t *testing.T) {
	now := time.Now()
	good, ca := signed(t, now)
	pin := ca.Fingerprint()
	if _, err := recovery.CheckAnswer(good, pin, "m1", "n1", now); err != nil {
		t.Fatalf("good answer: %v", err)
	}
	mut := func(f func(a *recovery.Answer)) recovery.Answer { a := good; f(&a); return a }
	cases := map[string]struct {
		a    recovery.Answer
		want error
		at   time.Time
	}{
		"tampered sha":     {mut(func(a *recovery.Answer) { a.SHA256 = strings.Repeat("cd", 32) }), recovery.ErrBadTarget, now},
		"tampered version": {mut(func(a *recovery.Answer) { a.Version = "9.9.9" }), recovery.ErrBadTarget, now},
		"no signature":     {mut(func(a *recovery.Answer) { a.Signature = nil }), recovery.ErrBadTarget, now},
		"other machine":    {mut(func(a *recovery.Answer) { a.MachineID = "m2" }), recovery.ErrNotForMe, now},
		"replayed nonce":   {mut(func(a *recovery.Answer) { a.Nonce = "old" }), recovery.ErrNotForMe, now},
		"expired":          {good, recovery.ErrExpired, now.Add(5 * time.Minute)},
		"no update":        {recovery.Answer{Action: recovery.ActionNone}, recovery.ErrNoUpdate, now},
	}
	for name, c := range cases {
		if _, err := recovery.CheckAnswer(c.a, pin, "m1", "n1", c.at); !errors.Is(err, c.want) {
			t.Errorf("%s: got %v, want %v", name, err, c.want)
		}
	}
	// Signed by another controller: the pin of ours does not accept it.
	other, _ := signed(t, now)
	if _, err := recovery.CheckAnswer(other, pin, "m1", "n1", now); !errors.Is(err, recovery.ErrBadTarget) {
		t.Errorf("other controller: %v", err)
	}
}
