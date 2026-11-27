package runners

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

// The stored pins (operations R5.20): the daily move of the runner pin keeps
// the newer pins here, and PinFor reads them before the embedded pins.json.
var (
	overrideMu sync.RWMutex
	override   []Pin
)

type storedPin struct {
	OS      string `json:"os"`
	Arch    string `json:"arch"`
	Version string `json:"version"`
	SHA256  string `json:"sha256"`
}

// EncodePins is the stored form of pins (the settings row runner_pins).
func EncodePins(ps []Pin) (string, error) {
	out := make([]storedPin, 0, len(ps))
	for _, p := range ps {
		out = append(out, storedPin(p))
	}
	b, err := json.Marshal(out)
	return string(b), err
}

// DecodePins reads the stored form and checks it like the pins file: a real
// SHA-256, a version, one pin per OS and architecture.
func DecodePins(s string) ([]Pin, error) {
	var in []storedPin
	if err := json.Unmarshal([]byte(s), &in); err != nil {
		return nil, fmt.Errorf("stored runner pins: %w", err)
	}
	seen := map[string]bool{}
	out := make([]Pin, 0, len(in))
	for _, r := range in {
		key := r.OS + "/" + r.Arch
		if seen[key] {
			return nil, fmt.Errorf("stored runner pins: %s is pinned twice", key)
		}
		seen[key] = true
		if r.Version == "" {
			return nil, fmt.Errorf("stored runner pins: %s has no version", key)
		}
		if !ValidSHA256(r.SHA256) {
			return nil, fmt.Errorf("stored runner pins: %s: %w", key, ErrBadHash)
		}
		out = append(out, Pin{OS: r.OS, Arch: r.Arch, Version: r.Version, SHA256: strings.ToLower(r.SHA256)})
	}
	return out, nil
}

// SetStoredPins replaces the stored pins; PinFor reads them first.
func SetStoredPins(ps []Pin) {
	overrideMu.Lock()
	defer overrideMu.Unlock()
	override = append([]Pin(nil), ps...)
}

// AllPins is every pin in force: a stored pin wins over the embedded one of the
// same OS and architecture.
func AllPins() []Pin {
	overrideMu.RLock()
	defer overrideMu.RUnlock()
	out := append([]Pin(nil), override...)
	for _, p := range pins {
		taken := false
		for _, o := range override {
			if o.OS == p.OS && o.Arch == p.Arch {
				taken = true
			}
		}
		if !taken {
			out = append(out, p)
		}
	}
	return out
}
