package update

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// StageFile is the live stage of an update, beside result.json. The controller
// and the install helper write it; the API shows it, so the console page that
// the tray opens can follow an update on every OS (TRAY4).
const StageFile = "stage.json"

// Stage names. They are the update states plus StageDone.
const (
	StageDownloading    = "downloading"
	StageWaitingForJobs = "waiting_for_jobs"
	StageInstalling     = "installing"
	// StageWaitingInstaller: another installer holds Windows Installer; the update starts when it ends (TRAY11).
	StageWaitingInstaller = "waiting_installer"
	StageRestarting       = "restarting"
	StageDone             = "done"
	StageFailed           = "failed"
)

// Stage is where the running update stands. Percent is 0 to 100 and is only
// meaningful for downloading and installing; -1 says "no number".
type Stage struct {
	Name    string `json:"stage"`
	Percent int    `json:"percent"`
	Version string `json:"version,omitempty"` // the version being installed (done: the version that runs)
	Error   string `json:"error,omitempty"`   // failed: what went wrong
	LogPath string `json:"log_path,omitempty"`
	// WaitingFor names the other installer (waiting_installer only); empty when it is not known.
	WaitingFor string `json:"waiting_for,omitempty"`
	// FailedAt is the stage that failed (failed only), so the page can mark the step.
	FailedAt string `json:"failed_at,omitempty"`
	// StartedBy says who started this update: auto (nobody clicked), tray (the update window
	// of the tray) or console (the console page or the command line). The new tray reads it
	// after the restart: a window started it, so the window says Done; nobody did, so one toast does.
	StartedBy string `json:"started_by,omitempty"`
	// Attempt is the id of the update or restart this stage belongs to, and AttemptKind and
	// AttemptStarted say what it is and when the yes was given (CRSP205).
	Attempt        string    `json:"attempt,omitempty"`
	AttemptKind    string    `json:"attempt_kind,omitempty"`
	AttemptStarted time.Time `json:"attempt_started,omitempty"`
	At             time.Time `json:"at"`
}

// Who started an update (Stage.StartedBy).
const (
	StartedByAuto    = "auto"
	StartedByTray    = "tray"
	StartedByConsole = "console"
)

// StartedByActor maps the actor of an apply request to Stage.StartedBy.
func StartedByActor(actor string) string {
	switch {
	case autoActor(actor):
		return StartedByAuto
	case strings.HasPrefix(actor, "tray:"):
		return StartedByTray
	}
	return StartedByConsole
}

// ClearStage removes the stage file: a cancelled update leaves nothing to show.
func ClearStage(dir string) { _ = os.Remove(filepath.Join(dir, StageFile)) }

// WriteStage stores the stage atomically.
func WriteStage(dir string, s Stage) error {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	return writeJSON(filepath.Join(dir, StageFile), s)
}

// ReadStage reads the stage; ok is false when there is none.
func ReadStage(dir string) (Stage, bool) {
	var s Stage
	b, err := os.ReadFile(filepath.Join(dir, StageFile)) //nolint:gosec // work folder
	if err != nil || json.Unmarshal(b, &s) != nil || s.Name == "" {
		return Stage{}, false
	}
	return s, true
}

// ---- download progress

type progressKey struct{}

// WithProgress returns a context whose downloads report (done, total) bytes to fn.
// total is 0 when the size is unknown.
func WithProgress(ctx context.Context, fn func(done, total int64)) context.Context {
	return context.WithValue(ctx, progressKey{}, fn)
}

func reportProgress(ctx context.Context, done, total int64) {
	if fn, ok := ctx.Value(progressKey{}).(func(done, total int64)); ok && fn != nil {
		fn(done, total)
	}
}

// Percent is done of total as 0 to 100; -1 when total is unknown.
func Percent(done, total int64) int {
	if total <= 0 {
		return -1
	}
	if done >= total {
		return 100
	}
	return int(done * 100 / total)
}

// ---- msiexec progress

// msiActions is the standard Windows Installer action sequence for an upgrade,
// in the order the engine runs it. The verbose log has one "Action start" line
// per action; how far down this list the last one is gives a rough percent.
var msiActions = []string{
	"INSTALL", "FindRelatedProducts", "LaunchConditions", "ValidateProductID", "CostInitialize", "FileCost",
	"CostFinalize", "InstallValidate", "InstallInitialize", "ProcessComponents", "UnpublishFeatures",
	"StopServices", "DeleteServices", "RemoveFiles", "RemoveFolders", "CreateFolders", "MoveFiles",
	"InstallFiles", "DuplicateFiles", "WriteRegistryValues", "RegisterProduct", "InstallServices",
	"StartServices", "PublishFeatures", "PublishProduct", "InstallFinalize",
}

var actionStart = regexp.MustCompile(`Action start \d+:\d+:\d+: ([A-Za-z0-9_.]+)\.`)

// MSIPercent reads a rough install percent from a verbose msiexec log: how far
// the last known standard action sits in the sequence. A finished log is 100,
// a log with no known action yet is 0. It is a guess for a progress bar, not a measure.
func MSIPercent(b []byte) int {
	s := decodeLog(b)
	if _, ok := ParseMSILog(b); ok {
		return 100
	}
	best := -1
	for _, m := range actionStart.FindAllStringSubmatch(s, -1) {
		for i, a := range msiActions {
			if strings.EqualFold(a, m[1]) && i > best {
				best = i
			}
		}
	}
	if best < 0 {
		return 0
	}
	p := (best + 1) * 100 / (len(msiActions) + 1)
	if p > 99 {
		p = 99
	}
	return p
}
