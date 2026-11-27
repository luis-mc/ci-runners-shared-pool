package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/github"
)

// The trigger scans a new commit once, never blocks the webhook, and a failed
// scan changes nothing (scheduling R5.84, R5.86).
func TestSizeTriggerScansEachCommitOnce(t *testing.T) {
	var mu sync.Mutex
	var got []string
	done := make(chan struct{}, 8)
	tr := newSizeTrigger(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	sha1, sha2 := strings.Repeat("1", 40), strings.Repeat("2", 40)
	// Before set, a job event does nothing and is not remembered.
	tr.Job(github.Job{Repository: "o/r", HeadSHA: sha1})
	tr.set(func(_ context.Context, repo, commit string) error {
		mu.Lock()
		got = append(got, repo+"@"+commit[:1])
		mu.Unlock()
		done <- struct{}{}
		if commit == sha2 {
			return errors.New("GitHub is down")
		}
		return nil
	})
	tr.Job(github.Job{Repository: "o/r", HeadSHA: sha1})
	tr.Job(github.Job{Repository: "o/r", HeadSHA: sha1})
	tr.Job(github.Job{Repository: "o/r", HeadSHA: "short"})
	tr.Job(github.Job{HeadSHA: sha1})
	<-done
	tr.Job(github.Job{Repository: "o/r", HeadSHA: sha2})
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the second commit was not scanned")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 2 || got[0] != "o/r@1" || got[1] != "o/r@2" {
		t.Fatalf("scans = %v", got)
	}
	var nilTrigger *sizeTrigger
	nilTrigger.Job(github.Job{Repository: "o/r", HeadSHA: sha1})
	nilTrigger.set(nil)
}
