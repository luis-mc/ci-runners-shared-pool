package tray

import (
	"context"
	"sync"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/update"
)

// The tray only notifies (LC-22). A found update opens the console page, or the one native
// window when the console cannot open; when neither can, the tray shows a notice. The tray never
// asks a question of its own: no Yes/No box, no OK box.

// ReadyNotice is the notice for a found update: one line from the lifecycle words table.
func ReadyNotice(version string) (title, body string) {
	return ProductName, Fill(WordAvailableTitle, "version", version)
}

// Offer opens the place where the person decides: the console page, or the native window.
// Only the person's answer there installs; a check alone never does.
func (o *Opener) Offer(ctx context.Context, st Status) error {
	// Never offer a version that is not newer than the one that runs (TRAY8).
	if !st.NewerFound() || !o.newerThanRunning(st.Update.Latest) {
		o.notify(WordUpToDate, ProductName+" "+o.Version+" is the newest stable release.")
		return nil
	}
	if o.OpenUpdate(ctx) == nil {
		return nil
	}
	if o.Notices == nil || o.Notices.Allow("update-ready:"+st.Update.Latest) {
		t, b := ReadyNotice(st.Update.Latest)
		o.notify(t, b+" Open the console, Settings, Updates.")
	}
	return errNoWindow
}

// newerThanRunning says latest is newer than the version this tray runs with. An unknown
// version is trusted: the service has already compared (TRAY8).
func (o *Opener) newerThanRunning(latest string) bool {
	return o.Version == "" || update.Newer(o.Version, latest)
}

// Offered remembers the release the person was already asked about in this tray, so a
// found update is offered once per version (Later is an answer) and not at every poll.
type Offered struct {
	mu   sync.Mutex
	last string
}

// Due says whether the status holds a found update to offer now: a newer release, no
// update running, automatic updates off (with them on the service installs by itself),
// and not offered yet. It records the offer.
func (f *Offered) Due(st Status) bool {
	u := st.Update
	if !st.NewerFound() || u.Updating() || !u.AutoOff {
		return false
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.last == u.Latest {
		return false
	}
	f.last = u.Latest
	return true
}
