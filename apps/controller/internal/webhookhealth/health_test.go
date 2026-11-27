package webhookhealth

import (
	"testing"
	"time"
)

func newAt(now *time.Time) *Tracker {
	t := New()
	t.Now = func() time.Time { return *now }
	return t
}

func TestNeverSeenIsWebhookWithNoDelivery(t *testing.T) {
	now := time.Unix(1000, 0)
	s := newAt(&now).Get("a/b")
	if s.Mode != ModeWebhook || !s.LastAt.IsZero() || s.Last != "" {
		t.Fatalf("got %+v", s)
	}
}

func TestThreeFailuresSwitchToPolling(t *testing.T) {
	now := time.Unix(1000, 0)
	tr := newAt(&now)
	tr.Record("A/B", ResultFailed)
	tr.Record("a/b", ResultFailed)
	if tr.Get("a/b").Mode != ModeWebhook {
		t.Fatal("switched too early")
	}
	tr.Record("a/b", ResultMissed)
	s := tr.Get("A/b")
	if s.Mode != ModePolling || s.Failures != 3 || s.SwitchedAt.IsZero() {
		t.Fatalf("got %+v", s)
	}
}

func TestOKResetsFailuresButKeepsPolling(t *testing.T) {
	now := time.Unix(1000, 0)
	tr := newAt(&now)
	tr.Record("a/b", ResultFailed)
	tr.Record("a/b", ResultOK)
	tr.Record("a/b", ResultFailed)
	tr.Record("a/b", ResultFailed)
	if tr.Get("a/b").Mode != ModeWebhook {
		t.Fatal("an ok in between must reset the count")
	}
	tr.Record("a/b", ResultFailed)
	tr.Record("a/b", ResultOK)
	if tr.Get("a/b").Mode != ModePolling {
		t.Fatal("polling stays until the owner switches back")
	}
}

func TestSwitchBack(t *testing.T) {
	now := time.Unix(1000, 0)
	tr := newAt(&now)
	for range 3 {
		tr.Record("a/b", ResultFailed)
	}
	s := tr.SwitchBack("a/b")
	if s.Mode != ModeWebhook || s.Failures != 0 || !s.SwitchedAt.IsZero() {
		t.Fatalf("got %+v", s)
	}
	if s.Last != ResultFailed {
		t.Fatal("the last result stays visible")
	}
}

func TestMissedDelivery(t *testing.T) {
	now := time.Unix(1000, 0)
	tr := newAt(&now)
	tr.PollSawQueued("a/b", 1) // first sight: pending
	now = now.Add(MissedGrace - time.Second)
	tr.PollSawQueued("a/b", 1)
	if tr.Get("a/b").Last != "" {
		t.Fatal("inside the grace")
	}
	now = now.Add(2 * time.Second)
	tr.PollSawQueued("a/b", 1)
	tr.PollSawQueued("a/b", 1) // counted once
	if s := tr.Get("a/b"); s.Last != ResultMissed || s.Failures != 1 {
		t.Fatalf("got %+v", s)
	}
}

func TestDeliveredSettlesPendingJob(t *testing.T) {
	now := time.Unix(1000, 0)
	tr := newAt(&now)
	tr.PollSawQueued("a/b", 7)
	tr.Delivered("a/b", 7)
	now = now.Add(time.Hour)
	tr.PollSawQueued("a/b", 7)
	if s := tr.Get("a/b"); s.Last != ResultOK || s.Failures != 0 {
		t.Fatalf("got %+v", s)
	}
}

// OPS22: an App with no webhook never announces a job, so polling finding
// every job first is not a missed delivery.
func TestNoWebhookNeverCountsAsMissed(t *testing.T) {
	now := time.Unix(1000, 0)
	tr := newAt(&now)
	tr.HooksOn = func() bool { return false }
	if !tr.WebhooksOff() {
		t.Fatal("webhooks should read as off")
	}
	tr.PollSawQueued("a/b", 1)
	now = now.Add(MissedGrace + time.Second)
	tr.PollSawQueued("a/b", 1)
	if s := tr.Get("a/b"); s.Last != "" || s.Failures != 0 {
		t.Fatalf("got %+v", s)
	}
	tr.HooksOn = func() bool { return true }
	tr.PollSawQueued("a/b", 2)
	now = now.Add(MissedGrace + time.Second)
	tr.PollSawQueued("a/b", 2)
	if s := tr.Get("a/b"); s.Last != ResultMissed {
		t.Fatalf("with a webhook the miss counts: %+v", s)
	}
}

func TestLastIsTheNewestDeliveryOfAnyRepository(t *testing.T) { // OPS25
	tr := New()
	now := time.Date(2026, 11, 18, 9, 0, 0, 0, time.UTC)
	tr.Now = func() time.Time { return now }
	if at, res := tr.Last(); !at.IsZero() || res != "" {
		t.Fatalf("no delivery yet: %v %q", at, res)
	}
	tr.Record("a/one", ResultOK)
	now = now.Add(time.Minute)
	tr.Record("a/two", ResultFailed)
	if at, res := tr.Last(); !at.Equal(now) || res != "failed" {
		t.Fatalf("got %v %q", at, res)
	}
}
