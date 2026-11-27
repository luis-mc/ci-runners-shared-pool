//go:build !windows

package update

import (
	"context"
	"errors"
)

// StartMSIDetached exists on Windows only.
func StartMSIDetached(context.Context, string, string) (int, error) {
	return 0, errors.New("an MSI installs on Windows only")
}

// InstallerBusy is never true off Windows: there is no Windows Installer.
func InstallerBusy() (bool, string) { return false, "" }
