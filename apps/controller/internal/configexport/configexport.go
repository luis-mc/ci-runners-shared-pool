// Package configexport writes the configuration as a YAML file with no secret
// in it (web-console R5.29, operations R5.26). It holds the settings, the
// retention and backup schedule and the config.toml values. A secret is named
// and marked "set" or "not set"; its value never leaves the OS secret store.
//
// Secret entries are list items with a `name` and a `state`, never a key named
// after the secret, so the file cannot be mistaken for a place to put one.
package configexport

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/config"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/github"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/secrets"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/settings"
)

// NamedSecret is a secret the controller needs, by a plain name and its key
// in the OS secret store.
type NamedSecret struct {
	Name string
	Key  secrets.Key
}

// SettingsReader is what the export needs from the settings service.
type SettingsReader interface {
	Get(ctx context.Context) (settings.Settings, error)
}

// Source collects the configuration of a running controller.
type Source struct {
	Version  string
	Config   config.Config
	Settings SettingsReader
	// Secrets is the OS secret store; nil marks every secret "not set".
	Secrets secrets.Store
	// Named are the fixed secrets (the controller's certificate keys).
	Named []NamedSecret
	// AppID returns the GitHub App id, or 0 when no App is set up; the App's
	// three secrets are listed once it is.
	AppID func(ctx context.Context) int64
	Now   func() time.Time
}

type document struct {
	Product     string       `yaml:"product"`
	Version     string       `yaml:"version,omitempty"`
	GeneratedAt string       `yaml:"generated_at"`
	Controller  controller   `yaml:"controller"`
	Cache       cache        `yaml:"cache"`
	Settings    settingsDoc  `yaml:"settings"`
	Secrets     []secretNote `yaml:"secrets"`
}

type controller struct {
	ConfigFile string `yaml:"config_file,omitempty"`
	DataDir    string `yaml:"data_dir"`
	ListenAddr string `yaml:"listen_addr"`
	AgentAddr  string `yaml:"agent_addr"`
	LogLevel   string `yaml:"log_level"`
}

type cache struct {
	ToolGiB    int `yaml:"tool_gib"`
	RustGiB    int `yaml:"rust_gib"`
	MinFreePct int `yaml:"min_free_pct"`
}

type settingsDoc struct {
	Retention retention `yaml:"retention"`
	Backup    backup    `yaml:"backup"`
	Events    events    `yaml:"events"`
	Updates   updates   `yaml:"updates"`
}

type retention struct {
	JobHistoryDays    int `yaml:"job_history_days"`
	LogsDays          int `yaml:"logs_days"`
	LogsMiBPerMachine int `yaml:"logs_mib_per_machine"`
	AuditDays         int `yaml:"audit_days"`
	LoadRawHours      int `yaml:"load_raw_hours"`
}

type backup struct {
	TimeLocal string `yaml:"time_local"`
	KeepDays  int    `yaml:"keep_days"`
}

type events struct {
	MaxStreams int `yaml:"max_streams"`
}

type updates struct {
	Auto bool `yaml:"auto"`
}

type secretNote struct {
	Name  string `yaml:"name"`
	State string `yaml:"state"` // "set" or "not set"
}

// YAML returns the export file. The store is asked only whether a secret
// exists; a value is dropped the moment it is read.
func (s Source) YAML(ctx context.Context) ([]byte, error) {
	if s.Settings == nil {
		return nil, errors.New("configexport: settings are not available")
	}
	st, err := s.Settings.Get(ctx)
	if err != nil {
		return nil, err
	}
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	c := s.Config
	q := c.Cache
	doc := document{
		Product:     "crsp",
		Version:     s.Version,
		GeneratedAt: now().UTC().Format(time.RFC3339),
		Controller: controller{
			ConfigFile: c.File, DataDir: c.DataDir, ListenAddr: c.ListenAddr, AgentAddr: c.AgentAddr, LogLevel: c.LogLevel.String(),
		},
		Cache: cache{ToolGiB: q.ToolGiB, RustGiB: q.RustGiB, MinFreePct: q.MinFreePct},
		Settings: settingsDoc{
			Retention: retention{
				JobHistoryDays: st.RetentionJobHistoryDays, LogsDays: st.RetentionLogsDays,
				LogsMiBPerMachine: st.RetentionLogsMiBPerMachine, AuditDays: st.RetentionAuditDays, LoadRawHours: st.RetentionLoadRawHours,
			},
			Backup:  backup{TimeLocal: st.BackupTimeLocal, KeepDays: st.BackupKeepDays},
			Events:  events{MaxStreams: st.MaxEventStreams},
			Updates: updates{Auto: st.AutoUpdate},
		},
		Secrets: []secretNote{},
	}
	named := append([]NamedSecret{}, s.Named...)
	if s.AppID != nil {
		if id := s.AppID(ctx); id != 0 {
			named = append(named,
				NamedSecret{"GitHub App private key", github.SecretKey(id, github.SecretPrivateKey)},
				NamedSecret{"GitHub App client secret", github.SecretKey(id, github.SecretClientSecret)},
				NamedSecret{"GitHub App webhook secret", github.SecretKey(id, github.SecretWebhookSecret)},
			)
		}
	}
	for _, n := range named {
		state, err := s.state(ctx, n.Key)
		if err != nil {
			return nil, fmt.Errorf("configexport: %s: %w", n.Name, err)
		}
		doc.Secrets = append(doc.Secrets, secretNote{Name: n.Name, State: state})
	}

	var buf bytes.Buffer
	buf.WriteString("# crsp configuration export. It holds no secret: a secret is named and marked set or not set.\n")
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// state asks the store whether a secret exists and drops the value at once.
func (s Source) state(ctx context.Context, k secrets.Key) (string, error) {
	if s.Secrets == nil {
		return "not set", nil
	}
	v, err := s.Secrets.Get(ctx, k)
	for i := range v {
		v[i] = 0
	}
	switch {
	case err == nil:
		return "set", nil
	case errors.Is(err, secrets.ErrNotFound):
		return "not set", nil
	}
	return "", err
}
