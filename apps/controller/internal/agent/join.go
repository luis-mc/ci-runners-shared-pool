package agent

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"connectrpc.com/connect"

	agentv1 "github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/gen/agent/v1"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/gen/agent/v1/agentv1connect"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/secrets"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/tlsca"
)

// ErrPinMismatch: the controller's certificate is not the pinned one. The agent
// stops and says so; it never joins a controller it cannot verify (R5.5).
var ErrPinMismatch = errors.New("the controller certificate does not match the fingerprint in the install command; the agent did not join")

// pinnedClient is an HTTP/2 client that checks the pinned certificate on every
// connection (R5.8, R6.1) and dials out only.
func pinnedClient(pin string) *http.Client {
	return &http.Client{Transport: &http.Transport{
		ForceAttemptHTTP2:   true,
		TLSClientConfig:     tlsca.ClientTLS(pin),
		TLSHandshakeTimeout: 10 * time.Second,
		IdleConnTimeout:     90 * time.Second,
	}}
}

func newClient(pin, controller string) agentv1connect.AgentServiceClient {
	return agentv1connect.NewAgentServiceClient(pinnedClient(pin), "https://"+controller, connect.WithGRPC())
}

func isPinError(err error) bool {
	return errors.Is(err, tlsca.ErrPinMismatch) || (err != nil && strings.Contains(err.Error(), tlsca.ErrPinMismatch.Error()))
}

// Join trades a join code (or the local credential) for a machine key, checks
// the controller certificate against pin, and keeps the key, the machine id and
// the pin in the secret store (R5.5 to R5.7). If the secret store is down the
// join fails with the reason; a key is never written to a plain file except by
// FileStore's 0600 files (spec section 8).
func Join(ctx context.Context, cfg Config, code string, local bool) error {
	if cfg.Secrets == nil {
		return errors.New("agent: a secret store is required")
	}
	// Fail before asking the controller to issue a key we cannot keep.
	if _, err := cfg.Secrets.Get(ctx, MachineKeySecret); err != nil && !errors.Is(err, secrets.ErrNotFound) {
		return fmt.Errorf("cannot join: %w", err)
	}
	facts := DetectAll(ctx, cfg.sys(), cfg.DataDir, time.Now())
	req := &agentv1.JoinRequest{Facts: facts, AgentVersion: cfg.Version, ProtocolVersion: agentv1.ProtocolVersion_PROTOCOL_VERSION_1}
	if local {
		req.Credential = &agentv1.JoinRequest_LocalCredential{LocalCredential: code}
	} else {
		req.Credential = &agentv1.JoinRequest_JoinCode{JoinCode: code}
	}
	pin := strings.ToLower(strings.TrimSpace(cfg.Pin))
	resp, err := newClient(pin, cfg.Controller).Join(ctx, connect.NewRequest(req))
	if err != nil {
		if isPinError(err) {
			return ErrPinMismatch
		}
		// Stopped while dialling: say so, not "the controller refused".
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		var ce *connect.Error
		if errors.As(err, &ce) {
			return fmt.Errorf("the controller refused the join: %s", ce.Message())
		}
		return fmt.Errorf("cannot reach the controller: %w", err)
	}
	m := resp.Msg
	if !strings.EqualFold(m.GetCertSha256(), pin) {
		return ErrPinMismatch
	}
	for k, v := range map[secrets.Key]string{MachineKeySecret: m.GetMachineCredential(), MachineIDSecret: m.GetMachineId(), PinnedCertSecret: pin} {
		if err := cfg.Secrets.Put(ctx, k, []byte(v)); err != nil {
			return fmt.Errorf("joined, but the key could not be kept: %w", err)
		}
	}
	if m.GetSettings() != nil {
		_ = cfg.cache().Save(SettingsFromProto(m.GetSettings()), m.GetSettings().GetState())
	}
	return nil
}

func (c Config) loadIdentity(ctx context.Context) (key, id, pin string, err error) {
	get := func(k secrets.Key) (string, error) {
		b, err := c.Secrets.Get(ctx, k)
		return string(b), err
	}
	if key, err = get(MachineKeySecret); err != nil {
		return "", "", "", err
	}
	if id, err = get(MachineIDSecret); err != nil {
		return "", "", "", err
	}
	if pin, err = get(PinnedCertSecret); err != nil {
		return "", "", "", err
	}
	return key, id, pin, nil
}

func (c Config) forget(ctx context.Context) {
	for _, k := range []secrets.Key{MachineKeySecret, MachineIDSecret, PinnedCertSecret} {
		_ = c.Secrets.Delete(ctx, k)
	}
}
