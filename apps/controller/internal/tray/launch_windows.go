//go:build windows

package tray

import (
	"errors"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// StartForSignedInUsers starts `crsp tray --updated` in the desktop session of each
// signed-in user (CRSP147). The updater runs as SYSTEM in session 0, which has no
// desktop and shows no tray, so the program starts with the user's own token (not
// elevated, never SYSTEM) in the user's session. It returns how many trays started.
// A tray that already runs in that session leaves at once (single instance).
func StartForSignedInUsers(exe string) (int, error) {
	var info *windows.WTS_SESSION_INFO
	var count uint32
	if err := windows.WTSEnumerateSessions(0, 0, 1, &info, &count); err != nil {
		return 0, fmt.Errorf("list sessions: %w", err)
	}
	defer windows.WTSFreeMemory(uintptr(unsafe.Pointer(info)))
	sessions := unsafe.Slice(info, count)

	started := 0
	var errs []error
	for _, s := range sessions {
		if s.State != windows.WTSActive || s.SessionID == 0 {
			continue
		}
		if err := startInSession(exe, s.SessionID); err != nil {
			errs = append(errs, fmt.Errorf("session %d: %w", s.SessionID, err))
			continue
		}
		started++
	}
	return started, errors.Join(errs...)
}

func startInSession(exe string, session uint32) error {
	var tok windows.Token
	if err := windows.WTSQueryUserToken(session, &tok); err != nil {
		return fmt.Errorf("user token: %w", err)
	}
	defer func() { _ = tok.Close() }()

	// An administrator's token may be the elevated half of a split token: use the limited one.
	use := tok
	var elevType uint32
	var n uint32
	if err := windows.GetTokenInformation(tok, windows.TokenElevationType, (*byte)(unsafe.Pointer(&elevType)), 4, &n); err == nil && elevType == 2 { // TokenElevationTypeFull
		var linked struct{ Token windows.Token }
		if err := windows.GetTokenInformation(tok, windows.TokenLinkedToken, (*byte)(unsafe.Pointer(&linked)), uint32(unsafe.Sizeof(linked)), &n); err == nil {
			defer func() { _ = linked.Token.Close() }()
			var primary windows.Token
			if err := windows.DuplicateTokenEx(linked.Token, windows.MAXIMUM_ALLOWED, nil, windows.SecurityImpersonation, windows.TokenPrimary, &primary); err == nil {
				defer func() { _ = primary.Close() }()
				use = primary
			}
		}
	}

	var block *uint16
	if err := windows.CreateEnvironmentBlock(&block, use, false); err != nil {
		return fmt.Errorf("environment: %w", err)
	}
	defer func() { _ = windows.DestroyEnvironmentBlock(block) }()

	cmdline, err := windows.UTF16PtrFromString(`"` + exe + `" tray --updated`)
	if err != nil {
		return err
	}
	desktop, err := windows.UTF16PtrFromString(`winsta0\default`)
	if err != nil {
		return err
	}
	si := windows.StartupInfo{Cb: uint32(unsafe.Sizeof(windows.StartupInfo{})), Desktop: desktop}
	var pi windows.ProcessInformation
	flags := uint32(windows.CREATE_UNICODE_ENVIRONMENT | windows.CREATE_NEW_PROCESS_GROUP | windows.DETACHED_PROCESS)
	if err := windows.CreateProcessAsUser(use, nil, cmdline, nil, nil, false, flags, block, nil, &si, &pi); err != nil {
		return fmt.Errorf("start: %w", err)
	}
	_ = windows.CloseHandle(pi.Thread)
	_ = windows.CloseHandle(pi.Process)
	return nil
}
