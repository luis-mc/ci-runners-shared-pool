package cli

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func compactMachine(mode string) map[string]any {
	m := machineJSON("win-01", "active", true)
	st := m["settings"].(map[string]any)
	st["docker_compact"], st["docker_compact_threshold_gib"], st["docker_compact_idle_minutes"] = mode, 30, 30
	m["docker_compact_status"] = map[string]any{"enabled": mode != "off", "running": false, "disk_present": true, "can_compact": false,
		"file_bytes": int64(236) << 30, "used_bytes": int64(71) << 30, "reclaimable_bytes": int64(165) << 30}
	return m
}

// caches R5.51 to R5.54: `machine settings` shows the policy with its defaults.
func TestMachineSettingsShowsThePolicy(t *testing.T) {
	f := newFixture(t)
	f.machines(compactMachine("auto"))
	code, out, errOut := f.run("machine", "settings", "win-01")
	if code != 0 {
		t.Fatalf("%d %q", code, errOut)
	}
	for _, want := range []string{"win-01: auto", "30 GiB", "30 minutes", "once per day", "disk file 236 GiB, reclaimable 165 GiB", "no administrator rights"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %q", want, out)
		}
	}
}

// The flags become one PATCH of the settings and the new policy is shown.
func TestMachineSettingsSetsThePolicy(t *testing.T) {
	f := newFixture(t)
	f.machines(compactMachine("auto"))
	var body map[string]any
	f.handle("PATCH /api/v1/machines/m_win-01", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		writeJSON(w, 200, compactMachine("off"))
	})
	code, out, errOut := f.run("machine", "settings", "win-01", "--docker-compact", "off", "--docker-compact-threshold", "50", "--docker-compact-idle", "45")
	if code != 0 {
		t.Fatalf("%d %q", code, errOut)
	}
	if body["docker_compact"] != "off" || body["docker_compact_threshold_gib"] != float64(50) || body["docker_compact_idle_minutes"] != float64(45) {
		t.Fatalf("body %v", body)
	}
	if !strings.Contains(out, "win-01: off") {
		t.Fatalf("out %q", out)
	}
}

func TestMachineSettingsRefusesBadValues(t *testing.T) {
	for _, args := range [][]string{
		{"--docker-compact", "sometimes"}, {"--docker-compact-threshold", "0"}, {"--docker-compact-idle", "9999"},
	} {
		f := newFixture(t)
		f.machines(compactMachine("auto"))
		code, _, errOut := f.run(append([]string{"machine", "settings", "win-01"}, args...)...)
		if code != 2 || errOut == "" {
			t.Errorf("%v: code %d %q", args, code, errOut)
		}
		if f.indexOf("PATCH") >= 0 {
			t.Errorf("%v: a request was sent", args)
		}
	}
}
