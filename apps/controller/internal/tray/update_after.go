package tray

import (
	"context"
	"time"
)

// After an update the new tray (started with --updated) says the result once, and never in a
// modal box (TRAY5 D13). Who started the update decides how:
//
//   - a person pressed Update now in the tray window: the window is the place; it shows Done
//     itself, and the new tray only opens it again when the person closed it;
//   - nobody clicked (automatic update, or an older service that does not say): one desktop
//     toast with the crsp icon, "Now running 0.1.13."; clicking it opens the window on Done;
//   - the console or the command line started it: they showed the end already; nothing more.

// AfterAction is what the new tray does about a finished update.
type AfterAction int

// The three answers.
const (
	AfterNothing AfterAction = iota
	AfterToast
	AfterWindow
)

// Who started an update (internal/update Stage.StartedBy).
const (
	StartedByAuto    = "auto"
	StartedByTray    = "tray"
	StartedByConsole = "console"
)

// DecideAfterUpdate chooses the toast or the window for a stage. native says this OS has the
// native update window; without it the toast is the only way to say it.
func DecideAfterUpdate(stage *StageInfo, native bool) AfterAction {
	if stage == nil {
		return AfterToast
	}
	switch stage.StartedBy {
	case StartedByTray:
		if native {
			return AfterWindow
		}
		return AfterToast
	case StartedByConsole:
		return AfterNothing
	}
	return AfterToast // auto, or a stage that does not say
}

// ToastTitle and ToastBody are the words of the one toast after an automatic update.
const ToastTitle = ProductName

// ToastBody is "Now running 0.1.13."
func ToastBody(version string) string { return "Now running " + version + "." }

// AfterEnv is what AfterUpdate uses from the machine; tests replace each part.
type AfterEnv struct {
	Status func(ctx context.Context) (Status, error)
	// Toast shows one desktop toast; click runs when the person clicks it.
	Toast func(title, body string, click func())
	// OpenWindow opens the native update window on what runs now (live); nil: no native window.
	OpenWindow func(ctx context.Context) error
	// Due says whether this version's result was not said yet (once per version and user).
	Due      func(version string) bool
	Version  string        // the version of this tray: the new one
	Interval time.Duration // between two looks at the service
	Limit    time.Duration // give up after this long
	Sleep    func(ctx context.Context, d time.Duration)
}

// AfterUpdate waits for the stage of the finished update and then does what DecideAfterUpdate says.
func AfterUpdate(ctx context.Context, e AfterEnv) AfterAction {
	if e.Interval <= 0 {
		e.Interval = 2 * time.Second
	}
	if e.Limit <= 0 {
		e.Limit = 3 * time.Minute
	}
	if e.Sleep == nil {
		e.Sleep = func(ctx context.Context, d time.Duration) {
			select {
			case <-ctx.Done():
			case <-time.After(d):
			}
		}
	}
	var stage *StageInfo
	for waited := time.Duration(0); waited <= e.Limit && ctx.Err() == nil; waited += e.Interval {
		if st, err := e.Status(ctx); err == nil && st.Update.Stage != nil {
			stage = st.Update.Stage
			if stage.Name == "done" || stage.Name == "failed" {
				break
			}
		}
		e.Sleep(ctx, e.Interval)
	}
	if stage != nil && stage.Name == "failed" {
		return AfterNothing // the old version still runs; the window or the console said it
	}
	act := DecideAfterUpdate(stage, e.OpenWindow != nil)
	if act == AfterNothing || (e.Due != nil && !e.Due(e.Version)) {
		return AfterNothing
	}
	switch act {
	case AfterWindow:
		if err := e.OpenWindow(ctx); err != nil {
			e.toast(ctx)
		}
	case AfterToast:
		e.toast(ctx)
	}
	return act
}

func (e AfterEnv) toast(ctx context.Context) {
	if e.Toast == nil {
		return
	}
	click := func() {
		if e.OpenWindow != nil {
			_ = e.OpenWindow(ctx)
		}
	}
	e.Toast(ToastTitle, ToastBody(e.Version), click)
}
