//go:build !windows

package cli

import (
	"os"
	"testing"
)

func assertOwnerOnly(t *testing.T, path string) {
	t.Helper()
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm()&0o077 != 0 {
		t.Errorf("the file with a one-time code must be owner only, is %v", st.Mode())
	}
}

func TestWriteOwnerOnlyFixesAnOpenFile(t *testing.T) {
	path := t.TempDir() + "/f.sh"
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil { //nolint:gosec // the test makes a wide file on purpose
		t.Fatal(err)
	}
	if err := writeOwnerOnly(path, []byte("new"), true); err != nil {
		t.Fatal(err)
	}
	assertOwnerOnly(t, path)
	if b, _ := os.ReadFile(path); string(b) != "new" {
		t.Errorf("content %q", b)
	}
}
