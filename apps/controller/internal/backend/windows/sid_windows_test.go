//go:build windows

package windows

import (
	"os/user"
	"strings"
	"testing"
)

func TestNativeLookupSIDCurrentUser(t *testing.T) {
	u, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	name := u.Username
	if i := strings.LastIndex(name, `\`); i >= 0 {
		name = name[i+1:]
	}
	got, err := nativeLookupSID(name)
	if err != nil {
		t.Fatalf("lookup %q: %v", name, err)
	}
	if got != u.Uid {
		t.Errorf("SID = %q, want %q", got, u.Uid)
	}
	if _, err := nativeLookupSID("crsp-no-such-account-xyz"); err == nil {
		t.Error("unknown account must fail")
	}
}
