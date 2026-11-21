package updatewindow

import (
	"strings"

	"github.com/luis-mc/ci-runners-shared-pool/internal/tray"
	"github.com/luis-mc/ci-runners-shared-pool/internal/update"
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
)

// releaseURL is the release notes page of a version (What's new).
func releaseURL(version string) string {
	if version == "" {
		return ""
	}
	return "https://github.com/" + update.Repo + "/releases/tag/v" + strings.TrimPrefix(version, "v")
}
