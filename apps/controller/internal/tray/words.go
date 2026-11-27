package tray

import "strings"

// The lifecycle words table (dev-standards design-system/lifecycle/words.json, commit 89711a43).
// Every word of the update window and the tray notices comes from here. {version}, {previous},
// {time}, {product}, {size} and {path} are filled in by Fill. Banned: OK, Yes, No, Install now,
// Later, daemon, binary, payload (LC-2, LC-31).
const (
	WordCheck             = "Look for updates"
	WordAvailableTitle    = "Version {version} is ready."
	WordAvailablePrimary  = "Update to version {version}"
	WordNotNow            = "Not now"
	WordDownloading       = "Downloading"
	WordVerifying         = "Checking the download"
	WordInstalling        = "Installing"
	WordTesting           = "Testing"
	WordUpdatedTitle      = "Updated to version {version}."
	WordWhatsNew          = "What's new"
	WordRolledBackTitle   = "The update to {version} failed its test. Version {previous} is running again. We try again at {time}."
	WordRefusedTitle      = "The download for {version} did not pass its signature check. Nothing changed."
	WordRemoveTitle       = "Remove {product}"
	WordRemoveKeep        = "Keep my data"
	WordRemovePurge       = "Delete my data too ({size} GB in {path})"
	WordRemoveCancel      = "Cancel"
	WordRemoveConfirm     = "Remove {product}"
	WordRemoveDisabledTil = "Choose what happens to your data first."
)

// Words the table does not hold yet: the states waiting, rolling back and skipped, the closing
// buttons, and the rolled back sentence without a retry time. They are proposed to the shared
// table; until it has them they live here, in one place, so the window and the tests share them.
const (
	WordWaiting         = "Waiting for running jobs"
	WordNoJobStopped    = "Jobs are never stopped by an update."
	WordRollingBack     = "Putting version {previous} back"
	WordRolledBackFinal = "The update to {version} failed its test. Version {previous} is running again. We try again with a newer version."
	WordSkippedTitle    = "Skipped version {version}. We wait for a newer version."
	WordHide            = "Hide"
	WordClose           = "Close"
	WordCancelUpdate    = "Cancel update"
	WordUpToDate        = "Up to date"
	WordRestarting      = "Restarting the service"
	WordCancelledTitle  = "Update cancelled"
	WordFailedTitle     = "The update failed"
	WordStarted         = "Starting the update."
)

// Fill puts the values into a table string. pairs are name, value, name, value ...
func Fill(s string, pairs ...string) string {
	for i := 0; i+1 < len(pairs); i += 2 {
		s = strings.ReplaceAll(s, "{"+pairs[i]+"}", pairs[i+1])
	}
	return s
}
