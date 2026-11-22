// Package uninstall is the remove step of the crsp lifecycle (OPS69, lifecycle spec LC-14, LC-15).
//
// Remove asks every time and has no default: a silent remove keeps data; PURGE=1 or --delete-data
// deletes it. Keeping or deleting, remove unregisters crsp's runners from GitHub, stops the services,
// and leaves the program and service to the installer. Deleting also removes everything crsp made:
// settings, database, logs, caches, runner images, containers and the crsp WSL distro.
// Every remove is audited with its actor and result.
package uninstall

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

// Exit codes of `crsp lifecycle-window remove` (built by TRAY12): the window answers with its exit code.
const (
	WindowExitKeep   = 10 // Keep my data
	WindowExitDelete = 11 // Delete my data too
	WindowExitCancel = 1  // Cancel
)

// Choice is what the owner chose for the data.
type Choice int

const (
	ChoiceNone   Choice = iota // nothing chosen yet
	ChoiceKeep                 // Keep my data
	ChoiceDelete               // Delete my data too
	ChoiceCancel               // Cancel: nothing changes
)

// ErrCancelled says the owner cancelled the remove.
var ErrCancelled = errors.New("remove cancelled")

// ChoiceFromWindowExit maps the exit code of the native window to a choice. Any other code is an error: the
// window did not answer, so nothing is guessed.
func ChoiceFromWindowExit(code int) (Choice, error) {
	switch code {
	case WindowExitKeep:
		return ChoiceKeep, nil
	case WindowExitDelete:
		return ChoiceDelete, nil
	case WindowExitCancel:
		return ChoiceCancel, nil
	}
	return ChoiceNone, fmt.Errorf("the remove window ended with code %d, which is not an answer", code)
}

// PurgeFromEnv reports whether PURGE=1 is set (the MSI passes PURGE through).
func PurgeFromEnv(getenv func(string) string) bool {
	switch strings.ToLower(strings.TrimSpace(getenv("PURGE"))) {
	case "1", "true", "yes":
		return true
	}
	return false
}

// Options says what to remove.
type Options struct {
	// DeleteData deletes everything crsp made. False keeps the data.
	DeleteData bool
	// Actor is who asked for the remove, written to the audit row.
	Actor string
	// Now is the clock; nil takes time.Now.
	Now func() time.Time
}

// Record is one audit row of a remove.
type Record struct {
	At         time.Time `json:"at"`
	Actor      string    `json:"actor"`
	Action     string    `json:"action"` // always "remove"
	DeleteData bool      `json:"delete_data"`
	Result     string    `json:"result"` // started, removed, removed_with_warnings or failed
	Detail     string    `json:"detail,omitempty"`
}

// Result values of a remove.
const (
	ResultStarted  = "started"
	ResultRemoved  = "removed"
	ResultWarnings = "removed_with_warnings"
	ResultFailed   = "failed"
)

// Steps are the effects of a remove. The host implements them; tests record them.
type Steps interface {
	// UnregisterRunners removes every runner crsp registered from GitHub. It returns how many.
	UnregisterRunners(ctx context.Context) (int, error)
	// StopServices stops the crsp services so no file is in use.
	StopServices(ctx context.Context) error
	// RemoveContainers removes the containers crsp made.
	RemoveContainers(ctx context.Context) (int, error)
	// RemoveImages removes the runner images crsp made.
	RemoveImages(ctx context.Context) (int, error)
	// UnregisterWSL removes the crsp WSL distro (Windows; elsewhere it does nothing).
	UnregisterWSL(ctx context.Context) error
	// DeleteData deletes settings, database, logs and caches: the data folders.
	DeleteData(ctx context.Context) error
	// Audit writes one audit row. A failed write is an error.
	Audit(ctx context.Context, r Record) error
}

// Summary is what a remove did.
type Summary struct {
	DeleteData     bool
	RunnersRemoved int
	Containers     int
	Images         int
	Warnings       []string
}

// Run removes crsp as o says. The first audit row is written before anything changes and must succeed: the
// audit log fails closed. A step that fails after that is a warning, so one stuck runner never leaves a
// half-removed machine with data still there; deleting the data folders is the one step that must succeed
// when it is asked for. The result row is written before the data folders go, because the audit file may
// live in them (the system log keeps the row either way).
func Run(ctx context.Context, o Options, s Steps, log func(string)) (Summary, error) {
	if log == nil {
		log = func(string) {}
	}
	now := o.Now
	if now == nil {
		now = time.Now
	}
	actor := o.Actor
	if actor == "" {
		actor = "unknown"
	}
	row := func(result, detail string) Record {
		return Record{At: now().UTC(), Actor: actor, Action: "remove", DeleteData: o.DeleteData, Result: result, Detail: detail}
	}
	sum := Summary{DeleteData: o.DeleteData}
	if err := s.Audit(ctx, row(ResultStarted, "")); err != nil {
		return sum, fmt.Errorf("the audit log did not take the remove, so nothing was removed: %w", err)
	}
	warn := func(step string, err error) {
		sum.Warnings = append(sum.Warnings, step+": "+err.Error())
		log("Could not " + step + ": " + err.Error())
	}

	log("Unregistering the runners from GitHub.")
	n, err := s.UnregisterRunners(ctx)
	sum.RunnersRemoved = n
	if err != nil {
		warn("unregister the runners from GitHub", err)
	}
	log("Stopping the crsp services.")
	if err := s.StopServices(ctx); err != nil {
		warn("stop the crsp services", err)
	}
	if o.DeleteData {
		log("Removing the containers.")
		if sum.Containers, err = s.RemoveContainers(ctx); err != nil {
			warn("remove the containers", err)
		}
		log("Removing the runner images.")
		if sum.Images, err = s.RemoveImages(ctx); err != nil {
			warn("remove the runner images", err)
		}
		log("Removing the crsp WSL distro.")
		if err := s.UnregisterWSL(ctx); err != nil {
			warn("remove the crsp WSL distro", err)
		}
	}
	result := ResultRemoved
	if len(sum.Warnings) > 0 {
		result = ResultWarnings
	}
	detail := fmt.Sprintf("runners unregistered: %d", sum.RunnersRemoved)
	if len(sum.Warnings) > 0 {
		detail += "; " + strings.Join(sum.Warnings, "; ")
	}
	if err := s.Audit(ctx, row(result, detail)); err != nil {
		warn("write the audit row", err)
	}
	if o.DeleteData {
		log("Deleting settings, database, logs and caches.")
		if err := s.DeleteData(ctx); err != nil {
			_ = s.Audit(ctx, row(ResultFailed, "delete the data folders: "+err.Error()))
			return sum, fmt.Errorf("could not delete the data: %w", err)
		}
	}
	return sum, nil
}

// Ask finds out what the owner wants for the data, in this order: --delete-data or PURGE=1 is a delete;
// --keep-data is a keep; a silent run (nobody to ask) keeps; otherwise the native window asks (window returns
// its exit code), and when no window can be shown ask puts the same two choices in the terminal, with no
// default. Cancel returns ErrCancelled.
func Ask(deleteFlag, keepFlag, purgeEnv, interactive bool, window func() (int, error), terminal func() (Choice, error)) (Choice, error) {
	switch {
	case deleteFlag || purgeEnv:
		return ChoiceDelete, nil
	case keepFlag:
		return ChoiceKeep, nil
	case !interactive:
		return ChoiceKeep, nil // a silent remove keeps data
	}
	if window != nil {
		code, err := window()
		if err == nil {
			c, cerr := ChoiceFromWindowExit(code)
			if cerr != nil {
				return ChoiceNone, cerr
			}
			if c == ChoiceCancel {
				return c, ErrCancelled
			}
			return c, nil
		}
		// The window could not start (no desktop, an older install): fall through to the terminal.
	}
	if terminal == nil {
		return ChoiceNone, errors.New("nobody could be asked whether to keep or delete the data; use --keep-data or --delete-data")
	}
	c, err := terminal()
	if err != nil {
		return ChoiceNone, err
	}
	if c == ChoiceCancel {
		return c, ErrCancelled
	}
	return c, nil
}

// SafeDataDir refuses a folder that is empty, a drive or file-system root, or too short to be crsp's own:
// deleting the data must never take a parent folder with it.
func SafeDataDir(dir string) error {
	d := strings.TrimRight(strings.ReplaceAll(dir, `\`, "/"), "/")
	if d == "" {
		return errors.New("the data folder is not set")
	}
	parts := 0
	for _, p := range strings.Split(d, "/") {
		if p != "" && !strings.HasSuffix(p, ":") {
			parts++
		}
	}
	if parts < 2 {
		return fmt.Errorf("%q is too close to the root to be crsp's data folder", dir)
	}
	base := d[strings.LastIndex(d, "/")+1:]
	if !strings.Contains(strings.ToLower(base), "crsp") {
		return fmt.Errorf("%q is not a crsp folder", dir)
	}
	return nil
}

// removeAll is os.RemoveAll behind SafeDataDir; a folder that is not there is not an error.
func removeAll(dir string) error {
	if err := SafeDataDir(dir); err != nil {
		return err
	}
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return nil
	}
	return os.RemoveAll(dir)
}
