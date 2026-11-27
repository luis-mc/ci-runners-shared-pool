package cli

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// machines R5.65: `crsp machine join-file` saves the installer the controller made.
func TestMachineJoinFileSavesTheFile(t *testing.T) {
	f := newFixture(t)
	var req map[string]any
	f.handle("POST /api/v1/join-files", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", `attachment; filename="crsp-join-linux-until-20261114T1215Z.sh"`)
		w.Header().Set("X-Join-Code-Id", "jc_1")
		w.WriteHeader(201)
		_, _ = w.Write([]byte("#!/bin/sh\necho hi\n"))
	})
	dir := t.TempDir()
	t.Chdir(dir)
	code, out, errOut := f.run("machine", "join-file", "--os", "linux", "--expires", "20m")
	if code != 0 || req["os"] != "linux" || req["ttl_minutes"] != float64(20) {
		t.Fatalf("%d %v %q %q", code, req, out, errOut)
	}
	name := "crsp-join-linux-until-20261114T1215Z.sh"
	if strings.TrimSpace(out) != name || !strings.Contains(errOut, "works once") {
		t.Fatalf("out %q err %q", out, errOut)
	}
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil || string(b) != "#!/bin/sh\necho hi\n" {
		t.Fatalf("file %q %v", b, err)
	}
	// Owner only on every system (POSIX mode 0600/0700, a protected DACL on Windows).
	assertOwnerOnly(t, filepath.Join(dir, name))
	if !strings.Contains(errOut, "Delete the file") {
		t.Errorf("the CLI must say to delete the file: %q", errOut)
	}
	// --out names the place; a hostile name in the answer never decides a folder.
	f.handle("POST /api/v1/join-files", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Disposition", `attachment; filename="../../evil.sh"`)
		w.WriteHeader(201)
		_, _ = w.Write([]byte("x"))
	})
	if code, _, _ := f.run("machine", "join-file", "--os", "macos"); code != 0 {
		t.Fatal("hostile name run failed")
	}
	if _, err := os.Stat(filepath.Join(dir, "crsp-join-macos.command")); err != nil {
		t.Errorf("fallback name not used: %v", err)
	}
	to := filepath.Join(dir, "mine.cmd")
	if code, _, _ := f.run("machine", "join-file", "--os", "windows", "--out", to); code != 0 {
		t.Fatal("--out failed")
	}
	if _, err := os.Stat(to); err != nil {
		t.Errorf("--out ignored: %v", err)
	}
}

func TestMachineJoinFileUsageAndErrors(t *testing.T) {
	f := newFixture(t)
	if code, _, _ := f.run("machine", "join-file"); code != ExitUsage {
		t.Errorf("no --os: %d", code)
	}
	if code, _, _ := f.run("machine", "join-file", "--os", "plan9"); code != ExitUsage {
		t.Errorf("bad --os: %d", code)
	}
	if code, _, _ := f.run("machine", "join-file", "--os", "linux", "--expires", "48h"); code != ExitUsage {
		t.Errorf("48h: %d", code)
	}
	f.reply("POST /api/v1/join-files", 501, map[string]any{"error": map[string]any{"code": "not_implemented", "message": "copy the command instead"}})
	code, _, e := f.run("machine", "join-file", "--os", "linux")
	if code == 0 || !strings.Contains(e, "copy the command") {
		t.Errorf("%d %q", code, e)
	}
}
