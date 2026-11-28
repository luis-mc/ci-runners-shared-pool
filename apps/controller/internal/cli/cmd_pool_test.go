package cli

import (
	"net/http"
	"strings"
	"testing"
)

func renameJSON(state string) map[string]any {
	return map[string]any{"id": "pr1", "old": "crsp-home", "new": "crsp-lab", "state": state,
		"steps":  []map[string]any{{"id": "add_label:r1", "kind": "add_label", "text": "Add the label crsp-lab to runner box-1", "state": "pending"}},
		"counts": map[string]any{"runners": 1, "registered": 1, "ephemeral": 0, "repositories": 2, "scale_set": false},
		"created_at": "2026-11-28T10:00:00Z", "updated_at": "2026-11-28T10:00:00Z"}
}

func TestPoolNameAndRename(t *testing.T) { // runners R5.141 to R5.143
	f := newFixture(t)
	f.handle("GET /api/v1/pool/name", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"name": "crsp-home", "source": "default", "env_override": false})
	})
	f.handle("POST /api/v1/pool/renames", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 201, renameJSON("planned")) })
	f.handle("POST /api/v1/pool/renames/pr1/apply", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 202, renameJSON("applying")) })
	f.handle("GET /api/v1/pool/renames/pr1", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, renameJSON("done")) })
	if code, out, _ := f.run("pool", "name"); code != 0 || !strings.Contains(out, "crsp-home (default)") {
		t.Fatalf("name: %d %q", code, out)
	}
	if code, _, _ := f.run("pool", "rename", "crsp-lab"); code != ExitNotConfirmed {
		t.Fatalf("without --yes it changes nothing: %d", code)
	}
	if code, out, _ := f.run("pool", "rename", "crsp-lab", "--yes"); code != 0 || !strings.Contains(out, "called crsp-lab") {
		t.Fatalf("rename: %d %q", code, out)
	}
	if code, _, _ := f.run("pool", "rename"); code != ExitUsage {
		t.Fatalf("usage: %d", code)
	}
}
