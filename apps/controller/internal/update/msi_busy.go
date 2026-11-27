package update

import (
	"regexp"
	"strings"
	"time"
)

// A busy Windows Installer (TRAY11). Windows runs one install at a time. While another
// installer runs, msiexec exits with 1618 (ERROR_INSTALL_ALREADY_RUNNING) or waits on the
// Global\_MSIExecute mutex. The update then waits and retries; it is never a failure.

// MSIBusyCode is the exit code of msiexec when another install is running.
const MSIBusyCode = 1618

// MSIBusyRetry is how often the update looks again while another installer runs.
const MSIBusyRetry = 15 * time.Second

const (
	msiExitGrace    = 10 * time.Second // msiexec is gone and the log has no end line: wait this long for the log to settle
	msiRestartGrace = 2 * time.Minute  // the installer said done but the old service still runs: the upgrade did not replace it
)

// MSIBusyExit says an msiexec exit code means "another installation is in progress".
func MSIBusyExit(code int) bool { return code == MSIBusyCode }

// MSILogBusy says the msiexec log reports that another install holds Windows Installer.
func MSILogBusy(b []byte) bool {
	if code, ok := ParseMSILog(b); ok && MSIBusyExit(code) {
		return true
	}
	return strings.Contains(strings.ToLower(decodeLog(b)), "another installation is in progress")
}

var msiFile = regexp.MustCompile(`(?i)([^\s"\\/]+)\.msi\b`)

// InstallerName finds the name of the install in the command lines of the running msiexec
// processes: the .msi file name without its folder and extension. "" when none names a file.
func InstallerName(cmdlines []string) string {
	for _, c := range cmdlines {
		if m := msiFile.FindStringSubmatch(c); m != nil {
			return m[1]
		}
	}
	return ""
}
