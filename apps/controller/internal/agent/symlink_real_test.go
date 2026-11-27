package agent

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestProbeSymlinkReal runs the real ProbeSymlink with a real link (doctor
// R5.42, runners R5.68). The unit tests fake makeSymlink, so this is the one
// place a real link is made. CI runs it on Windows with Developer Mode on.
func TestProbeSymlinkReal(t *testing.T) {
	if os.Getenv("CRSP_REAL_SYMLINK") != "1" {
		t.Skip("CRSP_REAL_SYMLINK=1 not set: the real link check runs in the Windows CI step only")
	}
	if runtime.GOOS != "windows" {
		t.Skip("the real link check needs Windows")
	}
	tmp := t.TempDir()
	t.Setenv("TMP", tmp)
	t.Setenv("TEMP", tmp)
	r := ProbeSymlink(context.Background(), OSSys{})
	if !r.GetKnown() || r.GetError() != "" {
		t.Fatalf("probe did not read: %+v", r)
	}
	if !r.GetCanLink() {
		t.Fatalf("a real link was refused with Developer Mode on: %+v", r)
	}
	if !r.GetDeveloperMode() {
		t.Fatalf("Developer Mode not read as on: %+v", r)
	}
	left, err := filepath.Glob(filepath.Join(tmp, "crsp-symlink-*"))
	if err != nil || len(left) != 0 {
		t.Fatalf("temporary folder left behind: %v %v", left, err)
	}
}
