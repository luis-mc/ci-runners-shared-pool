package demand_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/demand"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/github"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/github/fake"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/secrets"
)

// source is a demand source whose last demand the test sets.
type source struct {
	mu   sync.Mutex
	last time.Time
}

func (s *source) LastDemand(string) time.Time { s.mu.Lock(); defer s.mu.Unlock(); return s.last }
func (s *source) set(t time.Time)             { s.mu.Lock(); s.last = t; s.mu.Unlock() }

type wdEnv struct {
	fake     *fake.Server
	src      *source
	wd       *demand.Watchdog
	restarts int
	now      time.Time
}

func newWatchdogEnv(t *testing.T) *wdEnv {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := fake.New()
	t.Cleanup(f.Close)
	f.AppPublicKey = &k.PublicKey
	sec := secrets.NewMemory()
	if err := sec.Put(context.Background(), github.SecretKey(f.AppID, github.SecretPrivateKey),
		pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(k)})); err != nil {
		t.Fatal(err)
	}
	e := &wdEnv{fake: f, src: &source{}, now: time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)}
	f.Now = func() time.Time { return e.now }
	gh := github.New(github.Config{APIBase: f.URL, WebBase: f.URL, Secrets: sec, Now: func() time.Time { return e.now }})
	gh.UseApp(f.AppID, "Iv23liFAKECLIENTID00")
	f.AddInstall(10, "alice", "User")
	f.AddRepo(100, 10, "alice", "demo", false)
	e.wd = &demand.Watchdog{
		Lister: gh,
		Targets: func(context.Context) ([]github.PollTarget, error) {
			return []github.PollTarget{{InstallationID: 10, Owner: "alice", Repo: "demo"}}, nil
		},
		Source:   e.src,
		Restart:  func() { e.restarts++ },
		PoolName: "crsp",
		Quiet:    10 * time.Minute,
		Now:      func() time.Time { return e.now },
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	return e
}

func (e *wdEnv) tick(t *testing.T, after time.Duration) {
	t.Helper()
	e.now = e.now.Add(after)
	if err := e.wd.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func queuedJob(labels ...string) fake.Job {
	return fake.Job{ID: 1, RunID: 11, Repo: "alice/demo", Name: "build", Status: "queued", Event: "push", Labels: labels}
}

func TestQuietDemandRestartsOnceAndStaysQuiet(t *testing.T) {
	e := newWatchdogEnv(t)
	e.fake.AddJob(queuedJob("self-hosted", "Linux", "X64"))

	e.tick(t, 0) // just started: not quiet yet
	if e.restarts != 0 || len(e.wd.QuietRepos()) != 0 {
		t.Fatalf("too early: restarts %d, quiet %+v", e.restarts, e.wd.QuietRepos())
	}
	e.tick(t, 11*time.Minute) // 11 minutes, no demand seen
	q := e.wd.QuietRepos()
	if e.restarts != 1 || len(q) != 1 || !q[0].Restarted || q[0].Queued != 1 || q[0].Repository != "alice/demo" {
		t.Fatalf("first detection: restarts %d, quiet %+v", e.restarts, q)
	}
	e.tick(t, 5*time.Minute) // waiting for the next interval after the restart
	if e.restarts != 1 || len(e.wd.QuietRepos()) != 1 {
		t.Fatalf("after the restart: restarts %d, quiet %+v", e.restarts, e.wd.QuietRepos())
	}
	e.tick(t, 6*time.Minute) // another interval, still quiet: warn stays, no second restart
	if e.restarts != 1 || len(e.wd.QuietRepos()) != 1 {
		t.Fatalf("still quiet: restarts %d, quiet %+v", e.restarts, e.wd.QuietRepos())
	}
}

func TestQuietDemandHealthyAndRecovered(t *testing.T) {
	e := newWatchdogEnv(t)
	e.fake.AddJob(queuedJob("self-hosted", "Linux"))

	// Healthy: the source sees demand all the time.
	for i := 0; i < 4; i++ {
		e.now = e.now.Add(6 * time.Minute)
		e.src.set(e.now)
		if err := e.wd.Tick(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if e.restarts != 0 || len(e.wd.QuietRepos()) != 0 {
		t.Fatalf("healthy: restarts %d, quiet %+v", e.restarts, e.wd.QuietRepos())
	}

	// Quiet, restarted, then the restarted source sees demand again: cleared.
	e.tick(t, 11*time.Minute)
	if e.restarts != 1 || len(e.wd.QuietRepos()) != 1 {
		t.Fatalf("quiet: restarts %d, quiet %+v", e.restarts, e.wd.QuietRepos())
	}
	e.now = e.now.Add(time.Minute)
	e.src.set(e.now)
	if err := e.wd.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(e.wd.QuietRepos()) != 0 {
		t.Fatalf("recovered but still quiet: %+v", e.wd.QuietRepos())
	}
}

func TestQuietDemandIgnoresJobsNotForThePool(t *testing.T) {
	e := newWatchdogEnv(t)
	e.fake.AddJob(queuedJob("self-hosted", "Linux", "gpu")) // a container runner does not offer gpu
	e.fake.AddJob(fake.Job{ID: 2, RunID: 12, Repo: "alice/demo", Name: "x", Status: "queued", Event: "push", Labels: []string{"ubuntu-latest"}})
	e.tick(t, 0)
	e.tick(t, 30*time.Minute)
	if e.restarts != 0 || len(e.wd.QuietRepos()) != 0 {
		t.Fatalf("restarts %d, quiet %+v", e.restarts, e.wd.QuietRepos())
	}
}

func TestQuietDemandNoQueuedJobsNoFinding(t *testing.T) {
	e := newWatchdogEnv(t)
	e.fake.AddJob(fake.Job{ID: 3, RunID: 13, Repo: "alice/demo", Name: "run", Status: "in_progress", Event: "push", Labels: []string{"self-hosted", "Linux"}, RunnerName: "r1"})
	e.tick(t, 0)
	e.tick(t, 30*time.Minute)
	if e.restarts != 0 || len(e.wd.QuietRepos()) != 0 {
		t.Fatalf("restarts %d, quiet %+v", e.restarts, e.wd.QuietRepos())
	}
}
