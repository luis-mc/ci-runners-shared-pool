//go:build windows

package cli

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

// ownerOnlyDACL is a protected DACL (it takes nothing from the folder) that
// grants full control to the current user and to SYSTEM, and to nobody else.
func ownerOnlyDACL() (*windows.ACL, error) {
	tok := windows.GetCurrentProcessToken()
	user, err := tok.GetTokenUser()
	if err != nil {
		return nil, fmt.Errorf("read the current user: %w", err)
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;" + user.User.Sid.String() + ")(A;;FA;;;SY)")
	if err != nil {
		return nil, fmt.Errorf("build the access list: %w", err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return nil, fmt.Errorf("read the access list: %w", err)
	}
	return dacl, nil
}

// writeOwnerOnly saves data so only the current user (and SYSTEM) can read it
// (machines R5.65). POSIX modes mean nothing on Windows, so the file is created
// empty, given a protected DACL, and only then filled: the code is never in a
// file other users can open. runnable is not needed on Windows.
func writeOwnerOnly(path string, data []byte, _ bool) error {
	dacl, err := ownerOnlyDACL()
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600) //nolint:gosec // the caller's own --out
	if err != nil {
		return err
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return fmt.Errorf("limit the file to its owner: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}
