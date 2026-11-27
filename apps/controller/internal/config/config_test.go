package config

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func files(m map[string]string) func(string) ([]byte, error) {
	return func(p string) ([]byte, error) {
		if v, ok := m[p]; ok {
			return []byte(v), nil
		}
		return nil, os.ErrNotExist
	}
}

func abs(t *testing.T) string { return filepath.Join(t.TempDir(), "d") }

func TestLoadDefaults(t *testing.T) {
	cfg, err := LoadFor("linux", env(nil), files(nil))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ListenAddr != "127.0.0.1:8460" || cfg.AgentAddr != "127.0.0.1:8461" || cfg.LogLevel != slog.LevelInfo ||
		cfg.DataDir != "/var/lib/crsp" || cfg.File != "" {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
}

func TestDataFolderPerOS(t *testing.T) {
	cases := map[string]Paths{
		"linux":   {"/var/lib/crsp", "/etc/crsp/config.toml"},
		"darwin":  {"/Library/Application Support/crsp", "/Library/Application Support/crsp/config.toml"},
		"windows": {`D:\PD\crsp`, `D:\PD\crsp\config.toml`},
	}
	for goos, want := range cases {
		got := DefaultPaths(goos, env(map[string]string{"ProgramData": `D:\PD`}))
		if got != want {
			t.Errorf("%s: got %+v want %+v", goos, got, want)
		}
	}
	if p := DefaultPaths("windows", env(nil)); p.DataDir != `C:\ProgramData\crsp` {
		t.Errorf("windows fallback: %+v", p)
	}
}

func TestFileThenEnvOverride(t *testing.T) {
	dir := abs(t)
	file := `data_dir = "` + filepath.ToSlash(dir) + `"
listen_addr = "127.0.0.1:9000"
log_level = "warn"
`
	read := files(map[string]string{"/etc/crsp/config.toml": file})
	cfg, err := LoadFor("linux", env(nil), read)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DataDir != filepath.ToSlash(dir) || cfg.ListenAddr != "127.0.0.1:9000" || cfg.LogLevel != slog.LevelWarn || cfg.File == "" {
		t.Fatalf("file values lost: %+v", cfg)
	}
	cfg, err = LoadFor("linux", env(map[string]string{EnvListenAddr: "127.0.0.1:9100", EnvLogLevel: "debug"}), read)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ListenAddr != "127.0.0.1:9100" || cfg.LogLevel != slog.LevelDebug {
		t.Fatalf("env must win: %+v", cfg)
	}
}

func TestCacheQuotaKeys(t *testing.T) {
	cfg, err := LoadFor("linux", env(nil), files(nil))
	if err != nil || cfg.Cache.ToolGiB != 20 || cfg.Cache.RustGiB != 40 || cfg.Cache.MinFreePct != 10 {
		t.Fatalf("defaults: %+v %v", cfg.Cache, err)
	}
	read := files(map[string]string{"/etc/crsp/config.toml": "cache_tool_gib = 8\ncache_rust_gib = 100\ncache_min_free_pct = 15\n"})
	cfg, err = LoadFor("linux", env(nil), read)
	if err != nil || cfg.Cache.ToolGiB != 8 || cfg.Cache.RustGiB != 100 || cfg.Cache.MinFreePct != 15 {
		t.Fatalf("file: %+v %v", cfg.Cache, err)
	}
	if cfg.Cache.RustKeyGiB != 25 || cfg.Cache.BudgetGiB != 150 || cfg.Cache.AlertGiB != 20 {
		t.Fatalf("new defaults: %+v", cfg.Cache)
	}
	more := files(map[string]string{"/etc/crsp/config.toml": "cache_rust_key_gib = 9\ncache_budget_gib = 80\ncache_alert_gib = 5\n"})
	cfg, err = LoadFor("linux", env(nil), more)
	if err != nil || cfg.Cache.RustKeyGiB != 9 || cfg.Cache.BudgetGiB != 80 || cfg.Cache.AlertGiB != 5 {
		t.Fatalf("key, budget, alert: %+v %v", cfg.Cache, err)
	}
	bad := files(map[string]string{"/etc/crsp/config.toml": "cache_min_free_pct = 90\n"})
	if _, err = LoadFor("linux", env(nil), bad); err == nil || !strings.Contains(err.Error(), "min_free_pct") {
		t.Fatalf("bad threshold accepted: %v", err)
	}
}

func TestExplicitConfigFileMustExist(t *testing.T) {
	_, err := LoadFor("linux", env(map[string]string{EnvConfigFile: "/nope/config.toml"}), files(nil))
	if err == nil || !strings.Contains(err.Error(), "/nope/config.toml") {
		t.Fatalf("want error naming the file, got %v", err)
	}
}

func TestRefusesPlaceholders(t *testing.T) {
	for _, v := range []string{"<path>", "<...>", "changeme", "CHANGE_ME", "your-data-dir", "${CRSP_DATA_DIR}", "TODO", "example"} {
		_, err := LoadFor("linux", env(map[string]string{EnvDataDir: v}), files(nil))
		if err == nil || !strings.Contains(err.Error(), "placeholder") {
			t.Errorf("env %q: want placeholder error, got %v", v, err)
		}
		_, err = LoadFor("linux", env(nil), files(map[string]string{"/etc/crsp/config.toml": `data_dir = "` + v + `"`}))
		if err == nil || !strings.Contains(err.Error(), "placeholder") {
			t.Errorf("file %q: want placeholder error, got %v", v, err)
		}
	}
}

func TestRefusesUnknownKeysWrongTypesAndSecrets(t *testing.T) {
	cases := map[string]struct{ file, want string }{
		"unknown key":  {`bogus = 1`, `unknown key "bogus"`},
		"wrong type":   {`listen_addr = 5`, "ListenAddr"},
		"not toml":     {`listen_addr = `, "line"},
		"secret key":   {`privatekey = "-----BEGIN KEY-----"`, `key "privatekey" holds a secret`},
		"nested token": {"[github]\nwebhooksecret = \"s3cr3t-value\"", `key "github.webhooksecret" holds a secret`},
	}
	for name, c := range cases {
		_, err := LoadFor("linux", env(nil), files(map[string]string{"/etc/crsp/config.toml": c.file}))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: want %q, got %v", name, c.want, err)
			continue
		}
		for _, leak := range []string{"s3cr3t-value", "BEGIN KEY"} {
			if strings.Contains(err.Error(), leak) {
				t.Errorf("%s: error echoes the secret value: %v", name, err)
			}
		}
	}
}

func TestRefusesBadValues(t *testing.T) {
	d := abs(t)
	cases := map[string]map[string]string{
		"relative dir": {EnvDataDir: "relative/dir"},
		"no port":      {EnvDataDir: d, EnvListenAddr: "localhost"},
		"bad port":     {EnvDataDir: d, EnvListenAddr: "localhost:99999"},
		"port zero":    {EnvDataDir: d, EnvAgentAddr: "localhost:0"},
		"same ports":   {EnvDataDir: d, EnvListenAddr: "127.0.0.1:9", EnvAgentAddr: "127.0.0.1:9"},
		"bad level":    {EnvDataDir: d, EnvLogLevel: "loud"},
	}
	for name, m := range cases {
		if _, err := LoadFor("linux", env(m), files(nil)); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}

func TestReportsAllProblems(t *testing.T) {
	_, err := LoadFor("linux", env(map[string]string{EnvDataDir: "<x>", EnvListenAddr: "bad", EnvLogLevel: "loud"}),
		files(map[string]string{"/etc/crsp/config.toml": "bogus = 1\npassword = \"x\""}))
	if err == nil {
		t.Fatal("want error")
	}
	for _, want := range []string{EnvDataDir, "listen_addr", "log_level", `"bogus"`, `"password"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %s: %v", want, err)
		}
	}
}

func TestWindowsAbsolutePaths(t *testing.T) {
	if _, err := LoadFor("windows", env(nil), files(nil)); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFor("windows", env(map[string]string{EnvDataDir: `relative\dir`}), files(nil)); err == nil {
		t.Fatal("want error")
	}
}

func TestMissingDefaultFileIsNotAnError(t *testing.T) {
	_, err := LoadFor("linux", env(nil), func(string) ([]byte, error) { return nil, os.ErrNotExist })
	if err != nil {
		t.Fatal(err)
	}
	_, err = LoadFor("linux", env(nil), func(string) ([]byte, error) { return nil, errors.New("permission denied") })
	if err == nil {
		t.Fatal("a file that cannot be read must be refused")
	}
}

func TestDemandQuietMinutes(t *testing.T) { // github-integration R5.37
	cfg, err := LoadFor("linux", env(nil), files(nil))
	if err != nil || cfg.DemandQuiet != 10*time.Minute {
		t.Fatalf("default: %v %v", cfg.DemandQuiet, err)
	}
	cfg, err = LoadFor("linux", env(nil), files(map[string]string{"/etc/crsp/config.toml": "demand_quiet_minutes = 3\n"}))
	if err != nil || cfg.DemandQuiet != 3*time.Minute {
		t.Fatalf("file: %v %v", cfg.DemandQuiet, err)
	}
	if _, err = LoadFor("linux", env(nil), files(map[string]string{"/etc/crsp/config.toml": "demand_quiet_minutes = 2000\n"})); err == nil || !strings.Contains(err.Error(), "demand_quiet_minutes") {
		t.Fatalf("out of range accepted: %v", err)
	}
}

func TestScaleSetAndSweepSettings(t *testing.T) { // github-integration R5.40, R5.33
	cfg, err := LoadFor("linux", env(nil), files(nil))
	if err != nil || cfg.ScaleSetDemand || cfg.RunnerSweepDays != 7 {
		t.Fatalf("defaults: %+v %v", cfg, err)
	}
	cfg, err = LoadFor("linux", env(nil), files(map[string]string{"/etc/crsp/config.toml": "scaleset_demand = true\nrunner_sweep_days = 0\n"}))
	if err != nil || !cfg.ScaleSetDemand || cfg.RunnerSweepDays != 0 {
		t.Fatalf("file: %+v %v", cfg, err)
	}
	if _, err = LoadFor("linux", env(nil), files(map[string]string{"/etc/crsp/config.toml": "runner_sweep_days = 400\n"})); err == nil {
		t.Fatal("400 days is refused")
	}
}

func TestDiskFloorGiB(t *testing.T) { // runners R5.83
	for body, want := range map[string]int{"": 10, "disk_floor_gib = 25\n": 25, "disk_floor_gib = 0\n": 0, "disk_floor_gib = -3\n": 0} {
		files := files(nil)
		if body != "" {
			files = filesOf(body)
		}
		cfg, err := LoadFor("linux", env(nil), files)
		if err != nil || cfg.DiskFloorGiB != want {
			t.Errorf("%q: %d %v, want %d", body, cfg.DiskFloorGiB, err, want)
		}
	}
}

func filesOf(body string) func(string) ([]byte, error) {
	return files(map[string]string{"/etc/crsp/config.toml": body})
}
