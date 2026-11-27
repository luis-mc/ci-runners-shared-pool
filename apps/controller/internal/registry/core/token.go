package core

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry"
)

// tokenPrefix tells a registry token from other bearer values.
const tokenPrefix = "crsp1."

// maxScopes bounds one token request.
const maxScopes = 32

// ErrBadToken is an expired, forged or revoked token.
var ErrBadToken = errors.New("registry: invalid token")

// ErrScope is a scope a machine may not have.
var ErrScope = errors.New("registry: scope not allowed")

// Leases keeps the token ids that are alive, so GC and delete wait for them and
// a token can be revoked (R5.10). It never holds a token value.
type Leases interface {
	Lease(ctx context.Context, jti, machineID string, names []string, expires time.Time) error
	Live(ctx context.Context, jti string, now time.Time) (bool, error)
	Revoke(ctx context.Context, jti string) error
}

// PushGate says whether a push may start (R5.8): the free disk floor and the
// kind budget. It returns registry.ErrOverBudget to refuse.
type PushGate interface {
	AllowPush(ctx context.Context, target string) error
}

// Issuer is registry.TokenIssuer: HMAC-signed tokens that last registry.TokenTTL.
type Issuer struct {
	// Secret signs the tokens; at least 32 random bytes, kept in the secret store.
	Secret []byte
	Leases Leases
	// Gate is asked before a push scope is granted. Nil allows.
	Gate PushGate
	// Now is a seam for tests.
	Now func() time.Time
}

var _ registry.TokenIssuer = (*Issuer)(nil)

type claims struct {
	ID      string           `json:"jti"`
	Machine string           `json:"mid"`
	Expires int64            `json:"exp"`
	Scopes  []registry.Scope `json:"scp"`
}

func (i *Issuer) now() time.Time {
	if i.Now != nil {
		return i.Now()
	}
	return time.Now()
}

func (i *Issuer) sign(payload []byte) []byte {
	m := hmac.New(sha256.New, i.Secret)
	m.Write(payload)
	return m.Sum(nil)
}

// classify says where a scope name sits for one machine: its own staging
// (exact name below its root, or the whole root as crsp/incoming/<id>/*), or
// the promoted roots (an exact name below them, or crsp/std/* and crsp/custom/*).
func classify(machineID, name string) (own, promoted bool) {
	if _, isPrefix := strings.CutSuffix(name, "*"); isPrefix {
		return name == ownIncoming(machineID)+"*", name == stdPrefix+"*" || name == customPrefix+"*"
	}
	if !ValidName(name) {
		return false, false
	}
	return OwnStaging(machineID, name), strings.HasPrefix(name, stdPrefix) || strings.HasPrefix(name, customPrefix)
}

// checkWant refuses every scope a machine may not hold: it pulls crsp/std/* and
// crsp/custom/* (and its own staging, to push there), and it pushes only to
// crsp/incoming/<own id>/* (R5.11).
func checkWant(machineID string, want []registry.Scope) error {
	if err := checkMachineID(machineID); err != nil {
		return err
	}
	if len(want) == 0 || len(want) > maxScopes {
		return fmt.Errorf("%w: %d scopes asked", ErrScope, len(want))
	}
	for _, s := range want {
		if len(s.Actions) == 0 {
			return fmt.Errorf("%w: %q has no action", ErrScope, s.Name)
		}
		own, promoted := classify(machineID, s.Name)
		for _, a := range s.Actions {
			switch a {
			case ActionPush:
				if !own {
					return fmt.Errorf("%w: %s on %q (a machine pushes only below %s)", ErrScope, a, s.Name, ownIncoming(machineID))
				}
			case ActionPull:
				if !own && !promoted {
					return fmt.Errorf("%w: %s on %q", ErrScope, a, s.Name)
				}
			default:
				return fmt.Errorf("%w: action %q", ErrScope, a)
			}
		}
	}
	return nil
}

// Issue implements registry.TokenIssuer. The scopes asked are the scopes
// granted, or the whole request is refused. Only the token id, the machine and
// the names are recorded.
func (i *Issuer) Issue(ctx context.Context, machineID string, want []registry.Scope) (registry.Token, error) {
	if len(i.Secret) < 32 {
		return registry.Token{}, errors.New("registry: the token key is too short")
	}
	if err := checkWant(machineID, want); err != nil {
		return registry.Token{}, err
	}
	var names []string
	for _, s := range want {
		names = append(names, s.Name)
		for _, a := range s.Actions {
			if a == ActionPush && i.Gate != nil {
				if err := i.Gate.AllowPush(ctx, s.Name); err != nil {
					return registry.Token{}, err
				}
			}
		}
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return registry.Token{}, err
	}
	now := i.now()
	c := claims{ID: hex.EncodeToString(id[:]), Machine: machineID, Expires: now.Add(registry.TokenTTL).Unix(), Scopes: want}
	payload, err := json.Marshal(c)
	if err != nil {
		return registry.Token{}, err
	}
	if i.Leases != nil {
		if err := i.Leases.Lease(ctx, c.ID, machineID, names, time.Unix(c.Expires, 0).UTC()); err != nil {
			return registry.Token{}, fmt.Errorf("record the token: %w", err)
		}
	}
	value := tokenPrefix + base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(i.sign(payload))
	return registry.Token{Value: value, ExpiresAt: time.Unix(c.Expires, 0).UTC(), Scopes: want}, nil
}

// Verify implements registry.TokenIssuer.
func (i *Issuer) Verify(ctx context.Context, token string) (string, []registry.Scope, error) {
	body, ok := strings.CutPrefix(token, tokenPrefix)
	if !ok {
		return "", nil, ErrBadToken
	}
	p, s, ok := strings.Cut(body, ".")
	if !ok {
		return "", nil, ErrBadToken
	}
	payload, err := base64.RawURLEncoding.DecodeString(p)
	if err != nil {
		return "", nil, ErrBadToken
	}
	sig, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil || len(i.Secret) < 32 || !hmac.Equal(sig, i.sign(payload)) {
		return "", nil, ErrBadToken
	}
	var c claims
	if err := json.Unmarshal(payload, &c); err != nil || c.ID == "" || checkMachineID(c.Machine) != nil {
		return "", nil, ErrBadToken
	}
	now := i.now()
	if !now.Before(time.Unix(c.Expires, 0)) {
		return "", nil, fmt.Errorf("%w: expired", ErrBadToken)
	}
	if i.Leases != nil {
		live, err := i.Leases.Live(ctx, c.ID, now)
		if err != nil {
			return "", nil, err
		}
		if !live {
			return "", nil, fmt.Errorf("%w: revoked", ErrBadToken)
		}
	}
	return c.Machine, c.Scopes, nil
}

// Revoke ends a token before its time (a lease that is gone fails Verify).
func (i *Issuer) Revoke(ctx context.Context, token string) error {
	body, ok := strings.CutPrefix(token, tokenPrefix)
	if !ok || i.Leases == nil {
		return ErrBadToken
	}
	p, _, _ := strings.Cut(body, ".")
	payload, err := base64.RawURLEncoding.DecodeString(p)
	if err != nil {
		return ErrBadToken
	}
	var c claims
	if json.Unmarshal(payload, &c) != nil || c.ID == "" {
		return ErrBadToken
	}
	return i.Leases.Revoke(ctx, c.ID)
}
