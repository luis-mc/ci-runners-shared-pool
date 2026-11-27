package cli

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testKeyBody = "-----BEGIN RSA PRIVATE KEY-----\nTESTONLYKEYBODY1234567890\n-----END RSA PRIVATE KEY-----\n"

func TestAppRotateKeySendsOnceAndNeverPrintsIt(t *testing.T) { // github-integration R5.7
	f := newFixture(t)
	var got string
	f.handle("PUT /api/v1/app/private-key", func(w http.ResponseWriter, r *http.Request) {
		var b struct{ Pem string }
		_ = json.NewDecoder(r.Body).Decode(&b)
		got = b.Pem
		w.WriteHeader(204)
	})
	path := filepath.Join(t.TempDir(), "app.private-key.pem")
	if err := os.WriteFile(path, []byte(testKeyBody), 0o600); err != nil {
		t.Fatal(err)
	}
	// No terminal and no --yes: nothing is sent.
	if code, _, _ := f.run("app", "rotate-key", "--file", path); code != ExitNotConfirmed || f.called("PUT") {
		t.Fatalf("%d %v", code, f.calls)
	}
	code, out, errOut := f.run("app", "rotate-key", "--file", path, "--yes")
	if code != 0 || got != testKeyBody {
		t.Fatalf("%d %q", code, errOut)
	}
	if strings.Contains(out+errOut, "TESTONLYKEYBODY") {
		t.Fatal("the key was printed")
	}
}

func TestAppRotateKeyRefusalKeepsExitCodeAndHidesKey(t *testing.T) {
	f := newFixture(t)
	f.reply("PUT /api/v1/app/private-key", 400, apiErr("bad_request", "GitHub does not accept this key. The old key was kept."))
	f.stdin = testKeyBody
	code, out, errOut := f.run("app", "rotate-key", "--file", "-", "--yes")
	if code != ExitFailed || !strings.Contains(errOut, "old key was kept") || strings.Contains(out+errOut, "TESTONLYKEYBODY") {
		t.Fatalf("%d %q", code, errOut)
	}
	if code, _, _ := f.run("app", "rotate-key", "--yes"); code != ExitUsage {
		t.Fatalf("no file: %d", code)
	}
	if code, _, _ := f.run("app", "rotate-key", "--file", filepath.Join(t.TempDir(), "missing.pem"), "--yes"); code != ExitUsage {
		t.Fatalf("missing file: %d", code)
	}
}
