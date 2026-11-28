// Package lifecyclewords holds the lifecycle strings the words lint (scripts/design/wordslint) scans.
// Every value comes from the words table of docs/design-system/next-version/lifecycle.json (LC-2, LC-31).
// The tray, the update window and the console read these; they write no lifecycle string of their own.
package lifecyclewords

const (
	MenuCheck                  = "Look for updates"
	AvailableTitle             = "Version {version} is ready."
	AvailablePrimary           = "Update to version {version}"
	AvailableSecondary         = "Not now"
	AvailableRequired          = "Version {version} is required and installs now."
	DownloadingStep            = "Downloading"
	VerifyingStep              = "Checking the download"
	InstallingStep             = "Installing"
	TestingStep                = "Testing"
	UpdatedTitle               = "Updated to version {version}."
	UpdatedAction              = "What's new"
	UpdatedNotes               = "What is new in version {version}"
	RolledBackTitle            = "The update to {version} failed its test. Version {previous} is running again. We try again at {time}."
	RefusedTitle               = "The download for {version} did not pass its signature check. Nothing changed."
	RemoveTitle                = "Remove {product}"
	RemoveKeep                 = "Keep my data"
	RemovePurge                = "Delete my data too ({size} GB in {path})"
	RemoveCancel               = "Cancel"
	RemoveConfirm              = "Remove {product}"
	RemoveConfirmDisabledUntil = "Choose what happens to your data first."
	UpToDateTitle              = "Crsp is up to date. Version {version}."
	WaitingTitle               = "Waiting for {n} running jobs to finish. The update starts when they end."
	SkippedTitle               = "Version {version} is skipped. We look again when a newer version is out."
	UnpairedTitle              = "Pair this machine first. Crsp looks for updates after that."
)
