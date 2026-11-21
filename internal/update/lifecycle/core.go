// Package lifecycle is the update state machine of crsp: the contract's states, the check
// schedule, the install policies, the retry rule after a failed self-test, and the audit rows
// (dev-standards lifecycle contract, DS235c). It reads no clock and starts no goroutine: the
// caller sets Now (a fake clock in tests, the real one through Tick in the service).
// The manifest check is in manifest.go; the install itself is the Env's.
package lifecycle

import (
	"crypto/ed25519"
	"errors"
	"math/rand"
	"strconv"
	"strings"
)

const day = 86400.0

// WaitLimit is how long an automatic update waits for running jobs (seconds).
const WaitLimit = 3600.0

var (
	backoff = []float64{60, 300, 1800, 7200} // failed-check backoff
	retries = []float64{3600, 14400}         // retry after a failed self-test
)

// Policies.
const (
	PolicyAutomatic = "automatic_when_idle"
	PolicyAskFirst  = "ask_first"
)

// Env is what the updater needs from the machine. Tests give a fake server; the service gives GitHub.
type Env interface {
	// Get fetches the signed manifest. A non-200 reply is an error.
	Get(url string) ([]byte, error)
	// Package downloads the package of m and returns its SHA-256 (hex). The service also runs
	// its own checks here (SHA256SUMS, build provenance); a failure is a refusal.
	Package(m Manifest) (sha256 string, err error)
	// Install runs the install of the verified package and its self-test. done false means the
	// install was handed to a helper that restarts the service; Resume reports the outcome after.
	// A non-nil error with done true is a failed install or self-test: the old version is back.
	Install(m Manifest, actor string, remote bool) (done bool, err error)
}

// Sink receives every audit row. A nil Sink drops them.
type Sink interface{ Audit(Row) }

// Window is a daily install window in UTC, "HH:MM".
type Window struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

// Move is one entry of the state history.
type Move struct {
	From string  `json:"from"`
	To   string  `json:"to"`
	At   float64 `json:"at"`
}

// Row is one audit row.
type Row struct {
	At      float64 `json:"at"`
	Kind    string  `json:"kind"` // check, install, refused, rollback, skip
	Actor   string  `json:"actor"`
	Result  string  `json:"result"`
	Detail  string  `json:"detail"`
	Remote  bool    `json:"remote"`
	Version string  `json:"version,omitempty"`
}

// Updater holds all state. It is not safe for concurrent use; callers lock.
type Updater struct {
	State, Version       string
	Paired, Idle         bool
	Policy, Channel      string
	Window               *Window
	Now                  float64
	NextCheckAt, RetryAt *float64
	History              []Move
	Rows                 []Row
	LastCheck            *float64
	LastResult           *string
	Offered              *Manifest
	Failures             int

	Env  Env
	Sink Sink
	// ManifestURL is the signed manifest address; "<server>/<channel>/manifest.json" when empty.
	Server string
	Pub    ed25519.PublicKey
	// Product is the manifest's product name; empty accepts any.
	Product string

	rng         *rand.Rand
	backoffStep int
	waitingFrom *float64

	actor         string
	pendingActor  string
	pendingRemote bool
}

func ptr(f float64) *float64 { return &f }

func (u *Updater) go_(to string) {
	u.History = append(u.History, Move{u.State, to, u.Now})
	u.State = to
	if to == "waiting" {
		if u.waitingFrom == nil {
			u.waitingFrom = ptr(u.Now)
		}
	} else {
		u.waitingFrom = nil
	}
}

func (u *Updater) row(kind, result, actor, detail string, remote bool) {
	r := Row{At: u.Now, Kind: kind, Actor: actor, Result: result, Detail: detail, Remote: remote}
	if u.Offered != nil {
		r.Version = u.Offered.Version
	}
	u.Rows = append(u.Rows, r)
	if u.Sink != nil {
		u.Sink.Audit(r)
	}
}

func (u *Updater) who() string {
	if u.actor != "" {
		return u.actor
	}
	return "updater"
}

func (u *Updater) result(s string) { u.LastResult = &s }

func (u *Updater) firstCheck() *float64 { return ptr(u.Now + 60 + u.rng.Float64()*540) }

// New returns an updater at version, with a seeded random source for the check jitter.
func New(version string, paired bool, policy string, w *Window, channel string, idle bool, now float64, server string, pub ed25519.PublicKey, seed int64, env Env, sink Sink) *Updater {
	u := &Updater{Version: version, Paired: paired, Policy: policy, Window: w, Channel: channel, Idle: idle, Now: now,
		Server: server, Pub: pub, rng: rand.New(rand.NewSource(seed)), History: []Move{}, Rows: []Row{}, Env: env, Sink: sink}
	u.State = "unpaired"
	if paired {
		u.State = "up_to_date"
		u.NextCheckAt = u.firstCheck()
	}
	return u
}

// SetPaired pairs or unpairs the machine. Unpairing stops every timer (LC-28).
func (u *Updater) SetPaired(p bool) {
	u.Paired = p
	switch {
	case p && u.State == "unpaired":
		u.go_("up_to_date")
		u.NextCheckAt = u.firstCheck()
	case !p:
		u.go_("unpaired")
		u.NextCheckAt, u.RetryAt = nil, nil
	}
}

// Check runs one check now. It reports false when unpaired (LC-28).
func (u *Updater) Check() bool { return u.CheckAs("updater") }

// CheckAs is Check by a named actor (the console, the tray, the CLI); the audit row names it.
func (u *Updater) CheckAs(actor string) bool {
	if !u.Paired {
		return false
	}
	u.actor = actor
	u.doCheck()
	u.actor = ""
	return true
}

func (u *Updater) inWindow(t float64) bool {
	if u.Window == nil {
		return true
	}
	m := int(int64(t)%86400) / 60
	a, z := hm(u.Window.Start), hm(u.Window.End)
	if a <= z {
		return a <= m && m < z
	}
	return m >= a || m < z
}

func hm(s string) int {
	p := strings.SplitN(s, ":", 2)
	if len(p) != 2 {
		return 0
	}
	h, _ := strconv.Atoi(p[0])
	m, _ := strconv.Atoi(p[1])
	return h*60 + m
}

func (u *Updater) windowOpen() *float64 {
	if u.State != "waiting" || u.Window == nil {
		return nil
	}
	start := float64(hm(u.Window.Start) * 60)
	d := u.Now - float64(int64(u.Now)%86400)
	for _, t := range []float64{d + start, d + day + start} {
		if t > u.Now {
			return ptr(t)
		}
	}
	return nil
}

// Advance moves the clock by seconds and runs every timer that falls due, in order, each at its own due time.
func (u *Updater) Advance(seconds float64) { u.Tick(u.Now + seconds) }

// Tick moves the clock to target (never back) and runs every timer due on the way.
func (u *Updater) Tick(target float64) {
	if target < u.Now {
		return
	}
	for {
		kind, at := "", 0.0
		for _, c := range []struct {
			k string
			t *float64
		}{{"check", u.NextCheckAt}, {"retry", u.RetryAt}, {"window", u.windowOpen()}} {
			if c.t != nil && *c.t <= target && (kind == "" || *c.t < at) {
				kind, at = c.k, *c.t
			}
		}
		if kind == "" {
			break
		}
		if at > u.Now {
			u.Now = at
		}
		switch kind {
		case "check":
			u.doCheck()
		case "retry":
			u.RetryAt = nil
			if u.Offered != nil && u.Offered.URL == "" {
				u.doCheck() // restored after a restart: fetch the manifest again first
			} else {
				u.decide()
			}
		default:
			u.decide()
		}
	}
	u.Now = target
}

func (u *Updater) doCheck() {
	u.LastCheck = ptr(u.Now)
	url := u.Server + "/" + u.Channel + "/manifest.json"
	raw, err := u.Env.Get(url)
	if err != nil {
		step := u.backoffStep
		if step > len(backoff)-1 {
			step = len(backoff) - 1
		}
		u.NextCheckAt = ptr(u.Now + backoff[step])
		u.backoffStep++
		u.result("failed")
		u.row("check", "failed", u.who(), "", false)
		return
	}
	u.backoffStep = 0
	u.NextCheckAt = ptr(u.Now + 21600*(0.8+0.4*u.rng.Float64()))
	u.row("check", "ok", u.who(), "", false)
	m, bad := ParseManifest(raw, u.Pub, u.Product, u.Now, u.Version)
	if bad != "" {
		u.refuse(bad)
		return
	}
	if m.Version == u.Version {
		u.result("up_to_date")
		u.go_("up_to_date")
		return
	}
	same := u.Offered != nil && u.Offered.Version == m.Version
	if same && u.State == "skipped" {
		return
	}
	if same && u.State == "rolled_back" && u.RetryAt != nil {
		return
	}
	if !same {
		u.Failures, u.RetryAt = 0, nil
	}
	u.Offered = m
	u.result("available")
	u.go_("available")
	u.decide()
}

func (u *Updater) refuse(reason string) {
	u.go_("refused")
	u.result("refused")
	u.row("refused", "refused", "updater", reason, false)
}

// decide applies the policy to the offered version (LC-6).
func (u *Updater) decide() {
	switch {
	case u.Offered.Required:
		u.download("updater", false)
	case u.Policy == PolicyAskFirst:
		u.go_("review")
	case u.Idle && u.inWindow(u.Now):
		u.download("updater", false)
	default:
		u.go_("waiting")
	}
}

// WaitedTooLong says an automatic update has waited for running jobs past WaitLimit; the
// service then lets the drain give up and the next check offers the version again.
func (u *Updater) WaitedTooLong() bool {
	return u.waitingFrom != nil && u.Now-*u.waitingFrom >= WaitLimit
}

// WaitingForJobs says the update waits for running work on this machine.
func (u *Updater) WaitingForJobs() bool { return u.State == "waiting" && !u.Idle }

// Reeval runs the policy again after idle, policy or pairing changed.
func (u *Updater) Reeval() {
	if u.State == "waiting" && u.Offered != nil {
		u.decide()
	}
}

// Install starts an install by a click or by a remote owner (LC-25).
func (u *Updater) Install(actor string, remote, click bool) {
	switch u.State {
	case "review", "waiting", "available":
		if u.Offered != nil && (click || remote) {
			u.download(actor, remote)
		}
	}
}

func (u *Updater) download(actor string, remote bool) {
	m := u.Offered
	u.go_("downloading")
	sum, err := u.Env.Package(*m)
	if err != nil {
		reason := "download_failed"
		var r interface{ Refusal() string }
		if errors.As(err, &r) {
			reason = r.Refusal()
		}
		u.refuse(reason)
		return
	}
	u.go_("verifying")
	if strings.ToLower(sum) != strings.ToLower(m.SHA256) {
		u.refuse("bad_hash")
		return
	}
	u.go_("installing")
	done, err := u.Env.Install(*m, actor, remote)
	if !done {
		u.pendingActor, u.pendingRemote = actor, remote
		return // the helper restarts the service; Resume reports the outcome
	}
	u.finish(actor, remote, err == nil)
}

// Resume reports the outcome of an install that a helper ran across a restart.
func (u *Updater) Resume(ok bool) {
	if u.State != "installing" || u.Offered == nil {
		return
	}
	u.finish(u.pendingActor, u.pendingRemote, ok)
}

func (u *Updater) finish(actor string, remote, ok bool) {
	m := u.Offered
	u.go_("testing")
	if ok {
		u.Version = m.Version
		u.go_("updated")
		u.result("updated")
		u.row("install", "ok", actor, "", remote)
		u.Offered = nil
		return
	}
	u.go_("rolling_back")
	u.go_("rolled_back")
	u.result("rolled_back")
	u.row("install", "rolled_back", actor, "", remote)
	u.row("rollback", "ok", actor, "", remote)
	u.Failures++
	if u.Failures >= 3 {
		u.row("skip", "ok", "updater", m.Version, false)
		u.go_("skipped")
		return
	}
	u.RetryAt = ptr(u.Now + retries[u.Failures-1])
}

// Restore puts the state back after a restart that ended an install: updated (ok) or, after a
// failed self-test, rolled_back with the failure count of version, or skipped after the third.
// failedAt is the epoch second of the failure. It writes no audit row.
func (u *Updater) Restore(version string, ok bool, failures int, failedAt float64) {
	if ok {
		u.History = append(u.History, Move{u.State, "updated", u.Now})
		u.State = "updated"
		u.result("updated")
		return
	}
	u.Offered = &Manifest{Version: version}
	u.Failures = failures
	u.result("rolled_back")
	if failures >= len(retries)+1 {
		u.History = append(u.History, Move{u.State, "skipped", u.Now})
		u.State = "skipped"
		return
	}
	u.History = append(u.History, Move{u.State, "rolled_back", u.Now})
	u.State = "rolled_back"
	u.RetryAt = ptr(failedAt + retries[failures-1])
}
