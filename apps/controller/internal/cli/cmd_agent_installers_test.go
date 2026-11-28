package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// operations R5.132 (CRSP347): the folder of a release goes in, SHA256SUMS first, and a bad file is named.
func TestAgentInstallersImportSendsSumsFirstAndNamesRefusals(t *testing.T) {
	f := newFixture(t)
	dir := t.TempDir()
	for name, body := range map[string]string{"SHA256SUMS": "sums", "CrspAgent-1.0.0-linux-x64.tar.gz": "lin", "CrspAgent-1.0.0-win-x64.msi": "bad", "notes.txt": "skip"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var order []string
	put := func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		name := strings.TrimPrefix(r.URL.Path, "/api/v1/agent-installers/")
		order = append(order, name+"="+string(b))
		w.Header().Set("Content-Type", "application/json")
		if string(b) == "bad" {
			w.WriteHeader(400)
			_, _ = w.Write([]byte(`{"error":{"code":"bad_request","message":"the file does not match the SHA-256 in SHA256SUMS; nothing was kept"}}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"version": "1.0.0", "have_sums": true, "from_release": false, "installers": []map[string]any{
			{"name": "CrspAgent-1.0.0-linux-x64.tar.gz", "os": "linux", "arch": "amd64", "format": "tar.gz", "sha256": "x", "stored": true}}})
	}
	for _, n := range []string{"SHA256SUMS", "CrspAgent-1.0.0-linux-x64.tar.gz", "CrspAgent-1.0.0-win-x64.msi"} {
		f.handle("PUT /api/v1/agent-installers/"+n, put)
	}
	code, out, errOut := f.run("machine", "agent-installers", "import", dir)
	if code == 0 {
		t.Fatalf("a refused file must fail the command: %q %q", out, errOut)
	}
	if len(order) != 3 || order[0] != "SHA256SUMS=sums" {
		t.Fatalf("order = %v", order)
	}
	for _, want := range []string{"Stored SHA256SUMS", "Not stored: CrspAgent-1.0.0-win-x64.msi", "nothing was kept", "1 of 3"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("stderr lacks %q: %q", want, errOut)
		}
	}
	if !strings.Contains(out, "CrspAgent-1.0.0-linux-x64.tar.gz") || !strings.Contains(out, "stored") {
		t.Errorf("table: %q", out)
	}
}

func TestAgentInstallersListAndUsage(t *testing.T) {
	f := newFixture(t)
	f.handle("GET /api/v1/agent-installers", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"version":"1.0.0","have_sums":false,"from_release":false,"installers":[]}`))
	})
	code, out, errOut := f.run("machine", "agent-installers", "list")
	if code != 0 || out != "" || !strings.Contains(errOut, "no SHA256SUMS yet") {
		t.Fatalf("%d %q %q", code, out, errOut)
	}
	if code, _, errOut := f.run("machine", "agent-installers", "import"); code != ExitUsage || !strings.Contains(errOut, "Usage") {
		t.Fatalf("no files: %d %q", code, errOut)
	}
	if code, _, _ := f.run("machine", "agent-installers", "import", filepath.Join(t.TempDir(), "nope")); code != ExitNotFound {
		t.Fatalf("missing file: %d", code)
	}
}
