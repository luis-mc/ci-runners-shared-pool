// Package runnerpin keeps the pinned GitHub runner version close to the newest
// one (operations R5.20). Once a day the manager reads the newest actions/runner
// release, moves the pin to it when it is old enough (7 days by default, never
// past GitHub's 30 day limit) and keeps the new pin in the settings. Nothing here
// reads an OS file, so it is the same code on Windows, Linux and macOS.
package runnerpin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/runners"
)

// Defaults of operations R5.20.
const (
	DefaultMoveAfterDays = 7
	MaxMoveAfterDays     = 25
	WarnBehindDays       = 21
	FailBehindDays       = 28
	// SettingKey is the settings row that keeps the moved pins.
	SettingKey = "runner_pins"
)

// platforms maps the platform names of the release notes to OS and architecture.
var platforms = map[string][2]string{
	"win-x64":     {"windows", "x64"},
	"win-arm64":   {"windows", "arm64"},
	"osx-x64":     {"macos", "x64"},
	"osx-arm64":   {"macos", "arm64"},
	"linux-x64":   {"linux", "x64"},
	"linux-arm64": {"linux", "arm64"},
}

// Release is one actions/runner release.
type Release struct {
	Version     string
	PublishedAt time.Time
	// Hashes is the SHA-256 of the archive of each platform name.
	Hashes map[string]string
}

var shaLine = regexp.MustCompile(`<!--\s*BEGIN SHA ([a-z0-9-]+)\s*-->\s*([0-9a-fA-F]{64})\s*<!--\s*END SHA`)

// ParseRelease reads the tag, the date and the notes of a release.
func ParseRelease(tag string, published time.Time, body string) Release {
	r := Release{Version: strings.TrimPrefix(strings.TrimSpace(tag), "v"), PublishedAt: published.UTC(), Hashes: map[string]string{}}
	for _, m := range shaLine.FindAllStringSubmatch(body, -1) {
		r.Hashes[m[1]] = strings.ToLower(m[2])
	}
	return r
}

// Compare orders two dotted versions: -1, 0 or 1. A part that is not a number
// counts as 0.
func Compare(a, b string) int {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var x, y int
		if i < len(pa) {
			x, _ = strconv.Atoi(pa[i])
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(pb[i])
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}

// Decision is what the daily check found.
type Decision struct {
	Move bool
	From string
	To   string
	// Pins is the full new set when Move is true.
	Pins []runners.Pin
	// Reason says why nothing moves, or what moved.
	Reason string
}

// Decide compares the pins in force with the newest release. The pin moves
// when the release is newer and at least moveAfter old, and only when the notes
// carry a hash for every platform that is pinned (a half-known release moves nothing).
func Decide(cur []runners.Pin, rel Release, now time.Time, moveAfter time.Duration) Decision {
	oldest := ""
	for _, p := range cur {
		if oldest == "" || Compare(p.Version, oldest) < 0 {
			oldest = p.Version
		}
	}
	if oldest == "" || Compare(rel.Version, oldest) <= 0 {
		return Decision{From: oldest, To: oldest, Reason: "the pin is the newest release"}
	}
	if age := now.Sub(rel.PublishedAt); age < moveAfter {
		return Decision{From: oldest, To: rel.Version, Reason: fmt.Sprintf("%s is %d days old; the pin moves at %d days", rel.Version, int(age.Hours()/24), int(moveAfter.Hours()/24))}
	}
	var next []runners.Pin
	for _, p := range cur {
		var plat string
		for name, osarch := range platforms {
			if osarch[0] == p.OS && osarch[1] == p.Arch {
				plat = name
			}
		}
		h := rel.Hashes[plat]
		if plat == "" || !runners.ValidSHA256(h) {
			return Decision{From: oldest, To: rel.Version, Reason: fmt.Sprintf("the notes of %s have no usable SHA-256 for %s/%s, so the pin stays", rel.Version, p.OS, p.Arch)}
		}
		next = append(next, runners.Pin{OS: p.OS, Arch: p.Arch, Version: rel.Version, SHA256: h})
	}
	return Decision{Move: true, From: oldest, To: rel.Version, Pins: next, Reason: fmt.Sprintf("the pin moved from %s to %s", oldest, rel.Version)}
}

// BehindDays is how many whole days the pin has been behind the newest release
// (0 when it is the newest). The doctor warns at WarnBehindDays.
func BehindDays(cur []runners.Pin, rel Release, now time.Time) int {
	for _, p := range cur {
		if Compare(p.Version, rel.Version) < 0 {
			d := int(now.Sub(rel.PublishedAt).Hours() / 24)
			if d < 0 {
				d = 0
			}
			return d
		}
	}
	return 0
}

// Doer is the HTTP client.
type Doer interface {
	Do(*http.Request) (*http.Response, error)
}

// Fetch reads the newest release of actions/runner from the GitHub REST address.
func Fetch(ctx context.Context, c Doer, apiBase string) (Release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(apiBase, "/")+"/repos/actions/runner/releases/latest", nil)
	if err != nil {
		return Release{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	res, err := c.Do(req)
	if err != nil {
		return Release{}, err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return Release{}, fmt.Errorf("GitHub answered %d for the newest runner release", res.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		return Release{}, err
	}
	var j struct {
		Tag       string    `json:"tag_name"`
		Published time.Time `json:"published_at"`
		Body      string    `json:"body"`
	}
	if err := json.Unmarshal(b, &j); err != nil {
		return Release{}, err
	}
	if j.Tag == "" {
		return Release{}, errors.New("GitHub sent no runner release")
	}
	return ParseRelease(j.Tag, j.Published, j.Body), nil
}

// KV keeps the stored pins (the settings table).
type KV interface {
	Get(ctx context.Context, key string) (string, bool, error)
	Set(ctx context.Context, key, value string) error
}

// Manager runs the daily check.
type Manager struct {
	Fetch func(ctx context.Context) (Release, error)
	KV    KV
	Now   func() time.Time
	// MoveAfter is how old a release is before the pin moves; zero means 7 days.
	MoveAfter time.Duration
	// Audit writes one line to the audit log; may be nil.
	Audit func(ctx context.Context, action, detail string)
	// Moved runs after a move, for the persistent runners to update (drain, R5.14); may be nil.
	Moved func(ctx context.Context)

	mu     sync.Mutex
	latest Release
	known  bool
}

func (m *Manager) now() time.Time {
	if m.Now != nil {
		return m.Now().UTC()
	}
	return time.Now().UTC()
}

// Load puts the stored pins in force; call it once at start.
func (m *Manager) Load(ctx context.Context) error {
	raw, ok, err := m.KV.Get(ctx, SettingKey)
	if err != nil || !ok || raw == "" {
		return err
	}
	ps, err := runners.DecodePins(raw)
	if err != nil {
		return err
	}
	runners.SetStoredPins(ps)
	return nil
}

// Tick is the daily check: read the newest release, decide, keep and apply.
func (m *Manager) Tick(ctx context.Context) (Decision, error) {
	rel, err := m.Fetch(ctx)
	if err != nil {
		return Decision{}, err
	}
	m.mu.Lock()
	m.latest, m.known = rel, true
	m.mu.Unlock()
	after := m.MoveAfter
	if after <= 0 {
		after = DefaultMoveAfterDays * 24 * time.Hour
	}
	d := Decide(runners.AllPins(), rel, m.now(), after)
	if !d.Move {
		return d, nil
	}
	enc, err := runners.EncodePins(d.Pins)
	if err != nil {
		return d, err
	}
	if err := m.KV.Set(ctx, SettingKey, enc); err != nil {
		return d, err
	}
	runners.SetStoredPins(d.Pins)
	if m.Audit != nil {
		m.Audit(ctx, "runner.pin.move", d.From+" to "+d.To)
	}
	if m.Moved != nil {
		m.Moved(ctx)
	}
	return d, nil
}

// Status is what the doctor reads: the pin in force, the newest release seen
// and how long the pin has been behind. Known is false before the first check.
type Status struct {
	Known      bool
	Pinned     string
	Latest     string
	BehindDays int
}

// Status reports the last check.
func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur := runners.AllPins()
	oldest := ""
	for _, p := range cur {
		if oldest == "" || Compare(p.Version, oldest) < 0 {
			oldest = p.Version
		}
	}
	if !m.known {
		return Status{Pinned: oldest}
	}
	return Status{Known: true, Pinned: oldest, Latest: m.latest.Version, BehindDays: BehindDays(cur, m.latest, m.now())}
}

// Run checks once after the first delay and then once a day until the context ends.
func (m *Manager) Run(ctx context.Context, first time.Duration, onError func(error)) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(first):
	}
	t := time.NewTicker(24 * time.Hour)
	defer t.Stop()
	for {
		if _, err := m.Tick(ctx); err != nil && onError != nil {
			onError(err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
