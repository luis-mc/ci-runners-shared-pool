package checks

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"strings"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/doctor"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/github"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/github/fake"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/secrets"
)

type quietFake struct {
	list []doctor.QuietDemand
	err  error
}

func (q quietFake) QuietDemand(context.Context) ([]doctor.QuietDemand, error) { return q.list, q.err }

func TestDemandQuiet(t *testing.T) { // github-integration R5.37
	f := &fleet{repos: []doctor.Repository{repo(1, "o/r", nil), repo(2, "o/s", nil)}}
	quiet := quietFake{list: []doctor.QuietDemand{{Repository: "o/r", Queued: 2, Since: now.Add(-12 * time.Minute)}}}
	fs := run(t, doctor.Env{GitHub: newGH(), Fleet: f, Demand: quiet}, "demand.quiet")
	by := map[string]doctor.Finding{}
	for _, x := range fs {
		by[x.Subject.Name] = x
	}
	if len(fs) != 2 || by["o/s"].Severity != doctor.OK {
		t.Fatalf("healthy repo must be ok: %+v", fs)
	}
	w := by["o/r"]
	if w.Severity != doctor.Warn || !strings.Contains(w.Title, "2 jobs") || !strings.Contains(w.Fix.Text, "restarts the demand loop once") {
		t.Fatalf("first detection: %+v", w)
	}

	// Restarted and still quiet: the warn says so and what to do.
	quiet.list[0].Restarted, quiet.list[0].RestartedAt = true, now.Add(-2*time.Minute)
	fs = run(t, doctor.Env{GitHub: newGH(), Fleet: f, Demand: quiet}, "demand.quiet")
	for _, x := range fs {
		if x.Subject.Name == "o/r" && (x.Severity != doctor.Warn || !strings.Contains(x.Detail, "restarted the demand loop once") || !strings.Contains(x.Fix.Text, "Restart the controller")) {
			t.Fatalf("still quiet: %+v", x)
		}
	}

	// No demand source: skip, never a warn.
	fs = run(t, doctor.Env{GitHub: newGH(), Fleet: f}, "demand.quiet")
	if len(fs) != 1 || fs[0].Severity != doctor.Skip {
		t.Fatalf("no source: %+v", fs)
	}
}

// restHook is a real REST client on the fake GitHub, as the doctor's GitHub.
func restHook(t *testing.T, mut func(*fake.Server)) doctor.GitHub {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := fake.New()
	t.Cleanup(f.Close)
	f.AppPublicKey = &k.PublicKey
	f.Now = func() time.Time { return now }
	sec := secrets.NewMemory()
	if err := sec.Put(context.Background(), github.SecretKey(f.AppID, github.SecretPrivateKey),
		pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(k)})); err != nil {
		t.Fatal(err)
	}
	f.AddInstall(7, "o", "User")
	f.AddRepo(100, 7, "o", "r", false)
	mut(f)
	gh := github.New(github.Config{APIBase: f.URL, WebBase: f.URL, Secrets: sec, Now: func() time.Time { return now }})
	gh.UseApp(f.AppID, "Iv23liFAKECLIENTID00")
	return gh
}

func webhookFinding(t *testing.T, g doctor.GitHub) doctor.Finding {
	t.Helper()
	fs := run(t, doctor.Env{Now: now, GitHub: g, Fleet: &fleet{repos: []doctor.Repository{repo(1, "o/r", nil)}}}, "github.webhook")
	if len(fs) != 1 {
		t.Fatalf("findings %+v", fs)
	}
	return fs[0]
}

func TestGithubWebhook(t *testing.T) { // doctor R5.16
	d := func(id int64, ago time.Duration, code int) fake.Delivery {
		return fake.Delivery{ID: id, DeliveredAt: now.Add(-ago), Event: "workflow_job", StatusCode: code}
	}
	// Deliveries succeed: ok.
	g := restHook(t, func(f *fake.Server) { f.Deliveries = []fake.Delivery{d(3, time.Hour, 200), d(2, 2*time.Hour, 200)} })
	if got := webhookFinding(t, g); got.Severity != doctor.OK {
		t.Fatalf("ok: %+v", got)
	}
	// The newest failed: warn.
	g = restHook(t, func(f *fake.Server) { f.Deliveries = []fake.Delivery{d(3, time.Hour, 502), d(2, 2*time.Hour, 200)} })
	if got := webhookFinding(t, g); got.Severity != doctor.Warn || !strings.Contains(got.Title, "1 of 2") || !strings.Contains(got.Detail, "502") {
		t.Fatalf("failed: %+v", got)
	}
	// Only old deliveries and a job open: warn that none arrived in 24 h.
	g = restHook(t, func(f *fake.Server) {
		f.Deliveries = []fake.Delivery{d(1, 30*time.Hour, 200)}
		f.AddJob(fake.Job{ID: 1, RunID: 11, Repo: "o/r", Name: "b", Status: "queued", Event: "push", Labels: []string{"self-hosted"}})
	})
	if got := webhookFinding(t, g); got.Severity != doctor.Warn || !strings.Contains(got.Title, "No webhook delivery arrived in 24 hours") {
		t.Fatalf("none with jobs: %+v", got)
	}
	// No delivery and no job: nothing to deliver, ok.
	g = restHook(t, func(*fake.Server) {})
	if got := webhookFinding(t, g); got.Severity != doctor.OK {
		t.Fatalf("none, no jobs: %+v", got)
	}
}
