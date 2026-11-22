package uninstall

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// UpgradeCode is the UpgradeCode of the crsp MSI (installer/crsp.wxs). Every build has its own ProductCode;
// the UpgradeCode finds it when the Apps list does not.
const UpgradeCode = "{C1D62442-6DB4-4FF0-9F28-A1CE3121B332}"

// MSIHandoff hands the removal of the program, the service and the other entries to Windows Installer, which
// is the only thing that removes an MSI install whole (LC-15: a purge leaves nothing).
type MSIHandoff struct {
	// SystemRoot is %SystemRoot%; msiexec.exe is System32\msiexec.exe under it.
	SystemRoot string
	// ProductCode finds the installed ProductCode ("{GUID}"): the Apps list first, then the UpgradeCode.
	ProductCode func(ctx context.Context) (string, error)
	// Start starts the program detached: it must outlive this process, which msiexec ends (crsp.exe is in
	// the program folder the installer replaces). It does not wait.
	Start func(ctx context.Context, name string, args ...string) error
	// LogFile, when set, is where msiexec writes its log.
	LogFile string
}

// MSIExecArgs is the msiexec command line of the removal: silent, no restart, PURGE=1 for --delete-data and
// PURGE=0 otherwise. A run started here never asks again: the answer is PURGE.
func MSIExecArgs(productCode string, deleteData bool, logFile string) []string {
	purge := "0"
	if deleteData {
		purge = "1"
	}
	args := []string{"/x", productCode, "/qn", "/norestart"}
	if logFile != "" {
		args = append(args, "/l*v", logFile)
	}
	return append(args, "PURGE="+purge)
}

// ValidProductCode reports whether s is a braced GUID, the only thing msiexec /x may be handed.
func ValidProductCode(s string) bool {
	if len(s) != 38 || s[0] != '{' || s[37] != '}' {
		return false
	}
	for i, r := range s[1:37] {
		switch i {
		case 8, 13, 18, 23:
			if r != '-' {
				return false
			}
		default:
			if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
				return false
			}
		}
	}
	return true
}

// Remove starts the Windows Installer uninstall of crsp, detached, and returns once it has started. The
// installer then stops the service and the tray, removes the program, the service, the firewall rule and the
// tray's Run key, and with deleteData also the whole data folder (PURGE=1).
func (h MSIHandoff) Remove(ctx context.Context, deleteData bool) error {
	if h.ProductCode == nil || h.Start == nil {
		return errors.New("the installer hand-off is not set up")
	}
	code, err := h.ProductCode(ctx)
	if err != nil {
		return err
	}
	code = strings.TrimSpace(code)
	if !ValidProductCode(code) {
		return fmt.Errorf("crsp is not in the Apps list; nothing to remove (found %q)", code)
	}
	msiexec := filepath.Join(h.SystemRoot, "System32", "msiexec.exe")
	if h.SystemRoot == "" {
		msiexec = "msiexec.exe"
	}
	if err := h.Start(ctx, msiexec, MSIExecArgs(code, deleteData, h.LogFile)...); err != nil {
		return fmt.Errorf("could not start Windows Installer: %w", err)
	}
	return nil
}
