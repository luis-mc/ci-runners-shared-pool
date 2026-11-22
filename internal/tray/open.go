package tray

import (
	"context"
	"fmt"
	"github.com/luis-mc/ci-runners-shared-pool/internal/addresses"
	"github.com/luis-mc/ci-runners-shared-pool/internal/update"
	"os/exec"
	"runtime"
)

// LinkSource asks the service for a one-time link.
type LinkSource interface {
	Link(ctx context.Context) (string, error)
}

// TargetLinkSource asks for a one-time link that lands on a console path.
type TargetLinkSource interface {
	LinkTo(ctx context.Context, target string) (string, error)
}

// UpdateChecker asks the service to look for a newer stable release now.
type UpdateChecker interface {
	Check(ctx context.Context) (Status, error)
}

// Review steps of the console, opened through a one-time link. The question
// (the yes) is asked there, never in the tray (tray R5.15, R5.18; decision T2).
const (
	ReviewUpdatePath  = "/settings/updates/review?kind=update"
	ReviewRestartPath = "/settings/updates/review?kind=restart"
)

// Notifier shows one desktop notice (Problem tone: what failed, why, what next).
type Notifier func(title, body string)

// Launcher opens a URL or folder with the OS default handler.
type Launcher func(target string) error

// PolicySetter changes this machine's mode and pause through the service
// (tray R5.42). Each call answers with the status after the change.
type PolicySetter interface {
	SetMode(ctx context.Context, mode string) (Status, error)
	Pause(ctx context.Context, kind string) (Status, error)
	Resume(ctx context.Context) (Status, error)
}

// Opener implements Open and Open log folder.
type Opener struct {
	Policy  PolicySetter
	Links   LinkSource
	Targets TargetLinkSource // for the Review steps; nil when the service cannot
	Checker UpdateChecker
	// RepoURL is the repository page that Open on GitHub opens; empty takes the github.com page.
	RepoURL string
	Version string // this tray's own version, for the "up to date" notice
	Browser Launcher
	// Window opens a URL in a small app-style window (TRAY4); nil: the update is asked with Ask.
	Window Launcher
	// Native opens the native update window (Windows, TRAY5); nil: the console page opens instead.
	Native NativeWindow
	Files  Launcher
	// Notices keeps one notice per event per 24 hours (LC-22); nil lets every notice through.
	Notices *Notices
	Notify  Notifier
	// Current returns the last known status; Open refuses a dead link while stopped (R5.14).
	Current func() Status
	LogDir  func() string
}

// Open asks for a one-time link and opens it in the browser (R5.6, R5.14).
func (o *Opener) Open(ctx context.Context) error {
	return o.open(ctx, "Could not open the console", func() (string, error) { return o.Links.Link(ctx) })
}

// OpenReview opens the console at the Review step of an update or a restart. It
// names the running jobs and asks the yes; nothing changes before that.
func (o *Opener) OpenReview(ctx context.Context, path string) error {
	if o.Targets == nil {
		o.notify("Could not open the console", "This service cannot open a Review step. Open the console and go to Settings, Updates.")
		return fmt.Errorf("no target links")
	}
	return o.open(ctx, "Could not open the Review step", func() (string, error) { return o.Targets.LinkTo(ctx, path) })
}

// OpenPage opens a console page (Jobs, Doctor) through a one-time link. A
// service that cannot link to a page opens the console start page instead.
func (o *Opener) OpenPage(ctx context.Context, path string) error {
	if o.Targets == nil {
		return o.Open(ctx)
	}
	return o.open(ctx, "Could not open the console", func() (string, error) { return o.Targets.LinkTo(ctx, path) })
}

// OpenUpdate opens the console's update page in a small window: it asks for the yes and
// then shows the progress (TRAY4). It returns an error when no window could be opened,
// so the caller can fall back to the native box.
func (o *Opener) OpenUpdate(ctx context.Context) error {
	if o.Native != nil {
		var st Status
		if o.Current != nil {
			st = o.Current()
		}
		if st.State != StateStopped {
			if err := o.Native(ctx, WindowMode(st), st); err == nil {
				return nil
			}
		}
	}
	if o.Window == nil || o.Targets == nil {
		return errNoWindow
	}
	if o.Current != nil {
		if st := o.Current(); st.State == StateStopped {
			return errNoWindow
		}
	}
	url, err := o.Targets.LinkTo(ctx, UpdatePath)
	if err != nil {
		return err
	}
	return o.Window(url)
}

var errNoWindow = fmt.Errorf("no update window")

// OpenGitHub opens the repository page.
func (o *Opener) OpenGitHub() error {
	page := o.RepoURL
	if page == "" {
		page = addresses.Defaults().RepoPage(update.Repo)
	}
	if err := o.Browser(page); err != nil {
		o.notify("No browser found", "Paste this address into a browser: "+page)
		return err
	}
	return nil
}

// LookForUpdates asks the service to look now and says the result in one notice.
func (o *Opener) LookForUpdates(ctx context.Context) error {
	if o.Checker == nil {
		return fmt.Errorf("no update checker")
	}
	st, err := o.Checker.Check(ctx)
	if err != nil {
		o.notify("Could not look for updates", err.Error()+". Check the status line, then try again.")
		return err
	}
	switch {
	case st.Update.Updating():
		// Show the progress when a window can open; say it in a notice when not.
		if o.OpenUpdate(ctx) == nil {
			return nil
		}
		o.notify("Already updating", "Version "+st.Update.Latest+" is on its way. Running jobs finish first.")
	case st.NewerFound() && o.newerThanRunning(st.Update.Latest):
		// A look only looks: the person is asked, and nothing installs before the yes (CRSP147).
		return o.Offer(ctx, st)
	default:
		o.notify("Up to date", ProductName+" "+o.Version+" is the newest stable release.")
	}
	return nil
}

func (o *Opener) open(ctx context.Context, failTitle string, link func() (string, error)) error {
	if o.Current != nil {
		if st := o.Current(); st.State == StateStopped {
			o.notify("Service stopped",
				fmt.Sprintf("The service stopped with exit code %d. The log folder has the reason: use Open log folder.", st.ExitCode))
			return fmt.Errorf("service stopped, exit code %d", st.ExitCode)
		}
	}
	url, err := link()
	if err != nil {
		o.notify(failTitle, "The service did not give a sign-in link: "+err.Error()+". Check the status line, then try again.")
		return err
	}
	if err := o.Browser(url); err != nil {
		// No browser: show the link so the user can paste it (spec section 8).
		o.notify("No browser found", "Paste this link into a browser: "+url)
		return err
	}
	return nil
}

// OpenLogFolder opens the service log folder in the file manager (R5.13).
func (o *Opener) OpenLogFolder() error {
	dir := o.LogDir()
	if err := o.Files(dir); err != nil {
		o.notify("Could not open the log folder", dir+": "+err.Error())
		return err
	}
	return nil
}

// OpenDigest opens the weekly digest file, a draft the service wrote and never sent (ci-run-insights R5.38).
func (o *Opener) OpenDigest(path string) error {
	if path == "" {
		o.notify("No weekly digest yet", "The first one is written on Monday at 08:00 Lisbon time.")
		return nil
	}
	if err := o.Files(path); err != nil {
		o.notify("Could not open the weekly digest", path+": "+err.Error())
		return err
	}
	return nil
}

func (o *Opener) notify(title, body string) {
	if o.Notify != nil {
		o.Notify(title, body)
	}
}

// OSLauncher opens target with the OS default handler.
func OSLauncher(target string) error {
	// The launched program outlives this call, so it takes no cancelable context.
	ctx := context.Background()
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.CommandContext(ctx, "rundll32", "url.dll,FileProtocolHandler", target)
	case "darwin":
		cmd = exec.CommandContext(ctx, "open", target)
	default:
		cmd = exec.CommandContext(ctx, "xdg-open", target)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

// SetMode sets the machine mode and hands the new status to show, so the menu
// follows the change without waiting for the next poll.
func (o *Opener) SetMode(ctx context.Context, mode string, show func(Status)) error {
	return o.policy("Could not change the mode", show, func() (Status, error) { return o.Policy.SetMode(ctx, mode) })
}

// PauseFor pauses new jobs for one hour or until Resume.
func (o *Opener) PauseFor(ctx context.Context, kind string, show func(Status)) error {
	return o.policy("Could not pause", show, func() (Status, error) { return o.Policy.Pause(ctx, kind) })
}

// Resume ends a pause from the tray, and the Paused mode.
func (o *Opener) Resume(ctx context.Context, show func(Status)) error {
	return o.policy("Could not resume", show, func() (Status, error) { return o.Policy.Resume(ctx) })
}

func (o *Opener) policy(failTitle string, show func(Status), do func() (Status, error)) error {
	if o.Policy == nil {
		o.notify(failTitle, "This service cannot change the mode. Open the console and go to Machines.")
		return fmt.Errorf("no policy setter")
	}
	st, err := do()
	if err != nil {
		o.notify(failTitle, err.Error())
		return err
	}
	if show != nil {
		show(st)
	}
	return nil
}
