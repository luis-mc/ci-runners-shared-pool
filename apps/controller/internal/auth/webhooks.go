package auth

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/github"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/secrets"
)

// WebhookPath is the route GitHub posts deliveries to (R6.5).
const WebhookPath = "/webhooks/github"

// SelfTestTimeout bounds the signed ping to the public address (OPS25).
const SelfTestTimeout = 10 * time.Second

// WebhookState is what the console says about the App's webhook.
type WebhookState string

// Webhook states.
const (
	WebhookOff     WebhookState = "off"
	WebhookOn      WebhookState = "on"
	WebhookFailing WebhookState = "failing"
)

// WebhookStatus is the webhook section of the settings (github-integration R5.51).
type WebhookStatus struct {
	State   WebhookState
	Address string
	// LastDeliveryAt and LastResult are the newest delivery of any repository;
	// zero and "" before the first one.
	LastDeliveryAt time.Time
	LastResult     string
	// EventsLink is the App's permissions page at GitHub, set when the App
	// does not subscribe to workflow_job and the owner must tick it there.
	EventsLink string
}

// WebhookHealth gives the newest delivery of any repository (webhookhealth).
type WebhookHealth interface {
	Last() (at time.Time, result string)
}

// ValidateWebhookAddress checks the public address and returns it without a
// trailing slash. It must be https with a real public name: a LAN or loopback
// address cannot be reached by GitHub (OPS25, R5.51).
func ValidateWebhookAddress(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", Invalid("the address is not a web address. Write it like https://hooks.example.com")
	}
	if u.Scheme != "https" {
		return "", Invalid("the address must start with https://. GitHub does not send webhooks to plain http.")
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", Invalid("write the address without a user name, query or #fragment.")
	}
	host := strings.ToLower(u.Hostname())
	if privateHost(host) {
		return "", Invalid("%s is a local address. GitHub can only reach a public address, for example a tunnel or a reverse proxy with a real certificate.", host)
	}
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawPath = ""
	return u.String(), nil
}

func privateHost(host string) bool {
	if ip := net.ParseIP(host); ip != nil {
		return blockedIP(ip)
	}
	if host == "localhost" || !strings.Contains(host, ".") {
		return true
	}
	for _, sfx := range []string{".localhost", ".local", ".lan", ".internal", ".home.arpa", ".intranet", ".corp"} {
		if strings.HasSuffix(host, sfx) {
			return true
		}
	}
	return false
}

func blockedIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified() || ip.IsMulticast()
}

// NewSelfTestClient is the HTTP client of the self-test: it checks the
// certificate (a self-signed one fails, as it does at GitHub) and refuses to
// connect to a loopback, LAN or link-local address, so the test cannot be
// aimed at the controller's own network.
func NewSelfTestClient() *http.Client {
	d := &net.Dialer{Timeout: SelfTestTimeout, Control: func(_, address string, _ syscall.RawConn) error {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return err
		}
		if ip := net.ParseIP(host); ip == nil || blockedIP(ip) {
			return errors.New("the address points inside this network")
		}
		return nil
	}}
	return &http.Client{
		Timeout:   SelfTestTimeout,
		Transport: &http.Transport{DialContext: d.DialContext, TLSHandshakeTimeout: SelfTestTimeout},
		// A redirect would move the test to another address.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func newWebhookSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// selfTest posts a signed ping to <address>/webhooks/github and says in plain
// words why it failed (OPS25). The controller's own handler must accept the
// signature, so a success proves the address reaches this controller.
func (s *Service) selfTest(ctx context.Context, address, secret string) error {
	body := []byte(`{"zen":"crsp webhook self-test","hook_id":0}`)
	id, err := newWebhookSecret()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, SelfTestTimeout)
	defer cancel()
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, address+WebhookPath, bytes.NewReader(body))
	if err != nil {
		return Invalid("the address is not usable.")
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("User-Agent", "crsp-webhook-selftest")
	r.Header.Set("X-GitHub-Event", "ping")
	r.Header.Set("X-GitHub-Delivery", id[:36])
	r.Header.Set("X-Hub-Signature-256", github.Sign([]byte(secret), body))
	res, err := s.selfTestClient().Do(r)
	if err != nil {
		return Invalid("the controller could not reach %s: %s", address, selfTestReason(err))
	}
	defer func() { _ = res.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 4096))
	switch res.StatusCode {
	case http.StatusOK:
		return nil
	case http.StatusUnauthorized:
		return Invalid("%s answered, but not this controller: it did not accept the signed test. Check that the address forwards to this controller.", address)
	case http.StatusNotFound:
		return Invalid("%s answered 404: nothing there serves %s. Forward the address to this controller.", address, WebhookPath)
	default:
		return Invalid("%s answered %d to the test. It must forward to this controller.", address, res.StatusCode)
	}
}

func (s *Service) selfTestClient() *http.Client {
	if s.cfg.SelfTestHTTP != nil {
		return s.cfg.SelfTestHTTP
	}
	return NewSelfTestClient()
}

func selfTestReason(err error) string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "certificate") || strings.Contains(msg, "x509"):
		return "its certificate is not trusted (a self-signed certificate does not work; use a real one)."
	case strings.Contains(msg, "points inside this network"):
		return "it points inside this network. GitHub needs a public address."
	case errors.Is(err, context.DeadlineExceeded) || strings.Contains(msg, "timeout") || strings.Contains(msg, "deadline"):
		return "no answer in 10 seconds."
	case strings.Contains(msg, "no such host"):
		return "the name does not exist."
	default:
		return "the connection failed."
	}
}

// hookKey is the secret-store key of the App's webhook secret.
func (s *Service) hookKey(appID int64) secrets.Key {
	return secretKey(appID, github.SecretWebhookSecret)
}

// WebhookStatus reads the section's state (R5.51). It never returns the secret.
func (s *Service) WebhookStatus(ctx context.Context) (WebhookStatus, error) {
	app, err := s.store.GetApp(ctx)
	if err != nil {
		return WebhookStatus{State: WebhookOff}, nil
	}
	st := WebhookStatus{State: WebhookOff}
	sec, serr := s.cfg.Secrets.Get(ctx, s.hookKey(app.ID))
	if serr != nil || len(sec) == 0 {
		return st, nil
	}
	st.State = WebhookOn
	if s.cfg.LoadWebhookAddress != nil {
		st.Address, _ = s.cfg.LoadWebhookAddress(ctx)
	}
	if s.cfg.HookHealth != nil {
		at, res := s.cfg.HookHealth.Last()
		st.LastDeliveryAt, st.LastResult = at, res
		if res == "failed" || res == "missed" {
			st.State = WebhookFailing
		}
	}
	st.EventsLink = s.eventsLink(ctx, app)
	return st, nil
}

// eventsLink is the App's permissions page when the App does not subscribe to
// workflow_job. GitHub has no API to change the events of an App, so the
// owner ticks it there (OPS25). Empty when the App subscribes or GitHub does
// not say.
func (s *Service) eventsLink(ctx context.Context, app AppRecord) string {
	a, err := s.cfg.GitHub.GetApp(ctx)
	if err != nil || a.Events == nil {
		return ""
	}
	for _, e := range a.Events {
		if e == "workflow_job" {
			return ""
		}
	}
	base := SafeAppURL(app.HTMLURL)
	if base == "" {
		return ""
	}
	return strings.TrimRight(base, "/") + "/permissions"
}

// SetWebhook turns real webhooks on (owner, OPS25): it makes a secret, keeps
// it in the secret store, sends a signed test to the address, and only then
// sets the App's webhook at GitHub. Any failure puts the old secret back and
// leaves the App as it was.
func (s *Service) SetWebhook(ctx context.Context, p Principal, rawAddress string) (WebhookStatus, error) {
	if err := Require(p, RoleOwner); err != nil {
		return WebhookStatus{}, err
	}
	app, err := s.store.GetApp(ctx)
	if err != nil {
		return WebhookStatus{}, ErrNoApp
	}
	addr, err := ValidateWebhookAddress(rawAddress)
	if err != nil {
		return WebhookStatus{}, err
	}
	s.keyMu.Lock()
	defer s.keyMu.Unlock()

	fail := func(reason string, err error) (WebhookStatus, error) {
		_ = s.store.Audit(ctx, AuditEvent{Actor: p.Login, ActorID: fmt.Sprint(p.UserID), Source: p.Source,
			Action: "app.webhook_failed", TargetType: "github_app", Subject: app.Slug, Detail: scrub(reason), Result: "failed"})
		return WebhookStatus{}, err
	}
	secret, err := newWebhookSecret()
	if err != nil {
		return fail("no random secret", err)
	}
	name := s.hookKey(app.ID)
	old, oldErr := s.cfg.Secrets.Get(ctx, name)
	restore := func() {
		if oldErr == nil && len(old) > 0 {
			_ = s.cfg.Secrets.Put(ctx, name, old)
		} else {
			_ = s.cfg.Secrets.Delete(ctx, name)
		}
	}
	if err := s.cfg.Secrets.Put(ctx, name, []byte(secret)); err != nil {
		restore()
		return fail("the secret store refused the webhook secret", fmt.Errorf("storing the webhook secret: %w", err))
	}
	if err := s.selfTest(ctx, addr, secret); err != nil {
		restore()
		return fail("self-test failed", err)
	}
	if err := s.cfg.GitHub.SetAppHook(ctx, github.AppHook{URL: addr + WebhookPath, Secret: secret}); err != nil {
		restore()
		return fail("GitHub did not take the webhook", err)
	}
	if s.cfg.SaveWebhookAddress != nil {
		_ = s.cfg.SaveWebhookAddress(ctx, addr)
	}
	app.WebhookActive = true
	_ = s.store.SaveApp(ctx, app)
	s.auditUser(ctx, p, "app.webhook_on", "github_app", app.Slug, addr)
	return s.WebhookStatus(ctx)
}

// ClearWebhook turns webhooks off (owner, OPS25): GitHub's hook config is
// emptied first, then the secret is removed, and jobs are learned by polling.
func (s *Service) ClearWebhook(ctx context.Context, p Principal) (WebhookStatus, error) {
	if err := Require(p, RoleOwner); err != nil {
		return WebhookStatus{}, err
	}
	app, err := s.store.GetApp(ctx)
	if err != nil {
		return WebhookStatus{}, ErrNoApp
	}
	s.keyMu.Lock()
	defer s.keyMu.Unlock()
	if err := s.cfg.GitHub.ClearAppHook(ctx); err != nil {
		_ = s.store.Audit(ctx, AuditEvent{Actor: p.Login, ActorID: fmt.Sprint(p.UserID), Source: p.Source,
			Action: "app.webhook_failed", TargetType: "github_app", Subject: app.Slug, Detail: "GitHub did not clear the webhook", Result: "failed"})
		return WebhookStatus{}, err
	}
	if err := s.cfg.Secrets.Delete(ctx, s.hookKey(app.ID)); err != nil {
		return WebhookStatus{}, fmt.Errorf("removing the webhook secret: %w", err)
	}
	if s.cfg.SaveWebhookAddress != nil {
		_ = s.cfg.SaveWebhookAddress(ctx, "")
	}
	app.WebhookActive = false
	_ = s.store.SaveApp(ctx, app)
	s.auditUser(ctx, p, "app.webhook_off", "github_app", app.Slug, "")
	return WebhookStatus{State: WebhookOff}, nil
}
