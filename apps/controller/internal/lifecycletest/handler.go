// Package lifecycletest is the language-free test interface of the updater (lifecycle contract
// DS235c): a fake clock, a fake update server, and the verbs reset, state, check, install,
// policy, audit and pair. Only cmd/crsp-lifecycle-test imports it, so a release build of crsp
// never contains it.
package lifecycletest

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/update/lifecycle"
)

// ErrBadKey is the reply to a reset with a public key that is not an Ed25519 key.
var ErrBadKey = errors.New("bad public key")

// env is the fake machine: a plain HTTP client and a self-test whose result the test sets.
type env struct{ pass bool }

func (e *env) Get(url string) ([]byte, error) {
	c := http.Client{Timeout: 10 * time.Second}
	r, err := c.Get(url) //nolint:noctx // test build, the URL is the fake server
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Body.Close() }()
	if r.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", r.StatusCode)
	}
	return io.ReadAll(r.Body)
}

// Package downloads the package from the fake server and returns its SHA-256.
func (e *env) Package(m lifecycle.Manifest) (string, error) {
	b, err := e.Get(m.URL)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

func (e *env) Install(lifecycle.Manifest, string, bool) (bool, error) {
	if e.pass {
		return true, nil
	}
	return true, errors.New("self-test failed")
}

type sink struct{}

func (sink) Audit(lifecycle.Row) {}

// Handler serves the test verbs at /test/<verb>.
func Handler() http.Handler {
	var mu sync.Mutex
	e := &env{pass: true}
	var u *lifecycle.Updater
	fresh := func() *lifecycle.Updater {
		return lifecycle.New("0.0.0", false, lifecycle.PolicyAutomatic, nil, "stable", true, 0, "", nil, 1, e, sink{})
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		verb := strings.TrimPrefix(r.URL.Path, "/test/")
		var b struct {
			Version   string            `json:"version"`
			Paired    bool              `json:"paired"`
			Policy    string            `json:"policy"`
			Window    *lifecycle.Window `json:"window"`
			Channel   string            `json:"channel"`
			Idle      bool              `json:"idle"`
			Now       float64           `json:"now"`
			ServerURL string            `json:"server_url"`
			URL       string            `json:"url"`
			PublicKey string            `json:"public_key"`
			Seed      int64             `json:"seed"`
			Seconds   float64           `json:"seconds"`
			Pass      bool              `json:"pass"`
			Actor     string            `json:"actor"`
			Remote    bool              `json:"remote"`
			Click     bool              `json:"click"`
		}
		_ = json.NewDecoder(r.Body).Decode(&b)
		mu.Lock()
		defer mu.Unlock()
		if u == nil {
			u = fresh()
		}
		out := func(v interface{}) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(v)
		}
		switch verb {
		case "reset":
			pub, err := hex.DecodeString(b.PublicKey)
			if err != nil || len(pub) != ed25519.PublicKeySize {
				out(map[string]string{"error": ErrBadKey.Error()})
				return
			}
			e.pass = true
			u = lifecycle.New(b.Version, b.Paired, b.Policy, b.Window, b.Channel, b.Idle, b.Now, b.ServerURL, pub, b.Seed, e, sink{})
		case "server.set":
			pub, _ := hex.DecodeString(b.PublicKey)
			u.Server, u.Pub = b.URL, pub
			if b.ServerURL != "" {
				u.Server = b.ServerURL
			}
		case "clock.set":
			u.Now = b.Now
		case "clock.advance":
			u.Advance(b.Seconds)
		case "idle.set":
			u.Idle = b.Idle
			u.Reeval()
		case "policy.set":
			u.Policy, u.Window = b.Policy, b.Window
			u.Reeval()
		case "selftest.set":
			e.pass = b.Pass
		case "pair":
			u.SetPaired(b.Paired)
		case "install":
			u.Install(b.Actor, b.Remote, b.Click)
		case "check":
			ran := u.Check()
			out(map[string]interface{}{"ran": ran, "state": view(u)})
			return
		case "audit":
			out(map[string]interface{}{"rows": u.Rows})
			return
		case "settings":
			out(map[string]interface{}{"policy": u.Policy, "window": u.Window, "channel": u.Channel,
				"last_check": u.LastCheck, "last_result": u.LastResult})
			return
		case "state":
		default:
			out(map[string]string{"error": "unknown verb " + verb})
			return
		}
		out(view(u))
	})
}

func view(u *lifecycle.Updater) map[string]interface{} {
	var offered interface{}
	if u.Offered != nil {
		offered = u.Offered.Version
	}
	return map[string]interface{}{"state": u.State, "version": u.Version, "paired": u.Paired, "policy": u.Policy,
		"window": u.Window, "channel": u.Channel, "idle": u.Idle, "now": u.Now, "next_check_at": u.NextCheckAt,
		"retry_at": u.RetryAt, "failures": u.Failures, "offered": offered, "history": u.History}
}
