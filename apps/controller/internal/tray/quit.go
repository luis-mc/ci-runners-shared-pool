package tray

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// QuitGrace is how long `crsp tray --quit` waits for the trays to end on their
// own before it ends the ones left (operations R5.86).
const QuitGrace = 5 * time.Second

// quitSignals are the signals that ask the tray to quit (the OS "close" request
// on Unix; Ctrl+C, Ctrl+Break and logoff on Windows).
func quitSignals() []os.Signal { return []os.Signal{os.Interrupt, syscall.SIGTERM} }

// WatchQuit calls quit once when the process is asked to quit, or returns when
// ctx ends. The tray ends its loop on quit, so the process exits and releases
// its executable (operations R5.86).
func WatchQuit(ctx context.Context, quit func()) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, quitSignals()...)
	go func() {
		defer signal.Stop(ch)
		select {
		case <-ch:
			quit()
		case <-ctx.Done():
		}
	}()
}

// procLister lists the tray processes of this install and asks them to quit.
// The Windows and Unix versions are in quit_windows.go and quit_other.go.
type procLister interface {
	// Trays returns the process ids of the trays (not this process) that run
	// the same executable folder.
	Trays(dir string) ([]int, error)
	// Ask asks one tray to quit, the way its OS does (WM_CLOSE, SIGTERM).
	Ask(pid int) error
	// Alive says whether the process still runs.
	Alive(pid int) bool
	// Kill ends one process at once.
	Kill(pid int) error
}

// QuitAll asks every running tray of this install to quit, waits up to grace
// for them to end, and then ends the ones left. It returns how many trays it
// found and how many it had to end by force.
func QuitAll(ctx context.Context, p procLister, dir string, grace time.Duration) (found, forced int, err error) {
	pids, err := p.Trays(dir)
	if err != nil {
		return 0, 0, err
	}
	for _, pid := range pids {
		_ = p.Ask(pid) // a tray that cannot be asked is ended below
	}
	end := time.Now().Add(grace)
	for time.Now().Before(end) && anyAlive(p, pids) {
		select {
		case <-ctx.Done():
			return len(pids), 0, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	for _, pid := range pids {
		if p.Alive(pid) {
			if kerr := p.Kill(pid); kerr != nil && err == nil {
				err = kerr
			}
			forced++
		}
	}
	return len(pids), forced, err
}

func anyAlive(p procLister, pids []int) bool {
	for _, pid := range pids {
		if p.Alive(pid) {
			return true
		}
	}
	return false
}
