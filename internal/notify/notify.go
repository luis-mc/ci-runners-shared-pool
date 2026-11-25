// Package notify holds the notification channels and sends a test notice
// through one of them (notifications R5.7 to R5.9, R5.19 to R5.22, R5.29 to
// R5.31; web-console 4.9).
//
// A channel is configuration: its kind (email over SMTP, a signed webhook, a
// desktop notice), the events it carries and the lowest severity it gets. The
// webhook signing secret and the SMTP password are written only to the OS
// secret store (internal/secrets). They are never stored in the database,
// returned by the API, logged or put in the audit log; a channel only says
// whether one is set (R6.2).
//
// It also makes real notices (CRSP258): sources tell the engine what is true
// (engine.go), a change of an event key's state makes one notice and its
// deliveries in one transaction, a worker sends them with retries (queue.go,
// deliver.go) and the notices list reads them back (list.go). The tray notice
// stream and the mail provider adapter are not built (section 11 of the spec).
package notify

import (
	"context"
	"crypto/rand"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/mail"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/luis-mc/ci-runners-shared-pool/internal/audit"
	"github.com/luis-mc/ci-runners-shared-pool/internal/secrets"
	"github.com/luis-mc/ci-runners-shared-pool/internal/store"
)

// Channel kinds.
const (
	KindEmail   = "email"
	KindWebhook = "webhook"
	KindDesktop = "desktop"
)

// Channel scopes: email and desktop belong to one user, a webhook to the pool
// (R5.7, R5.9).
const (
	ScopePersonal = "personal"
	ScopePool     = "pool"
)

// Events are the stable event types of R5.1, in the order the console shows them.
var Events = []string{"machine.offline", "job.waiting", "disk.low", "runner.outdated", "doctor.finding", "update.available"}

// Severities, lowest first (R5.4).
var Severities = []string{"info", "warning", "error"}

// DefaultSeverity is the lowest severity a channel gets unless told otherwise (R5.4).
const DefaultSeverity = "warning"

// TestMinGap is the least time between two tests of one channel by one user (R5.31).
const TestMinGap = 10 * time.Second

// MinSecretLen is the shortest webhook signing secret accepted.
const MinSecretLen = 16

const (
	maxName = 80
	maxAddr = 254
	maxURL  = 2048
)

// Errors the API turns into answers.
var (
	// ErrForbidden is returned when the role may not change the channel (R5.9).
	ErrForbidden = errors.New("your role does not allow this")
	// ErrNotFound is returned for an unknown channel, or another user's personal channel.
	ErrNotFound = errors.New("no such notification channel")
	// ErrTooSoon is returned when a test is asked for again within TestMinGap (R5.31).
	ErrTooSoon = errors.New("a test of this channel was sent a moment ago")
)

// InvalidError lists what is wrong with a request, one sentence each.
type InvalidError struct{ Problems []string }

func (e *InvalidError) Error() string { return strings.Join(e.Problems, " ") }

// NeedsConfirmation is returned by Delete without a yes (UX-62). It names the
// channel and the events it carries.
type NeedsConfirmation struct {
	Name   string
	Events []string
}

func (e *NeedsConfirmation) Error() string {
	return "removing the channel needs a yes: " + e.Name
}

// Caller says who asks. Admin is true for an admin or the owner.
type Caller struct {
	Login string
	Admin bool
	Actor audit.Actor
}

// SMTP is the mail server of an email channel. The password is not here.
type SMTP struct {
	Host        string
	Port        int
	Username    string
	From        string
	HasPassword bool
}

// Channel is one channel as the API shows it. No secret is in it.
type Channel struct {
	ID          string
	Kind        string
	Name        string
	Scope       string
	Enabled     bool
	Events      []string
	MinSeverity string
	Address     string
	URL         string
	SecretSet   bool
	SMTP        *SMTP
	LastTestAt  *time.Time
	LastTestOK  *bool
}

// SMTPInput is the mail server of a new email channel.
type SMTPInput struct {
	Host     string
	Port     int
	Username string
	From     string
	Password string
}

// Input is a new channel.
type Input struct {
	Kind        string
	Name        string
	Enabled     *bool
	Events      []string
	MinSeverity string
	Address     string
	URL         string
	Secret      string
	SMTP        *SMTPInput
}

// SMTPPatch changes the mail server. A nil field keeps its value; a Password
// that points to an empty string removes it.
type SMTPPatch struct {
	Host     *string
	Port     *int
	Username *string
	From     *string
	Password *string
}

// Patch changes a channel. A nil field keeps its value. A Secret that points
// to an empty string removes the signing secret.
type Patch struct {
	Name        *string
	Enabled     *bool
	Events      *[]string
	MinSeverity *string
	Address     *string
	URL         *string
	Secret      *string
	SMTP        *SMTPPatch
}

// TestResult is what one test send did (R5.30).
type TestResult struct {
	OK      bool
	Message string
	SentAt  time.Time
}

// Config makes a Service.
type Config struct {
	DB      *store.Store
	Secrets secrets.Store
	Logger  *slog.Logger
	// Now is the clock; nil uses time.Now.
	Now func() time.Time
	// Thresholds returns the pool thresholds (R5.8); nil means the defaults.
	Thresholds func(ctx context.Context) Thresholds
	// ConsoleURL is the console's address, for the link in a notice; empty gives a path only.
	ConsoleURL string

	// The two fields below exist for tests only. AllowLoopback lets a channel
	// point at 127.0.0.1 (normally refused, see guard.go); Trust adds the
	// certificates a test server presents.
	AllowLoopback bool
	Trust         *x509.CertPool
}

// Service keeps the channels and sends tests.
type Service struct {
	cfg   Config
	audit *audit.Log

	mu        sync.Mutex
	lastTest  map[string]time.Time // channel id + user
	lastMaint time.Time

	engMu   sync.Mutex // one state change at a time
	sources []Source
}

// New returns the service. DB and Secrets are required.
func New(cfg Config) *Service {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Service{cfg: cfg, audit: audit.New(cfg.DB), lastTest: map[string]time.Time{}}
}

// signingKey and passwordKey name the two secrets of a channel in the OS
// secret store. A key is not itself secret.
func signingKey(id string) secrets.Key  { return secrets.Key("crsp/notify/" + id + "/signing") }
func passwordKey(id string) secrets.Key { return secrets.Key("crsp/notify/" + id + "/smtp-auth") }

func newID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return "nch_" + hex.EncodeToString(b[:])
}

func toChannel(c store.NotificationChannel) Channel {
	out := Channel{
		ID: c.ID, Kind: c.Kind, Name: c.Name, Scope: c.Scope, Enabled: c.Enabled, Events: c.Events,
		MinSeverity: c.MinSeverity, Address: c.Address, URL: c.URL, SecretSet: c.HasSigning && c.Kind == KindWebhook,
		LastTestAt: c.LastTestAt, LastTestOK: c.LastTestOK,
	}
	if c.Kind == KindEmail {
		out.SMTP = &SMTP{Host: c.SMTPHost, Port: c.SMTPPort, Username: c.SMTPUsername, From: c.SMTPFrom, HasPassword: c.SMTPHasPassword}
	}
	return out
}

// canSee says whether the caller may read the channel. A viewer sees only their
// own personal channels: a webhook URL can carry a token of its own.
func canSee(c store.NotificationChannel, who Caller) bool {
	if c.Scope == ScopePool {
		return who.Admin
	}
	return c.OwnerLogin == who.Login
}

// List returns the channels the caller may see: their own personal channels,
// and for an admin or the owner the pool's webhooks too.
func (s *Service) List(ctx context.Context, who Caller) ([]Channel, error) {
	rows, err := s.cfg.DB.ListNotificationChannels(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Channel, 0, len(rows))
	for _, r := range rows {
		if canSee(r, who) {
			out = append(out, toChannel(r))
		}
	}
	return out, nil
}

// load returns a channel the caller may change: another user's personal
// channel does not exist for them, and a webhook needs an admin (R5.9).
func (s *Service) load(ctx context.Context, who Caller, id string) (store.NotificationChannel, error) {
	c, err := s.cfg.DB.GetNotificationChannel(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return c, ErrNotFound
	}
	if err != nil {
		return c, err
	}
	if c.Scope == ScopePool && !who.Admin {
		return c, ErrForbidden
	}
	if c.Scope == ScopePersonal && c.OwnerLogin != who.Login {
		return c, ErrNotFound
	}
	return c, nil
}

// Create adds a channel (R5.7 to R5.9, R5.19).
func (s *Service) Create(ctx context.Context, who Caller, in Input) (Channel, error) {
	if in.Kind == KindWebhook && !who.Admin {
		return Channel{}, ErrForbidden
	}
	c := store.NotificationChannel{
		ID: newID(), Kind: in.Kind, Enabled: true, MinSeverity: DefaultSeverity,
	}
	var p problems
	switch in.Kind {
	case KindEmail, KindDesktop:
		c.Scope, c.OwnerLogin = ScopePersonal, who.Login
	case KindWebhook:
		c.Scope = ScopePool
	default:
		p.add("The kind must be email, webhook or desktop.")
		return Channel{}, p.err()
	}
	c.Name = checkName(&p, in.Name)
	if in.Enabled != nil {
		c.Enabled = *in.Enabled
	}
	c.Events = checkEvents(&p, in.Events)
	if in.MinSeverity != "" {
		c.MinSeverity = checkSeverity(&p, in.MinSeverity)
	}
	var signing, password string
	switch in.Kind {
	case KindEmail:
		c.Address = checkAddress(&p, "address", in.Address)
		if in.URL != "" || in.Secret != "" {
			p.add("An email channel has no URL or signing secret.")
		}
		if in.SMTP == nil {
			p.add("An email channel needs a mail server: host, port and sender.")
		} else {
			c.SMTPHost = checkHost(&p, in.SMTP.Host, s.cfg.AllowLoopback)
			c.SMTPPort = checkPort(&p, in.SMTP.Port)
			c.SMTPUsername = strings.TrimSpace(in.SMTP.Username)
			c.SMTPFrom = checkAddress(&p, "sender", in.SMTP.From)
			password = in.SMTP.Password
			if password != "" && c.SMTPUsername == "" {
				p.add("A password needs a user name.")
			}
			c.SMTPHasPassword = password != ""
		}
	case KindWebhook:
		c.URL = checkWebhookURL(&p, in.URL, s.cfg.AllowLoopback)
		if in.Address != "" || in.SMTP != nil {
			p.add("A webhook has no email address or mail server.")
		}
		signing = in.Secret
		if len(signing) < MinSecretLen {
			p.add(fmt.Sprintf("A webhook needs a signing secret of at least %d characters.", MinSecretLen))
		}
		c.HasSigning = signing != ""
	case KindDesktop:
		if in.Address != "" || in.URL != "" || in.Secret != "" || in.SMTP != nil {
			p.add("A desktop channel has no address, URL, secret or mail server.")
		}
	}
	if err := p.err(); err != nil {
		return Channel{}, err
	}

	if err := s.putSecret(ctx, signingKey(c.ID), signing); err != nil {
		return Channel{}, err
	}
	if err := s.putSecret(ctx, passwordKey(c.ID), password); err != nil {
		s.dropSecrets(ctx, c.ID)
		return Channel{}, err
	}
	err := s.cfg.DB.WithTx(ctx, func(tx *store.Tx) error {
		if err := tx.InsertNotificationChannel(c); err != nil {
			return err
		}
		return audit.RecordTx(tx, who.Actor, "notification.channel.create", audit.Target{Type: "notification_channel", ID: c.ID},
			s.auditDetails(c, "created channel "+c.Name))
	})
	if err != nil {
		s.dropSecrets(ctx, c.ID)
		return Channel{}, err
	}
	return toChannel(c), nil
}

// Update changes a channel; the kind never changes (R5.7 to R5.9).
func (s *Service) Update(ctx context.Context, who Caller, id string, pt Patch) (Channel, error) {
	c, err := s.load(ctx, who, id)
	if err != nil {
		return Channel{}, err
	}
	var p problems
	var fields []string
	if pt.Name != nil {
		c.Name = checkName(&p, *pt.Name)
		fields = append(fields, "name")
	}
	if pt.Enabled != nil {
		c.Enabled = *pt.Enabled
		fields = append(fields, "enabled")
	}
	if pt.Events != nil {
		c.Events = checkEvents(&p, *pt.Events)
		fields = append(fields, "events")
	}
	if pt.MinSeverity != nil {
		c.MinSeverity = checkSeverity(&p, *pt.MinSeverity)
		fields = append(fields, "min_severity")
	}
	if pt.Address != nil {
		if c.Kind != KindEmail {
			p.add("Only an email channel has an address.")
		} else {
			c.Address = checkAddress(&p, "address", *pt.Address)
			fields = append(fields, "address")
		}
	}
	if pt.URL != nil {
		if c.Kind != KindWebhook {
			p.add("Only a webhook has a URL.")
		} else {
			c.URL = checkWebhookURL(&p, *pt.URL, s.cfg.AllowLoopback)
			fields = append(fields, "url")
		}
	}
	signing, setSigning := "", false
	if pt.Secret != nil {
		switch {
		case c.Kind != KindWebhook:
			p.add("Only a webhook has a signing secret.")
		case *pt.Secret != "" && len(*pt.Secret) < MinSecretLen:
			p.add(fmt.Sprintf("A webhook needs a signing secret of at least %d characters.", MinSecretLen))
		default:
			signing, setSigning = *pt.Secret, true
			c.HasSigning = signing != ""
			fields = append(fields, "signing")
		}
	}
	password, setPassword := "", false
	if pt.SMTP != nil {
		if c.Kind != KindEmail {
			p.add("Only an email channel has a mail server.")
		} else {
			sp := pt.SMTP
			if sp.Host != nil {
				c.SMTPHost = checkHost(&p, *sp.Host, s.cfg.AllowLoopback)
				fields = append(fields, "smtp.host")
			}
			if sp.Port != nil {
				c.SMTPPort = checkPort(&p, *sp.Port)
				fields = append(fields, "smtp.port")
			}
			if sp.Username != nil {
				c.SMTPUsername = strings.TrimSpace(*sp.Username)
				fields = append(fields, "smtp.username")
			}
			if sp.From != nil {
				c.SMTPFrom = checkAddress(&p, "sender", *sp.From)
				fields = append(fields, "smtp.from")
			}
			if sp.Password != nil {
				password, setPassword = *sp.Password, true
				c.SMTPHasPassword = password != ""
				fields = append(fields, "smtp.password")
			}
			if c.SMTPHasPassword && c.SMTPUsername == "" {
				p.add("A password needs a user name.")
			}
		}
	}
	if err := p.err(); err != nil {
		return Channel{}, err
	}

	if setSigning {
		if err := s.putSecret(ctx, signingKey(c.ID), signing); err != nil {
			return Channel{}, err
		}
	}
	if setPassword {
		if err := s.putSecret(ctx, passwordKey(c.ID), password); err != nil {
			return Channel{}, err
		}
	}
	details := s.auditDetails(c, "changed channel "+c.Name)
	details["fields"] = fields
	err = s.cfg.DB.WithTx(ctx, func(tx *store.Tx) error {
		if err := tx.UpdateNotificationChannel(c); err != nil {
			return err
		}
		return audit.RecordTx(tx, who.Actor, "notification.channel.update", audit.Target{Type: "notification_channel", ID: c.ID}, details)
	})
	if errors.Is(err, store.ErrNotFound) {
		return Channel{}, ErrNotFound
	}
	if err != nil {
		return Channel{}, err
	}
	return s.reload(ctx, c.ID)
}

func (s *Service) reload(ctx context.Context, id string) (Channel, error) {
	c, err := s.cfg.DB.GetNotificationChannel(ctx, id)
	if err != nil {
		return Channel{}, err
	}
	return toChannel(c), nil
}

// Delete removes a channel and its secrets. Without confirm it answers
// *NeedsConfirmation naming the channel and its events (UX-62).
func (s *Service) Delete(ctx context.Context, who Caller, id string, confirm bool) error {
	c, err := s.load(ctx, who, id)
	if err != nil {
		return err
	}
	if !confirm {
		return &NeedsConfirmation{Name: c.Name, Events: c.Events}
	}
	err = s.cfg.DB.WithTx(ctx, func(tx *store.Tx) error {
		if err := tx.DeleteNotificationChannel(c.ID); err != nil {
			return err
		}
		return audit.RecordTx(tx, who.Actor, "notification.channel.delete", audit.Target{Type: "notification_channel", ID: c.ID},
			s.auditDetails(c, "removed channel "+c.Name))
	})
	if errors.Is(err, store.ErrNotFound) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	s.dropSecrets(ctx, c.ID)
	return nil
}

// Test sends one clearly marked test notice through the channel's real path
// and says what happened (R5.29, R5.30). A failed delivery is a result, not an
// error. It is not retried, and it is limited to one per channel per user per
// 10 s (R5.31).
func (s *Service) Test(ctx context.Context, who Caller, id string) (TestResult, error) {
	c, err := s.load(ctx, who, id)
	if err != nil {
		return TestResult{}, err
	}
	now := s.cfg.Now()
	gate := c.ID + "\x00" + who.Login
	s.mu.Lock()
	if last, ok := s.lastTest[gate]; ok && now.Sub(last) < TestMinGap {
		s.mu.Unlock()
		return TestResult{}, ErrTooSoon
	}
	s.lastTest[gate] = now
	s.mu.Unlock()

	res := TestResult{SentAt: now.UTC()}
	switch c.Kind {
	case KindEmail:
		res.OK, res.Message = s.testEmail(ctx, c)
	case KindWebhook:
		res.OK, res.Message = s.testWebhook(ctx, c)
	default:
		res.Message = desktopMissing
	}
	if err := s.cfg.DB.SetNotificationChannelTest(ctx, c.ID, res.SentAt, res.OK); err != nil {
		s.cfg.Logger.Error("notify: record test result", "channel", c.ID, "err", err)
	}
	details := map[string]any{"summary": "sent a test through channel " + c.Name, "kind": c.Kind, "ok": res.OK}
	if err := s.audit.Record(ctx, who.Actor, "notification.channel.test", audit.Target{Type: "notification_channel", ID: c.ID}, details); err != nil {
		s.cfg.Logger.Error("notify: audit test", "channel", c.ID, "err", err)
	}
	return res, nil
}

// desktopMissing says exactly what a desktop test lacks (R5.15, R5.29).
const desktopMissing = "No desktop notice was shown. The controller has no way to send a notice to a tray yet: " +
	"the tray only asks the controller for its status, and nothing lets the controller push a notice to it (notifications R5.15)."

func (s *Service) auditDetails(c store.NotificationChannel, summary string) map[string]any {
	return map[string]any{
		"summary": summary, "kind": c.Kind, "name": c.Name, "scope": c.Scope,
		"enabled": c.Enabled, "events": c.Events, "min_severity": c.MinSeverity,
	}
}

// putSecret stores value under key, or removes the secret when value is empty.
// A secret store that cannot be reached fails the request: a channel never
// keeps a secret anywhere else.
func (s *Service) putSecret(ctx context.Context, key secrets.Key, value string) error {
	var err error
	if value == "" {
		err = s.cfg.Secrets.Delete(ctx, key)
	} else {
		err = s.cfg.Secrets.Put(ctx, key, []byte(value))
	}
	if err != nil {
		s.cfg.Logger.Error("notify: secret store", "key", string(key), "err", err)
		return fmt.Errorf("the secret store could not save the secret: %w", secrets.ErrUnavailable)
	}
	return nil
}

func (s *Service) dropSecrets(ctx context.Context, id string) {
	for _, k := range []secrets.Key{signingKey(id), passwordKey(id)} {
		if err := s.cfg.Secrets.Delete(ctx, k); err != nil {
			s.cfg.Logger.Error("notify: remove secret", "key", string(k), "err", err)
		}
	}
}

// ---------------------------------------------------------------- checks

type problems struct{ list []string }

func (p *problems) add(s string) { p.list = append(p.list, s) }
func (p *problems) err() error {
	if len(p.list) == 0 {
		return nil
	}
	return &InvalidError{Problems: p.list}
}

func checkName(p *problems, name string) string {
	name = strings.TrimSpace(name)
	switch {
	case name == "":
		p.add("Write a name for the channel.")
	case utf8.RuneCountInString(name) > maxName:
		p.add(fmt.Sprintf("The name is longer than %d characters.", maxName))
	case strings.ContainsAny(name, "\r\n\x00"):
		p.add("The name must be one line.")
	}
	return name
}

func checkEvents(p *problems, events []string) []string {
	known := map[string]bool{}
	for _, e := range Events {
		known[e] = true
	}
	seen := map[string]bool{}
	for _, e := range events {
		if !known[e] {
			p.add("Unknown event " + quote(e) + ".")
			continue
		}
		seen[e] = true
	}
	out := []string{}
	for _, e := range Events {
		if seen[e] {
			out = append(out, e)
		}
	}
	if len(events) == 0 {
		p.add("Pick at least one event.")
	}
	return out
}

func checkSeverity(p *problems, s string) string {
	for _, v := range Severities {
		if s == v {
			return s
		}
	}
	p.add("The lowest severity must be info, warning or error.")
	return DefaultSeverity
}

// checkAddress accepts one plain address, "name@host". A display name, a list
// or a control character is refused, so the value is safe in a mail header.
func checkAddress(p *problems, what, v string) string {
	v = strings.TrimSpace(v)
	a, err := mail.ParseAddress(v)
	if v == "" || len(v) > maxAddr || err != nil || a.Address != v || strings.ContainsAny(v, "\r\n<>,;\" ") ||
		!strings.Contains(a.Address[strings.LastIndex(a.Address, "@")+1:], ".") {
		p.add("Write the " + what + " as an email address, for example name@example.com.")
	}
	return v
}

func checkPort(p *problems, port int) int {
	if port < 1 || port > 65535 {
		p.add("The mail server port must be between 1 and 65535.")
	}
	return port
}

// checkHost accepts a host name or an IP literal, never a URL or a path.
func checkHost(p *problems, host string, allowLoopback bool) string {
	host = strings.TrimSpace(host)
	if host == "" || len(host) > 253 || strings.ContainsAny(host, " /\\@?#:[]\r\n\t") && net.ParseIP(strings.Trim(host, "[]")) == nil {
		p.add("Write the mail server as a host name, for example smtp.example.com.")
		return host
	}
	if why := hostRefused(host, allowLoopback); why != "" {
		p.add("The mail server " + why)
	}
	return host
}

func checkWebhookURL(p *problems, raw string, allowLoopback bool) string {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	switch {
	case raw == "" || len(raw) > maxURL || err != nil || u.Host == "":
		p.add("Write the webhook address as an https URL, for example https://hooks.example.com/crsp.")
	case !strings.EqualFold(u.Scheme, "https"):
		p.add("A webhook must use https (notifications R5.19).")
	case u.User != nil:
		p.add("The webhook address must not carry a user name or password.")
	case u.Fragment != "" || strings.ContainsAny(raw, "\r\n\t "):
		p.add("The webhook address must be one plain URL.")
	default:
		if why := hostRefused(u.Hostname(), allowLoopback); why != "" {
			p.add("The webhook address " + why)
		}
	}
	return raw
}

func quote(s string) string {
	if len(s) > 40 {
		s = s[:40] + "..."
	}
	return "\"" + strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return '?'
		}
		return r
	}, s) + "\""
}
