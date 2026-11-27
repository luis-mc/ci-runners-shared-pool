package tray

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
)

// The update page opens in a small app-style window: a Chromium-family browser
// started with --app, so it has no tabs or address bar (TRAY4). One lookup per
// OS finds the browser; no browser falls back to the default one.

// UpdatePath is the console page that asks for the yes and shows the progress.
const UpdatePath = "/update"

// AppWindowSize is the size of the update window, width,height.
const AppWindowSize = "520,420"

// ErrNoAppBrowser says no Edge, Chrome or Chromium was found on this machine.
var ErrNoAppBrowser = errors.New("no Edge, Chrome or Chromium found")

// BrowserLookup is what the lookup uses from the machine; tests replace it.
type BrowserLookup struct {
	GOOS     string
	LookPath func(name string) (string, error)
	Stat     func(path string) error // nil error: the file exists
	Getenv   func(key string) string
}

// OSBrowserLookup is the lookup of this machine.
func OSBrowserLookup() BrowserLookup {
	return BrowserLookup{
		GOOS:     runtimeGOOS(),
		LookPath: exec.LookPath,
		Stat:     func(p string) error { _, err := os.Stat(p); return err },
		Getenv:   os.Getenv,
	}
}

// Candidates lists, in the order tried, the programs that may be the browser:
// first the fixed install paths of the OS, then the names on the PATH.
func (l BrowserLookup) Candidates() (paths, names []string) {
	env := func(k string) string {
		if l.Getenv == nil {
			return ""
		}
		return l.Getenv(k)
	}
	switch l.GOOS {
	case "windows":
		for _, root := range []string{env("ProgramFiles(x86)"), env("ProgramFiles"), env("LocalAppData")} {
			if root == "" {
				continue
			}
			paths = append(paths,
				filepath.Join(root, "Microsoft", "Edge", "Application", "msedge.exe"),
				filepath.Join(root, "Google", "Chrome", "Application", "chrome.exe"),
				filepath.Join(root, "Chromium", "Application", "chrome.exe"))
		}
		names = []string{"msedge.exe", "chrome.exe"}
	case "darwin":
		apps := []string{"/Applications"}
		if home := env("HOME"); home != "" {
			apps = append(apps, filepath.Join(home, "Applications"))
		}
		for _, dir := range apps {
			paths = append(paths,
				filepath.Join(dir, "Microsoft Edge.app", "Contents", "MacOS", "Microsoft Edge"),
				filepath.Join(dir, "Google Chrome.app", "Contents", "MacOS", "Google Chrome"),
				filepath.Join(dir, "Chromium.app", "Contents", "MacOS", "Chromium"))
		}
	default:
		names = []string{"microsoft-edge", "microsoft-edge-stable", "google-chrome", "google-chrome-stable", "chromium", "chromium-browser", "chrome"}
	}
	return paths, names
}

// Find returns the first browser that exists. It returns ErrNoAppBrowser when none does.
func (l BrowserLookup) Find() (string, error) {
	paths, names := l.Candidates()
	for _, p := range paths {
		if l.Stat != nil && l.Stat(p) == nil {
			return p, nil
		}
	}
	for _, n := range names {
		if l.LookPath == nil {
			break
		}
		if p, err := l.LookPath(n); err == nil && p != "" {
			return p, nil
		}
	}
	return "", ErrNoAppBrowser
}

// AppWindowArgs are the arguments that open url as an app window.
func AppWindowArgs(url string) []string {
	return []string{"--app=" + url, "--window-size=" + AppWindowSize}
}

// AppWindowOpener opens a URL in an app-style window with the browser the lookup
// finds, and in the default browser when it finds none or cannot start it. start
// runs the program detached (nil: the OS start).
type AppWindowOpener struct {
	Lookup   BrowserLookup
	Start    func(path string, args []string) error
	Fallback Launcher
}

// Open opens url. It returns an error only when nothing could be opened.
func (a AppWindowOpener) Open(url string) error {
	if path, err := a.Lookup.Find(); err == nil {
		start := a.Start
		if start == nil {
			start = startDetached
		}
		if err := start(path, AppWindowArgs(url)); err == nil {
			return nil
		}
	}
	if a.Fallback == nil {
		return ErrNoAppBrowser
	}
	return a.Fallback(url)
}

func startDetached(path string, args []string) error {
	// The browser outlives this call, so it takes no cancelable context.
	cmd := exec.CommandContext(context.Background(), path, args...) //nolint:gosec // a browser looked up on this machine, the arguments are ours
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

// ProfileDirName is the folder (under the user's cache folder) of the browser profile that
// only crsp's own windows use (TRAY7 D19). A browser opens a window at the size it is asked
// for only when its profile has no remembered size; an own profile gives that on every start
// and keeps the person's own browser untouched.
const ProfileDirName = "crsp-window-profile"

// ProfileDir is the folder of the own browser profile of this user.
func ProfileDir() string {
	base, err := os.UserCacheDir()
	if err != nil || base == "" {
		base = os.TempDir()
	}
	return filepath.Join(base, "crsp", ProfileDirName)
}

// ProfileWindowArgs are the arguments that open url as an app window of w by h pixels in the
// own browser profile: no first-run page, no default-browser question, no extensions.
func ProfileWindowArgs(url, profile string, w, h int) []string {
	return []string{
		"--app=" + url,
		"--window-size=" + strconv.Itoa(w) + "," + strconv.Itoa(h),
		"--user-data-dir=" + profile,
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-extensions",
	}
}

// OpenProfileWindow opens url in an app window of the own profile. It returns
// ErrNoAppBrowser when no Edge, Chrome or Chromium exists: the caller then keeps the console
// page, since the default browser cannot be asked for a size or a profile.
func OpenProfileWindow(l BrowserLookup, start func(path string, args []string) error, url string, w, h int) error {
	path, err := l.Find()
	if err != nil {
		return err
	}
	if start == nil {
		start = startDetached
	}
	return start(path, ProfileWindowArgs(url, ProfileDir(), w, h))
}
