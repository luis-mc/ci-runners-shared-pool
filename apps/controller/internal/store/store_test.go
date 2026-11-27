package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func openTemp(t *testing.T) *Store {
	t.Helper()
	s, err := Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// TestIntegrationMigrationsAndPing runs the embedded migrations against a real
// SQLite file (STACKSPEC 11.1: ephemeral real datastore, no mocks).
func TestIntegrationMigrationsAndPing(t *testing.T) {
	s := openTemp(t)
	if err := s.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
	n, err := s.CountJobs(context.Background(), "queued")
	if err != nil || n != 0 {
		t.Fatalf("queued jobs = %d, %v", n, err)
	}
}

func TestIntegrationEnqueueIsTransactional(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()

	boom := errors.New("boom")
	err := s.WithTx(ctx, func(tx *Tx) error {
		if _, err := tx.Enqueue("sync", "", ""); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("want boom, got %v", err)
	}
	if n, _ := s.CountJobs(ctx, "queued"); n != 0 {
		t.Fatalf("rolled-back enqueue left %d jobs", n)
	}

	err = s.WithTx(ctx, func(tx *Tx) error {
		if _, err := tx.Audit("system", "test", "", "{}"); err != nil {
			return err
		}
		_, err := tx.Enqueue("sync", "", "")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := s.CountJobs(ctx, "queued"); n != 1 {
		t.Fatalf("committed enqueue: %d jobs, want 1", n)
	}
}

func TestIntegrationEnqueueDedupe(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	a, err := s.Enqueue(ctx, "sync", "", "k1")
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Enqueue(ctx, "sync", "", "k1")
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatalf("dedupe returned %d then %d", a, b)
	}
	if n, _ := s.CountJobs(ctx, "queued"); n != 1 {
		t.Fatalf("%d jobs, want 1", n)
	}
	if _, err := s.Enqueue(ctx, "", "", ""); err == nil {
		t.Fatal("empty kind must fail")
	}
}

func TestWithDeadline(t *testing.T) {
	ctx, cancel := withDeadline(context.Background())
	defer cancel()
	d, ok := ctx.Deadline()
	if !ok || time.Until(d) > DefaultDeadline {
		t.Fatalf("no default deadline: %v %v", d, ok)
	}
	own, cancelOwn := context.WithTimeout(context.Background(), time.Minute)
	defer cancelOwn()
	got, c2 := withDeadline(own)
	defer c2()
	if got != own {
		t.Fatal("an existing deadline must be kept")
	}
}

// A runner's id is recorded in the store and survives a reopen (runners R5.72).
func TestIntegrationRunnerIDsSurviveReopen(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	t0 := RunnerIDs{S: s}
	if err := t0.Put(ctx, "pool-a", 64007); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s2.Close() }()
	id, ok, err := RunnerIDs{S: s2}.Get(ctx, "pool-a")
	if err != nil || !ok || id != 64007 {
		t.Fatalf("Get = %d, %v, %v", id, ok, err)
	}
	if _, ok, _ := (RunnerIDs{S: s2}).Get(ctx, "other"); ok {
		t.Fatal("unknown runner has an id")
	}
}
