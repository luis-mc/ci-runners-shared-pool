package configexport

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/config"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/machines/secretstest"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/settings"
)

type fixedSettings struct{}

func (fixedSettings) Get(context.Context) (settings.Settings, error) { return settings.Defaults, nil }

// A secret is named only as set or not set; its value is never in the file.
func TestYAMLHasNoSecretValue(t *testing.T) {
	ctx := context.Background()
	store := secretstest.New()
	const value = "s3cr3t-value-123"
	_ = store.Put(ctx, "crsp/github-app/42/private-key", []byte(value))
	_ = store.Put(ctx, "crsp/controller/ca-key", []byte(value))
	src := Source{
		Version: "1.2.3", Config: config.Config{DataDir: "/var/lib/crsp", ListenAddr: "127.0.0.1:8460", AgentAddr: "127.0.0.1:8461"},
		Settings: fixedSettings{}, Secrets: store,
		Named: []NamedSecret{{"Controller certificate key", "crsp/controller/ca-key"}, {"Controller server key", "crsp/controller/server-key"}},
		AppID: func(context.Context) int64 { return 42 },
		Now:   func() time.Time { return time.Date(2026, 11, 14, 10, 5, 0, 0, time.UTC) },
	}
	out, err := src.YAML(ctx)
	if err != nil {
		t.Fatal(err)
	}
	text := string(out)
	if strings.Contains(text, value) {
		t.Fatal("a secret value is in the export")
	}
	if regexp.MustCompile(`(?m)^\s*-?\s*\w*(_secret|_token|_key|_pass)\s*:`).MatchString(text) {
		t.Fatalf("a key named like a secret is in the export:\n%s", text)
	}
	var doc struct {
		Secrets  []struct{ Name, State string }
		Settings struct {
			Backup struct {
				TimeLocal string `yaml:"time_local"`
				KeepDays  int    `yaml:"keep_days"`
			}
		}
	}
	if err := yaml.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, s := range doc.Secrets {
		got[s.Name] = s.State
	}
	want := map[string]string{
		"Controller certificate key": "set", "Controller server key": "not set",
		"GitHub App private key": "set", "GitHub App client secret": "not set", "GitHub App webhook secret": "not set",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: %q, want %q", k, got[k], v)
		}
	}
	if doc.Settings.Backup.TimeLocal != "03:00" || doc.Settings.Backup.KeepDays != 14 {
		t.Errorf("backup schedule: %+v", doc.Settings.Backup)
	}
}

func TestYAMLWithoutStoreMarksNotSet(t *testing.T) {
	out, err := Source{Settings: fixedSettings{}, Named: []NamedSecret{{"A", "k"}}}.YAML(context.Background())
	if err != nil || !strings.Contains(string(out), "state: not set") {
		t.Fatalf("%v\n%s", err, out)
	}
}
