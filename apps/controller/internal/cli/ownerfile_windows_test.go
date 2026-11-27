//go:build windows

package cli

import (
	"os"
	"path/filepath"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// assertOwnerOnly reads the DACL back: it must be protected, and no allow entry
// may name Everyone, Users or Authenticated Users (or anybody but the current
// user, SYSTEM and Administrators).
func assertOwnerOnly(t *testing.T, path string) {
	t.Helper()
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	ctrl, _, err := sd.Control()
	if err != nil {
		t.Fatal(err)
	}
	if ctrl&windows.SE_DACL_PROTECTED == 0 {
		t.Error("the DACL takes entries from the folder; it must be protected")
	}
	dacl, _, err := sd.DACL()
	if err != nil || dacl == nil {
		t.Fatalf("no DACL: %v", err)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{user.User.Sid.String(): true, "S-1-5-18": true, "S-1-5-32-544": true}
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			t.Fatal(err)
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			continue
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart)).String()
		if !allowed[sid] {
			t.Errorf("the file grants access to %s; only the current user, SYSTEM and Administrators may", sid)
		}
	}
}

func TestWriteOwnerOnlyHidesTheFileFromEveryone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "crsp-join.cmd")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil { //nolint:gosec // an existing wide file is the case
		t.Fatal(err)
	}
	if err := writeOwnerOnly(path, []byte("code"), false); err != nil {
		t.Fatal(err)
	}
	assertOwnerOnly(t, path)
	if b, err := os.ReadFile(path); err != nil || string(b) != "code" {
		t.Errorf("owner cannot read its own file: %q %v", b, err)
	}
}
