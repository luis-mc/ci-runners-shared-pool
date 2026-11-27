package tray

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestWindowModeIsReviewUntilAnUpdateRuns(t *testing.T) {
	if WindowMode(Status{}) != "review" {
		t.Fatal("no update: review")
	}
	if WindowMode(Status{Update: UpdateInfo{State: "downloading"}}) != "live" {
		t.Fatal("running: live")
	}
	if WindowMode(Status{Update: UpdateInfo{Stage: &StageInfo{Name: "done"}}}) != "live" {
		t.Fatal("done: live, so a toast click lands on Done")
	}
}

func TestUpdateWindowArgs(t *testing.T) {
	got := UpdateWindowArgs("review", "0.1.12", "0.1.13", 1500, 20, true)
	want := []string{"--mode", "review", "--from", "0.1.12", "--to", "0.1.13", "--at", "1500,20"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%v", got)
	}
	if got := UpdateWindowArgs("live", "", "", 0, 0, false); !reflect.DeepEqual(got, []string{"--mode", "live"}) {
		t.Fatalf("%v", got)
	}
}

// D12: with a native window the menu item and the offer open it, not the browser page.
func TestOpenUpdatePrefersTheNativeWindow(t *testing.T) {
	var gotMode string
	browser := 0
	o := &Opener{
		Native:  func(_ context.Context, mode string, _ Status) error { gotMode = mode; return nil },
		Window:  func(string) error { browser++; return nil },
		Targets: &fakeTargets{},
		Current: func() Status { return Status{State: StateHealthy, Update: UpdateInfo{State: "waiting_for_jobs"}} },
	}
	if err := o.OpenUpdate(context.Background()); err != nil || gotMode != "live" || browser != 0 {
		t.Fatalf("%v %q %d", err, gotMode, browser)
	}
	// a native window that cannot start falls back to the console page
	o.Native = func(context.Context, string, Status) error { return errors.New("no") }
	if err := o.OpenUpdate(context.Background()); err != nil || browser != 1 {
		t.Fatalf("%v %d", err, browser)
	}
}

// TRAY9: a done stage of an earlier update must not hide a newer release.
func TestWindowModeTable(t *testing.T) {
	done := func(v string) *StageInfo { return &StageInfo{Name: "done", Version: v} }
	cases := []struct {
		name string
		st   Status
		want string
	}{
		{"no stage", Status{Version: "0.1.17", Update: UpdateInfo{Latest: "0.1.18"}}, "review"},
		{"leftover done, newer latest", Status{Version: "0.1.17", Update: UpdateInfo{Latest: "0.1.18", Stage: done("0.1.17")}}, "review"},
		{"real done, same latest", Status{Version: "0.1.18", Update: UpdateInfo{Latest: "0.1.18", Stage: done("0.1.18")}}, "live"},
		{"done, latest not yet known", Status{Version: "0.1.18", Update: UpdateInfo{Stage: done("0.1.18")}}, "live"},
		{"done without version, newer latest", Status{Version: "0.1.17", Update: UpdateInfo{Latest: "0.1.18", Stage: done("")}}, "review"},
		{"done, latest older", Status{Version: "0.1.18", Update: UpdateInfo{Latest: "0.1.17", Stage: done("0.1.18")}}, "live"},
		{"updating over a leftover done", Status{Version: "0.1.17", Update: UpdateInfo{State: "downloading", Latest: "0.1.18", Stage: done("0.1.17")}}, "live"},
		{"old failed stage, newer latest", Status{Version: "0.1.17", Update: UpdateInfo{Latest: "0.1.18", Stage: &StageInfo{Name: "failed", Version: "0.1.17"}}}, "review"},
	}
	for _, c := range cases {
		if got := WindowMode(c.st); got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
}
