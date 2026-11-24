//go:build windows

package windows

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

// nativeLookupSID asks Windows for the SID of a local account, in process, so
// no PowerShell host is involved (runners R5.50).
func nativeLookupSID(account string) (string, error) {
	sid, _, _, err := windows.LookupSID("", QualifyAccount(localMachine(), account))
	if err != nil {
		return "", fmt.Errorf("LookupAccountName: %w", err)
	}
	return sid.String(), nil
}

func localMachine() string {
	if m := os.Getenv("COMPUTERNAME"); m != "" {
		return m
	}
	m, _ := os.Hostname()
	return m
}
