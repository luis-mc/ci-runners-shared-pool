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

const setupYAML = "version: 1\nsettings:\n  queue: fifo\n"

func writeSetup(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "setup.yaml")
	if err := os.WriteFile(p, []byte(setupYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func planReply(f *fixture, body map[string]any) {
	f.reply("POST /api/v1/config/plans", 201, body)
}

func TestConfigExportPrintsAndSavesRawYAML(t *testing.T) {
	f := newFixture(t)
	f.handle("GET /api/v1/config/export", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/yaml")
		_, _ = w.Write([]byte(setupYAML))
	})
	code, out, errOut := f.run("config", "export")
	if code != 0 || out != setupYAML {
		t.Fatalf("exit %d out %q err %q", code, out, errOut)
	}
	dst := filepath.Join(t.TempDir(), "out.yaml")
	if code, _, errOut = f.run("config", "export", "--out", dst); code != 0 {
		t.Fatalf("exit %d err %q", code, errOut)
	}
	if b, _ := os.ReadFile(dst); string(b) != setupYAML {
		t.Fatalf("saved %q", b)
	}
}

func TestConfigPlanAndDryRunChangeNothingAndExit8(t *testing.T) {
	for _, args := range [][]string{{"config", "plan"}, {"config", "import", "--dry-run"}} {
		f := newFixture(t)
		planReply(f, map[string]any{"id": "p1", "created_at": "2026-11-28T06:00:00Z", "removals": []string{"queue/old"},
			"changes": []any{
				map[string]any{"section": "settings", "name": "", "kind": "changed", "fields": []any{map[string]any{"field": "queue", "old": "lifo", "new": "fifo"}}},
				map[string]any{"section": "queue", "name": "old", "kind": "removed"},
			}})
		code, out, errOut := f.run(append(args, writeSetup(t))...)
		if code != ExitFindings {
			t.Fatalf("%v: exit %d out %q err %q", args, code, out, errOut)
		}
		if !strings.Contains(out, "removed queue/old") || !strings.Contains(out, "queue: lifo -> fifo") {
			t.Fatalf("%v: out %q", args, out)
		}
		if f.called("POST /api/v1/config/plans/") {
			t.Fatalf("%v: applied", args)
		}
	}
}

func TestConfigPlanNoChangesExits0(t *testing.T) {
	f := newFixture(t)
	planReply(f, map[string]any{"id": "p1", "created_at": "2026-11-28T06:00:00Z", "changes": []any{}})
	if code, out, _ := f.run("config", "plan", writeSetup(t)); code != 0 || !strings.Contains(out, "No changes") {
		t.Fatalf("exit %d out %q", code, out)
	}
}

func TestConfigImportNeedsDryRun(t *testing.T) {
	f := newFixture(t)
	if code, _, errOut := f.run("config", "import", writeSetup(t)); code != ExitUsage || !strings.Contains(errOut, "config apply") {
		t.Fatalf("exit %d err %q", code, errOut)
	}
	if len(f.calls) != 0 {
		t.Fatalf("called %v", f.calls)
	}
}

func TestConfigFileWithProblemsCannotBeApplied(t *testing.T) {
	f := newFixture(t)
	planReply(f, map[string]any{"problems": []string{"settings.queue: unknown value"}, "created_at": "2026-11-28T06:00:00Z", "changes": []any{}})
	code, _, errOut := f.run("config", "apply", "--yes", writeSetup(t))
	if code != ExitFailed || !strings.Contains(errOut, "unknown value") || f.called("POST /api/v1/config/plans/") {
		t.Fatalf("exit %d err %q calls %v", code, errOut, f.calls)
	}
}

func applyFixture(t *testing.T) (*fixture, *map[string]any) {
	f := newFixture(t)
	planReply(f, map[string]any{"id": "p1", "created_at": "2026-11-28T06:00:00Z", "removals": []string{"queue/old"},
		"changes": []any{map[string]any{"section": "queue", "name": "old", "kind": "removed"}}})
	got := map[string]any{}
	f.handle("POST /api/v1/config/plans/p1/apply", func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &got)
		writeJSON(w, 200, map[string]any{"plan_id": "p1", "applied": []any{map[string]any{"section": "queue", "name": "old", "kind": "removed"}}})
	})
	return f, &got
}

func TestConfigApplySendsEachRemovalByName(t *testing.T) {
	f, got := applyFixture(t)
	code, out, errOut := f.run("config", "apply", "--yes", writeSetup(t))
	if code != 0 || !strings.Contains(out, "1 change applied") || !strings.Contains(errOut, "queue/old") {
		t.Fatalf("exit %d out %q err %q", code, out, errOut)
	}
	if r, _ := (*got)["confirm_removals"].([]any); len(r) != 1 || r[0] != "queue/old" {
		t.Fatalf("body %v", *got)
	}
}

func TestConfigApplyAsksAndNoMeansNothingChanged(t *testing.T) {
	f, _ := applyFixture(t)
	f.terminal, f.stdin = true, "no\n"
	code, _, errOut := f.run("config", "apply", writeSetup(t))
	if code != ExitNotConfirmed || !strings.Contains(errOut, "Type yes") || f.called("POST /api/v1/config/plans/") {
		t.Fatalf("exit %d err %q calls %v", code, errOut, f.calls)
	}
	// No terminal and no --yes: exit 5 as well, nothing applied.
	f2, _ := applyFixture(t)
	if code, _, _ = f2.run("config", "apply", writeSetup(t)); code != ExitNotConfirmed || f2.called("POST /api/v1/config/plans/") {
		t.Fatalf("exit %d", code)
	}
}

func TestConfigApplyYesAnswer(t *testing.T) {
	f, _ := applyFixture(t)
	f.terminal, f.stdin = true, "yes\n"
	if code, _, errOut := f.run("config", "apply", writeSetup(t)); code != 0 {
		t.Fatalf("exit %d err %q", code, errOut)
	}
}

func TestConfigApplyStaleIsConflict(t *testing.T) {
	f, _ := applyFixture(t)
	f.reply("POST /api/v1/config/plans/p1/apply", 409, apiErr("conflict", "the setup changed since the plan"))
	if code, _, errOut := f.run("config", "apply", "--yes", writeSetup(t)); code != ExitConflict || !strings.Contains(errOut, "changed since") {
		t.Fatalf("exit %d err %q", code, errOut)
	}
}

func TestConfigMissingFileIsUsage(t *testing.T) {
	f := newFixture(t)
	if code, _, _ := f.run("config", "plan", filepath.Join(t.TempDir(), "none.yaml")); code != ExitUsage {
		t.Fatalf("exit %d", code)
	}
	if code, _, _ := f.run("config", "plan"); code != ExitUsage {
		t.Fatalf("exit %d", code)
	}
}
