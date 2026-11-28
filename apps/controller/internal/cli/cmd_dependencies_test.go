package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDependenciesListsInWords(t *testing.T) {
	f := newFixture(t)
	f.reply("GET /api/v1/dependencies/store", 200, map[string]any{"items": []any{
		map[string]any{"id": "docker-desktop-windows", "name": "Docker Desktop", "kind": "installer", "state": "missing", "pinned": true, "can_fetch": true, "source": "desktop.docker.com"},
		map[string]any{"id": "runner-linux-x64", "name": "GitHub Actions runner", "kind": "runner", "state": "ready", "origin": "imported", "pinned": true, "can_fetch": true},
		map[string]any{"id": "x", "name": "X", "kind": "installer", "state": "not_pinned", "pinned": false, "can_fetch": false},
	}})
	code, out, _ := f.run("dependencies")
	if code != 0 || !strings.Contains(out, "not stored yet") || !strings.Contains(out, "ready (imported)") || !strings.Contains(out, "no checksum") {
		t.Fatalf("%d %q", code, out)
	}
}

func TestDependenciesFetchAndImport(t *testing.T) {
	f := newFixture(t)
	entry := map[string]any{"id": "a", "name": "Docker Desktop", "kind": "installer", "state": "ready", "pinned": true, "can_fetch": true}
	f.reply("POST /api/v1/dependencies/store/a/fetch", 200, entry)
	f.reply("PUT /api/v1/dependencies/store/a/file", 200, entry)
	if code, out, _ := f.run("dependencies", "fetch", "a"); code != 0 || !strings.Contains(out, "Docker Desktop fetched") {
		t.Fatalf("fetch %d %q", code, out)
	}
	file := filepath.Join(t.TempDir(), "d.exe")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, out, _ := f.run("dependencies", "import", "a", file); code != 0 || !strings.Contains(out, "imported") {
		t.Fatalf("import %d %q", code, out)
	}
	if code, _, _ := f.run("dependencies", "import", "a"); code != ExitUsage {
		t.Fatalf("import without a file: %d", code)
	}
	if code, _, _ := f.run("dependencies", "import", "a", filepath.Join(t.TempDir(), "missing")); code != ExitUsage {
		t.Fatalf("import of a missing file: %d", code)
	}
}
