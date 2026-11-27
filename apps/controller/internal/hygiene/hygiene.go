// Package hygiene is what a persistent runner's clean-up leaves in its folder,
// and the one setting it reads: whether the runner keeps its downloaded
// dependencies (runners R5.90, R5.91). The backends write the setting file, the
// hooks read it and write the state file, and the agent reads the state file to
// tell the controller. Temporary files of a job are always removed; nothing in
// this package can turn that off.
package hygiene

import (
	"encoding/json"
	"errors"
	"io/fs"
	"strings"
	"time"
)

// File names inside the runner folder.
const (
	// SettingsFile holds `CRSP_KEEP_DEPENDENCIES=1` or `=0`. It never holds a
	// token or any other secret (runners R6.2).
	SettingsFile = "crsp-hygiene.env"
	// StateFile is what the last clean-up found, as JSON.
	StateFile = "crsp-clean.json"
)

// Names of the values the runner and its hooks read.
const (
	// EnvRunnerDir is the runner folder, set in the runner's .env.
	EnvRunnerDir = "CRSP_RUNNER_DIR"
	// EnvKeep is the key of SettingsFile.
	EnvKeep = "CRSP_KEEP_DEPENDENCIES"
)

// Reader reads one file (sys.FS satisfies it).
type Reader interface {
	ReadFile(path string) ([]byte, error)
}

// Writer writes one file (sys.FS satisfies it).
type Writer interface {
	WriteFile(path string, data []byte, perm fs.FileMode) error
}

// SettingsText is the content of the settings file.
func SettingsText(keep bool) string {
	if keep {
		return EnvKeep + "=1\n"
	}
	return EnvKeep + "=0\n"
}

// ParseKeep reads the settings text. Missing, empty or unreadable text means
// keep: the default of a persistent runner is to keep its dependencies.
func ParseKeep(text string) bool {
	for _, ln := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(ln), "=")
		if ok && strings.TrimSpace(k) == EnvKeep {
			switch strings.ToLower(strings.TrimSpace(v)) {
			case "0", "false", "no", "off":
				return false
			}
			return true
		}
	}
	return true
}

// ReadKeep reads the setting of the runner folder. A folder with no file keeps.
func ReadKeep(r Reader, folder string) bool {
	b, err := r.ReadFile(join(folder, SettingsFile))
	if err != nil {
		return true
	}
	return ParseKeep(string(b))
}

// WriteKeep writes the setting of the runner folder.
func WriteKeep(w Writer, folder string, keep bool) error {
	return w.WriteFile(join(folder, SettingsFile), []byte(SettingsText(keep)), 0o600)
}

// State is what the last clean-up of a runner found.
type State struct {
	// UncleanReason is empty when the runner is clean; else plain words naming
	// what stays. A runner with a reason takes no new job (runners R5.91).
	UncleanReason string    `json:"unclean_reason,omitempty"`
	At            time.Time `json:"at"`
	// Phase is "job-started" or "job-completed".
	Phase string `json:"phase,omitempty"`
}

// ReadState reads the state file. No file is a clean runner that has not been
// cleaned yet: the zero State.
func ReadState(r Reader, folder string) (State, error) {
	b, err := r.ReadFile(join(folder, StateFile))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return State{}, nil
		}
		return State{}, err
	}
	var s State
	if err := json.Unmarshal(b, &s); err != nil {
		return State{}, err
	}
	return s, nil
}

// WriteState writes the state file.
func WriteState(w Writer, folder string, s State) error {
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return w.WriteFile(join(folder, StateFile), b, 0o600)
}

// join joins with the separator the folder already uses, so a Windows runner
// folder planned on another OS stays whole.
func join(folder, name string) string {
	switch {
	case folder == "":
		return name
	case strings.HasSuffix(folder, "/") || strings.HasSuffix(folder, `\`):
		return folder + name
	case strings.Contains(folder, `\`) && !strings.Contains(folder, "/"):
		return folder + `\` + name
	}
	return folder + "/" + name
}
