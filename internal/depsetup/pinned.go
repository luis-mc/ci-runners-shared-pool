// Package depsetup is the shared, pure part of dependency setup (spec
// features/dependency-setup, CRSP248): the item ids, the needs of a runner
// choice, the phases of a job and the pinned list of allowed installers. The
// controller builds offers from it and the agent resolves every item in it; no
// URL, hash or command ever travels from the controller to the agent.
package depsetup

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sync"
)

// Item ids (the `id` of DepsItem on the agent link and of DependencyItem in the API).
const (
	ItemWSL2          = "wsl2"
	ItemDockerWSL2    = "docker-in-wsl2"
	ItemDockerDesktop = "docker-desktop"
	ItemDockerEngine  = "docker-engine"
)

// Item states (DepsItem.state).
const (
	StateOK        = "ok"
	StateMissing   = "missing"
	StateStopped   = "stopped"
	StateWrongKind = "wrong_kind"
)

// Phase ids of a job, in order. A job runs the phases it needs; phase_count in
// an event is the number it runs.
const (
	PhaseCheck    = "check"
	PhaseDownload = "download"
	PhaseInstall  = "install"
	PhaseRestart  = "restart"
	PhaseVerify   = "verify"
	PhasePrepare  = "prepare"
	PhaseUndo     = "undo"
)

// Job outcomes (final DepsEvent.outcome) and job states (API).
const (
	OutcomeDone          = "done"
	OutcomePausedRestart = "paused_restart"
	OutcomeFailed        = "failed"
	OutcomeStopped       = "stopped"
)

// Kind of how an item is installed.
const (
	MethodCommands = "commands" // run a fixed list of commands from this file
	MethodDownload = "download" // download a file, check its SHA-256, run it
)

// Pinned is one allowed installer (DEP5, D-DEP-10: pinned per release).
type Pinned struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Version string `json:"version"`
	// OS is the machine OS it applies to: windows, linux or darwin.
	OS     string `json:"os"`
	Method string `json:"method"`
	// Source is the host shown to the user ("learn.microsoft.com", "download.docker.com").
	Source string `json:"source"`
	// URL and SHA256 for MethodDownload. An empty SHA256 means "not pinned yet":
	// the agent refuses to run it and the wizard offers "Do it myself".
	URL    string `json:"url,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
	// SilentArgs are the fixed arguments of a downloaded installer.
	SilentArgs []string `json:"silent_args,omitempty"`
	// Facts for the offer (DEP3).
	DownloadBytes  int64  `json:"download_bytes"`
	DiskBytes      int64  `json:"disk_bytes"`
	TypicalMinutes int    `json:"typical_minutes"`
	NeedsAdmin     bool   `json:"needs_admin"`
	RestartLikely  bool   `json:"restart_likely"`
	LicenceNote    string `json:"licence_note,omitempty"`
	Undo           string `json:"undo"`
	// ManualSteps is the "Do it myself" text, one copyable command per entry.
	ManualSteps []string `json:"manual_steps"`
	// MinVersion is the oldest engine version crsp accepts (Docker 24.0 for Docker items).
	MinVersion string `json:"min_version,omitempty"`
}

// List is the pinned list shipped with the release.
type List struct {
	Release string   `json:"release"`
	Items   []Pinned `json:"items"`
}

//go:embed pinned.json
var pinnedJSON []byte

var (
	once   sync.Once
	loaded List
	loadEr error
)

// Load returns the embedded pinned list.
func Load() (List, error) {
	once.Do(func() { loadEr = json.Unmarshal(pinnedJSON, &loaded) })
	return loaded, loadEr
}

// Find returns the pinned entry of an item id for an OS ("windows", "linux", "darwin").
func (l List) Find(id, goos string) (Pinned, bool) {
	for _, p := range l.Items {
		if p.ID == id && p.OS == goos {
			return p, true
		}
	}
	return Pinned{}, false
}

// MustLoad is Load for tests and start-up; it panics on a broken embedded file.
func MustLoad() List {
	l, err := Load()
	if err != nil {
		panic(fmt.Sprintf("depsetup: pinned.json: %v", err))
	}
	return l
}
