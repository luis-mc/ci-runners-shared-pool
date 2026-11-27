// Package recovery is the frozen call that reaches an agent too old for the
// current agent link, or stuck on an old version (operations R5.44).
//
// The call is plain JSON over the agent link port, not protobuf, and its shape
// never changes: a future agent can always speak it. It does one thing: the
// controller hands the agent a signed update target. The agent checks the
// signature against the certificate it pinned at join, then downloads and
// verifies the release exactly as an update does (SHA-256 and attestation,
// operations R5.34, R5.43). Nothing else is reachable through it.
package recovery

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/tlsca"
)

// Path is the one path of the recovery call. It is frozen.
const Path = "/recovery/v1/poll"

// Frozen header names, the same as the agent link's.
const (
	headerMachineID = "Crsp-Machine-Id"
	maxBody         = 4 << 10
	maxAnswer       = 64 << 10
	// DefaultTTL is how long a signed target is good for.
	DefaultTTL = 10 * time.Minute
	magic      = "crsp-recovery-v1"
)

// Request is the agent's poll.
type Request struct {
	MachineID    string `json:"machine_id"`
	AgentVersion string `json:"agent_version"`
	OS           string `json:"os"`
	Arch         string `json:"arch"`
	Nonce        string `json:"nonce"`
}

// Answer actions.
const (
	ActionNone   = "none"
	ActionUpdate = "update"
)

// Answer is what the controller says. For ActionUpdate every field is signed.
type Answer struct {
	Action    string   `json:"action"`
	MachineID string   `json:"machine_id,omitempty"`
	Nonce     string   `json:"nonce,omitempty"`
	Version   string   `json:"version,omitempty"`
	Asset     string   `json:"asset,omitempty"`
	URL       string   `json:"url,omitempty"`
	SHA256    string   `json:"sha256,omitempty"`
	IssuedAt  int64    `json:"issued_at,omitempty"`  // unix seconds
	ExpiresAt int64    `json:"expires_at,omitempty"` // unix seconds
	Signature []byte   `json:"signature,omitempty"`
	Chain     [][]byte `json:"chain,omitempty"`
}

// Message is the exact text that is signed. The download URL is not part of it:
// the agent checks the file against the signed SHA-256, so a changed URL can
// only fail.
func (a Answer) Message() []byte {
	return []byte(strings.Join([]string{magic, a.MachineID, a.Nonce, a.Version, a.Asset, a.SHA256,
		fmt.Sprint(a.IssuedAt), fmt.Sprint(a.ExpiresAt)}, "\n"))
}

// Target is the release an agent is pushed to.
type Target struct {
	Version string
	Asset   string
	URL     string
	SHA256  string
}

// Signer signs the target (tlsca.Authority).
type Signer interface {
	Sign(msg []byte) (sig []byte, chain [][]byte, err error)
}

// Handler is the controller's side.
type Handler struct {
	// Authenticate maps a machine key and id to the machine's id; an error
	// means the key is unknown (machines.Service.Authenticate).
	Authenticate func(ctx context.Context, key, machineID string) (string, error)
	// Armed says the owner pressed the recovery action for this machine and
	// the machine needs recovery; only then a target is handed out.
	Armed func(ctx context.Context, machineID, agentVersion string) bool
	// Target finds the release for this machine's OS and arch.
	Target func(ctx context.Context, req Request) (Target, error)
	Signer Signer
	// Served runs after a signed target was handed out (for the audit).
	Served func(ctx context.Context, machineID string, t Target)
	TTL    time.Duration
	Now    func() time.Time
}

func (h *Handler) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}

// ServeHTTP implements http.Handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "use POST", http.StatusMethodNotAllowed)
		return
	}
	v := strings.TrimSpace(r.Header.Get("Authorization"))
	key := ""
	if len(v) > 7 && strings.EqualFold(v[:7], "bearer ") {
		key = strings.TrimSpace(v[7:])
	}
	id, err := h.Authenticate(r.Context(), key, r.Header.Get(headerMachineID))
	if err != nil || id == "" {
		http.Error(w, "unknown machine", http.StatusUnauthorized)
		return
	}
	var req Request
	if err := json.NewDecoder(io.LimitReader(r.Body, maxBody)).Decode(&req); err != nil || req.Nonce == "" || len(req.Nonce) > 128 {
		http.Error(w, "unreadable request", http.StatusBadRequest)
		return
	}
	req.MachineID = id // the key decides who it is, never the body
	ans := Answer{Action: ActionNone}
	if h.Armed != nil && h.Armed(r.Context(), id, req.AgentVersion) {
		t, err := h.Target(r.Context(), req)
		if err != nil {
			http.Error(w, "no release for this machine", http.StatusBadGateway)
			return
		}
		ttl := h.TTL
		if ttl <= 0 {
			ttl = DefaultTTL
		}
		now := h.now()
		ans = Answer{Action: ActionUpdate, MachineID: id, Nonce: req.Nonce, Version: t.Version, Asset: t.Asset, URL: t.URL,
			SHA256: strings.ToLower(t.SHA256), IssuedAt: now.Unix(), ExpiresAt: now.Add(ttl).Unix()}
		sig, chain, err := h.Signer.Sign(ans.Message())
		if err != nil {
			http.Error(w, "cannot sign", http.StatusInternalServerError)
			return
		}
		ans.Signature, ans.Chain = sig, chain
		if h.Served != nil {
			h.Served(r.Context(), id, t)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(ans)
}

// Errors the agent tells apart.
var (
	ErrRefused   = errors.New("the controller does not know this machine")
	ErrNoUpdate  = errors.New("the controller has no recovery update for this machine")
	ErrExpired   = errors.New("the recovery update is out of date")
	ErrNotForMe  = errors.New("the recovery update names another machine or request")
	ErrBadTarget = errors.New("the recovery update is not valid")
)

// Client is the agent's side.
type Client struct {
	HTTP       *http.Client // pinned TLS client
	Controller string       // host:port of the agent link
	Pin        string       // pinned fingerprint
	MachineID  string
	Key        string
	Now        func() time.Time
}

func (c *Client) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// Poll asks for a signed target and returns it only when it is valid for this
// machine: the signature chains to the pin, the nonce is the one just sent,
// and it has not expired.
func (c *Client) Poll(ctx context.Context, version, goos, arch string) (Target, error) {
	var nb [12]byte
	if _, err := rand.Read(nb[:]); err != nil {
		return Target{}, err
	}
	req := Request{MachineID: c.MachineID, AgentVersion: version, OS: goos, Arch: arch, Nonce: hex.EncodeToString(nb[:])}
	body, err := json.Marshal(req)
	if err != nil {
		return Target{}, err
	}
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+c.Controller+Path, bytes.NewReader(body))
	if err != nil {
		return Target{}, err
	}
	hr.Header.Set("Authorization", "Bearer "+c.Key)
	hr.Header.Set(headerMachineID, c.MachineID)
	hr.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(hr)
	if err != nil {
		return Target{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusUnauthorized {
		return Target{}, ErrRefused
	}
	if resp.StatusCode != http.StatusOK {
		return Target{}, fmt.Errorf("the controller answered %d", resp.StatusCode)
	}
	var ans Answer
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxAnswer)).Decode(&ans); err != nil {
		return Target{}, ErrBadTarget
	}
	return CheckAnswer(ans, c.Pin, c.MachineID, req.Nonce, c.now())
}

// CheckAnswer is every check the agent makes before it trusts a target.
func CheckAnswer(ans Answer, pin, machineID, nonce string, now time.Time) (Target, error) {
	if ans.Action != ActionUpdate {
		return Target{}, ErrNoUpdate
	}
	if ans.MachineID != machineID || ans.Nonce != nonce {
		return Target{}, ErrNotForMe
	}
	if err := tlsca.VerifySigned(pin, ans.Chain, ans.Message(), ans.Signature, now); err != nil {
		return Target{}, fmt.Errorf("%w: %v", ErrBadTarget, err)
	}
	if now.Unix() < ans.IssuedAt-60 || now.Unix() > ans.ExpiresAt {
		return Target{}, ErrExpired
	}
	if ans.Version == "" || ans.Asset == "" || len(ans.SHA256) != 64 || strings.ContainsAny(ans.Asset, `/\`) {
		return Target{}, ErrBadTarget
	}
	return Target{Version: ans.Version, Asset: ans.Asset, URL: ans.URL, SHA256: ans.SHA256}, nil
}
