package tray

import (
	"context"
	"strings"
	"testing"
	"time"
)

type notices struct{ titles, bodies []string }

func (n *notices) add(t, b string) { n.titles = append(n.titles, t); n.bodies = append(n.bodies, b) }

func newOffer(st Status) (*Opener, *notices) {
	n := &notices{}
	return &Opener{Checker: fakeChecker{st: st}, Version: "1.4.0", Notify: n.add}, n
}

var found = Status{Update: UpdateInfo{Latest: "1.5.0", Available: true, ReleaseURL: "https://example.test/v1.5.0", Jobs: 2}}

// The tray only notifies (LC-22): with no console and no native window it shows one notice in
// the words of the table, and nothing installs.
func TestNoWindowShowsOneNoticeFromTheWordsTable(t *testing.T) {
	o, n := newOffer(found)
	if err := o.LookForUpdates(context.Background()); err == nil {
		t.Fatal("no window could open: the caller hears it")
	}
	if len(n.titles) != 1 || !strings.Contains(n.bodies[0], "Version 1.5.0 is ready.") {
		t.Fatalf("%v %v", n.titles, n.bodies)
	}
}

func TestUpToDateAndAlreadyUpdatingNotifyOnly(t *testing.T) {
	o, n := newOffer(Status{})
	_ = o.LookForUpdates(context.Background())
	if n.titles[0] != "Up to date" || !strings.Contains(n.bodies[0], "newest stable release") {
		t.Fatalf("%v", n.titles)
	}
	busy := found
	busy.Update.State = "waiting_for_jobs"
	o, n = newOffer(busy)
	_ = o.LookForUpdates(context.Background())
	if n.titles[0] != "Already updating" {
		t.Fatalf("%v", n.titles)
	}
}

// One notice per event per 24 hours.
func TestNoticesOncePer24Hours(t *testing.T) {
	now := time.Unix(1000, 0)
	n := &Notices{Now: func() time.Time { return now }}
	if !n.Allow("a") || n.Allow("a") || !n.Allow("b") {
		t.Fatal("first passes, repeat does not, other event does")
	}
	now = now.Add(NoticeWindow - time.Second)
	if n.Allow("a") {
		t.Fatal("still quiet")
	}
	now = now.Add(time.Second)
	if !n.Allow("a") {
		t.Fatal("window over")
	}
}

func TestOfferNoticeIsLimitedByTheNoticeWindow(t *testing.T) {
	o, n := newOffer(found)
	o.Notices = &Notices{}
	_ = o.Offer(context.Background(), found)
	_ = o.Offer(context.Background(), found)
	if len(n.titles) != 1 {
		t.Fatalf("%v", n.titles)
	}
}

// Words: the table's sentence, no banned word.
func TestReadyNoticeUsesTheTableAndNoBannedWord(t *testing.T) {
	_, b := ReadyNotice("1.5.0")
	if b != "Version 1.5.0 is ready." {
		t.Fatalf("%q", b)
	}
	for _, w := range []string{WordAvailableTitle, WordAvailablePrimary, WordNotNow, WordRolledBackTitle, WordRefusedTitle} {
		for _, bad := range []string{"OK", "Yes", "Install now", "Later", "daemon", "binary", "payload"} {
			if strings.Contains(w, bad) {
				t.Errorf("%q has banned word %q", w, bad)
			}
		}
	}
}

// A found update is offered once per version, only when it needs a yes (auto-update off).
func TestOfferedOncePerVersion(t *testing.T) {
	f := &Offered{}
	off := Status{Update: UpdateInfo{Latest: "1.5.0", Available: true, AutoOff: true}}
	if !f.Due(off) || f.Due(off) {
		t.Fatal("offer once")
	}
	off.Update.Latest = "1.6.0"
	if !f.Due(off) {
		t.Fatal("a newer version is offered again")
	}
	if (&Offered{}).Due(Status{Update: UpdateInfo{Latest: "1.5.0", Available: true}}) {
		t.Fatal("auto-update on: the service installs by itself, no offer")
	}
	run := off
	run.Update.State = "installing"
	if (&Offered{}).Due(run) {
		t.Fatal("no offer while updating")
	}
}

// TRAY8: a release that is not newer than the running one is never offered, whatever the service says.
func TestOfferNeverOffersAVersionThatIsNotNewer(t *testing.T) {
	for _, latest := range []string{"1.4.0", "1.3.9"} {
		st := Status{Version: "1.4.0", Update: UpdateInfo{Latest: latest, Available: true}}
		o, n := newOffer(st)
		if err := o.Offer(context.Background(), st); err != nil {
			t.Fatal(err)
		}
		if len(n.titles) != 1 || n.titles[0] != "Up to date" {
			t.Fatalf("%s: notices=%v", latest, n.titles)
		}
		if st.UpdateShown() {
			t.Fatalf("%s: the Update line must stay hidden", latest)
		}
		if h := Header("1.4.0", st); !strings.HasSuffix(h, "up to date") {
			t.Fatalf("%s: header %q", latest, h)
		}
		if (&Offered{}).Due(Status{Version: "1.4.0", Update: UpdateInfo{Latest: latest, Available: true, AutoOff: true}}) {
			t.Fatalf("%s: due", latest)
		}
	}
}
