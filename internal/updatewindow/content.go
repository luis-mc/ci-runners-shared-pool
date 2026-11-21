package updatewindow

// The words the window shows (design-system content.md, "Native update window"). Kept in one
// place so the layout check, the screenshots and the tests use the same text.
const (
	WindowTitle = "Update - CI Runners Shared Pool"
	ProductName = "CI Runners Shared Pool"

	WordNotNow   = "Not now"
	WordUpdate   = "Update now"
	WordHide     = "Hide"
	WordCancel   = "Cancel update"
	WordClose    = "Close"
	WordStarted  = "Starting the update."
	WordUpToDate = "Up to date"

	// Another installer holds Windows Installer (TRAY11). %s is its name.
	WordWaitingInstaller     = "Waiting for another installer"
	WordWaitingInstallerBody = "%s is installing. Windows installs one program at a time; this update starts when it finishes."
	WordAnotherProgram       = "Another program"
)
