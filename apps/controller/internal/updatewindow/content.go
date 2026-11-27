package updatewindow

import (
	"fmt"
	"os"
	"strings"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/addresses"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/tray"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/update"
)

// The words the window shows. The lifecycle words table lives in internal/tray/words.go; this
// file only names the window's own title and passes the table's words on.
const (
	WindowTitle = "Update - CI Runners Shared Pool"
	ProductName = "CI Runners Shared Pool"

	WordNotNow   = tray.WordNotNow
	WordHide     = tray.WordHide
	WordCancel   = tray.WordCancelUpdate
	WordClose    = tray.WordClose
	WordStarted  = tray.WordStarted
	WordUpToDate = tray.WordUpToDate

	// Another installer holds Windows Installer (TRAY11). %s is its name.
	WordWaitingInstaller     = "Waiting for another installer"
	WordWaitingInstallerBody = "%s is installing. Windows installs one program at a time; this update starts when it finishes."
	WordAnotherProgram       = "Another program"
)

// releaseURL is the release notes page of a version (What's new).
func releaseURL(version string) string {
	if version == "" {
		return ""
	}
	// The address comes from the settings (internal/addresses), never a literal.
	a, err := addresses.Load(os.Getenv)
	if err != nil {
		a = addresses.Defaults()
	}
	return a.RepoPage(update.Repo + "/releases/tag/v" + strings.TrimPrefix(version, "v"))
}

func sprintf(format string, a ...any) string { return fmt.Sprintf(format, a...) }
