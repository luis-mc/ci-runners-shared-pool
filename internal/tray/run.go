package tray

import (
	"context"
	"flag"
	"fmt"
	"github.com/luis-mc/ci-runners-shared-pool/internal/addresses"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"fyne.io/systray"

	"github.com/luis-mc/ci-runners-shared-pool/internal/update"
)

// NeedsRelaunch says whether the service runs a newer stable version than this
// tray: an update installed, so the tray starts the new binary (R5.18).
func NeedsRelaunch(own, service string) bool {
	return service != "" && update.Newer(own, service)
}

// Relaunch starts the tray of the binary now on disk (the new version) and
// returns at once. The new tray waits up to 20 s for this one to release the
// single-instance lock (--wait).
func Relaunch(exe string) error {
	cmd := exec.CommandContext(context.Background(), exe, "tray", "--wait", "20s", "--updated") //nolint:gosec // our own binary
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

// Options configure Run. Zero values use the OS defaults.
type Options struct {
	Version string
	Exe     string
	Stdout  io.Writer
	Stderr  io.Writer
}

// Run is `crsp tray`. It returns the process exit code (spec section 7).
func Run(args []string, o Options) int {
	fs := flag.NewFlagSet("tray", flag.ContinueOnError)
	fs.SetOutput(o.Stderr)
	smoke := fs.Bool("smoke", false, "start the tray loop, exit 0 once the icon is ready (CI)")
	hold := fs.Duration("smoke-hold", 0, "with --smoke: keep running this long after the icon is ready")
	updated := fs.Bool("updated", false, "this tray was started by the old tray after an update: say so in one notice")
	quit := fs.Bool("quit", false, "ask every running tray of this install to quit, wait for them, and exit (the MSI upgrade)")
	wait := fs.Duration("wait", 0, "wait up to this long for the tray of the old version to quit (used after an update)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if o.Exe == "" {
		o.Exe, _ = os.Executable()
	}

	if *quit {
		return runQuit(o)
	}

	lock, ok, err := AcquireLock()
	for end := time.Now().Add(*wait); err == nil && !ok && time.Now().Before(end); {
		time.Sleep(500 * time.Millisecond)
		lock, ok, err = AcquireLock()
	}
	if err != nil {
		_, _ = fmt.Fprintln(o.Stderr, "error:", err)
		return 1
	}
	if !ok {
		return ExitOK // another tray runs in this session (R5.23)
	}
	defer lock.Release()

	switch ProbeHost() {
	case HostNoSession:
		_, _ = fmt.Fprintln(o.Stderr, "No desktop session found. Use `crsp open` to get a sign-in link.")
		return ExitNoSession
	case HostNoTrayArea:
		if p, err := statePath(); err == nil && noticeDue(p, o.Version) {
			DesktopNotice("No tray area", "This desktop shows no tray icons. Use `crsp open` to open the console, or install the AppIndicator extension.")
		}
		return ExitNoTrayHost
	}

	// No modal box after an update (TRAY5 D13): loop says the result once, by a toast or the window.
	return loop(o, *smoke, *hold, *updated)
}

func loop(o Options, smoke bool, hold time.Duration, updated bool) int {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// A quit request (operations R5.86) ends the loop, so the process leaves and frees its exe.
	WatchQuit(ctx, systray.Quit)

	client := NewClient()
	cur := Status{State: StateStarting}
	curCh := make(chan Status, 1)
	current := func() Status {
		select {
		case s := <-curCh:
			cur = s
		default:
		}
		return cur
	}

	auto, _ := DefaultAutostart(o.Exe)
	// A bad CRSP_GITHUB_URL was refused at the service start; the defaults are the safe answer here.
	addrs, err := addresses.Load(os.Getenv)
	if err != nil {
		addrs = addresses.Defaults()
	}
	opener := &Opener{
		RepoURL: addrs.RepoPage(update.Repo),
		Links:   client, Targets: client, Checker: client, Policy: client, Version: o.Version,
		Browser: OSLauncher, Files: OSLauncher,
		Window: AppWindowOpener{Lookup: OSBrowserLookup(), Fallback: OSLauncher}.Open,
		Notify: DesktopNotice, Notices: &Notices{}, Current: current, LogDir: LogDir,
		Native: OSNativeWindow(o.Exe, o.Version),
	}
	if !smoke && !updated {
		go OSFirstStart(o.Exe) // once per user; macOS and Linux (TRAY7, D20)
	}
	if updated && o.Version != "" && !smoke {
		go func() {
			env := AfterEnv{
				Status: client.Status, Toast: OSToast, Version: o.Version, Due: updatedOnceFor,
			}
			if opener.Native != nil {
				env.OpenWindow = func(ctx context.Context) error {
					st := current()
					return opener.Native(ctx, "live", st)
				}
			}
			AfterUpdate(ctx, env)
		}()
	}

	ready := make(chan struct{})
	onReady := func() {
		systray.SetIcon(Icon(StateStarting))
		systray.SetTooltip(cur.Tooltip())
		loginOn := false
		if auto != nil {
			loginOn, _ = auto.Enabled()
		}
		items := BuildMenu(o.Version, cur, loginOn)
		sys := map[string]*systray.MenuItem{}
		for _, it := range items {
			if it.Separator {
				systray.AddSeparator()
				continue
			}
			var mi *systray.MenuItem
			if it.Parent != "" {
				mi = sys[it.Parent].AddSubMenuItemCheckbox(it.Label, "", it.Checked)
			} else if it.Switch {
				mi = systray.AddMenuItemCheckbox(it.Label, "", it.Checked)
			} else {
				mi = systray.AddMenuItem(it.Label, "")
				if !it.Action {
					mi.Disable()
				}
			}
			if it.Hidden {
				mi.Hide()
			}
			sys[it.ID] = mi
		}
		open, check, upd, restart := sys[IDOpen], sys[IDCheck], sys[IDUpdate], sys[IDRestart]
		logs, gh, login, quit := sys[IDLogs], sys[IDGitHub], sys[IDLogin], sys[IDQuit]
		status, doctor, digest := sys[IDStatus], sys[IDDoctor], sys[IDDigest]
		modeAuto, modeAlw := sys[IDModeAuto], sys[IDModeAlw]
		pauseHour, pauseTill, resume := sys[IDPauseHour], sys[IDPauseTill], sys[IDResume]

		var relaunchOnce sync.Once
		offers := &Offered{}
		alerts := &Alerted{}
		applier := &Applier{
			Surf:  traySurface{items: sys},
			Build: func(s Status) []MenuItem { return BuildMenu(o.Version, s, false) },
			Open:  MenuOpen,
		}
		applier.Seed(items, cur)
		go applier.Run(ctx)
		p := &Poller{Source: client, Manager: ServiceState}
		p.OnStatus = func(s Status) {
			select {
			case <-curCh:
			default:
			}
			curCh <- s
			applier.Update(s) // in place, and queued while the menu is open (R5.45)
			// A found update the owner has not been asked about yet: the same Install now / Later dialog.
			if offers.Due(s) {
				go func() { _ = opener.Offer(ctx, s) }()
			}
			// A runner service that did not start: one notification for each, until it runs.
			for _, rp := range alerts.Due(s) {
				DesktopNotice(ProblemTitle, ProblemBody(rp))
			}
			// The service runs a newer version than this tray: the update installed.
			// Start the tray of the new version and quit this one (R5.18, within 30 s).
			if NeedsRelaunch(o.Version, s.Version) {
				relaunchOnce.Do(func() {
					if err := Relaunch(o.Exe); err == nil {
						systray.Quit()
					}
				})
			}
		}
		go p.Run(ctx)
		go func() {
			for {
				select {
				case <-open.ClickedCh:
					go func() { _ = opener.Open(ctx) }()
				case <-check.ClickedCh:
					go func() { _ = opener.LookForUpdates(ctx) }()
				case <-upd.ClickedCh:
					go func() { _ = opener.Offer(ctx, current()) }()
				case <-status.ClickedCh:
					go func() { _ = opener.OpenPage(ctx, JobsPath) }()
				case <-doctor.ClickedCh:
					go func() { _ = opener.OpenPage(ctx, DoctorPath) }()
				case <-digest.ClickedCh:
					go func() { _ = opener.OpenDigest(current().DigestPath) }()
				case <-restart.ClickedCh:
					go func() { _ = opener.OpenReview(ctx, ReviewRestartPath) }()
				case <-modeAuto.ClickedCh:
					go func() { _ = opener.SetMode(ctx, ModeAutomatic, p.OnStatus) }()
				case <-modeAlw.ClickedCh:
					go func() { _ = opener.SetMode(ctx, ModeAlways, p.OnStatus) }()
				case <-pauseHour.ClickedCh:
					go func() { _ = opener.PauseFor(ctx, PauseOneHour, p.OnStatus) }()
				case <-pauseTill.ClickedCh:
					go func() { _ = opener.PauseFor(ctx, PauseUntilResumed, p.OnStatus) }()
				case <-resume.ClickedCh:
					go func() { _ = opener.Resume(ctx, p.OnStatus) }()
				case <-gh.ClickedCh:
					go func() { _ = opener.OpenGitHub() }()
				case <-logs.ClickedCh:
					go func() { _ = opener.OpenLogFolder() }()
				case <-login.ClickedCh:
					if auto == nil {
						continue
					}
					want := !login.Checked()
					if err := auto.Set(want); err != nil {
						DesktopNotice("Could not change Start at login", err.Error())
						continue
					}
					if want {
						login.Check()
					} else {
						login.Uncheck()
					}
				case <-quit.ClickedCh:
					systray.Quit() // closes the tray only (R5.5)
					return
				case <-ctx.Done():
					return
				}
			}
		}()
		close(ready)
	}
	if smoke {
		go func() {
			select {
			case <-ready:
			case <-time.After(15 * time.Second):
				_, _ = fmt.Fprintln(o.Stderr, "smoke: icon not ready in 15 s")
				os.Exit(1)
			}
			time.Sleep(hold)
			systray.Quit()
		}()
	}
	systray.Run(onReady, cancel)
	return ExitOK
}

// runQuit is `crsp tray --quit`: it signals every other tray of this install,
// waits for them, and ends the ones that did not leave (operations R5.86).
func runQuit(o Options) int {
	dir := filepath.Dir(o.Exe)
	found, forced, err := QuitAll(context.Background(), newProcs(), dir, QuitGrace)
	if o.Stdout == nil {
		o.Stdout = io.Discard
	}
	if o.Stderr == nil {
		o.Stderr = io.Discard
	}
	_, _ = fmt.Fprintf(o.Stdout, "tray quit: %d found, %d ended by force\n", found, forced)
	if err != nil {
		_, _ = fmt.Fprintln(o.Stderr, "error:", err)
		return 1
	}
	return ExitOK
}

// updatedOnceFor is true the first time this version's result is due for this user.
func updatedOnceFor(version string) bool { return updatedOnce(version) }

// updatedOnce is true the first time this version's "Updated to" notice is due for this user.
func updatedOnce(version string) bool {
	p, err := statePath()
	if err != nil {
		return true // no state file: say it rather than stay silent
	}
	return updatedDue(p, version)
}

// traySurface is the live systray menu behind the Applier.
type traySurface struct{ items map[string]*systray.MenuItem }

func (t traySurface) SetTitle(id, title string) { t.items[id].SetTitle(title) }
func (t traySurface) SetVisible(id string, v bool) {
	if v {
		t.items[id].Show()
	} else {
		t.items[id].Hide()
	}
}
func (t traySurface) SetChecked(id string, c bool) {
	if c {
		t.items[id].Check()
	} else {
		t.items[id].Uncheck()
	}
}
func (t traySurface) SetTooltip(text string) { systray.SetTooltip(text) }
func (t traySurface) SetIcon(s State)        { systray.SetIcon(Icon(s)) }
