package notify

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/smtp"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/secrets"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/store"
)

// Webhook headers (notifications R5.21, R5.29). A receiver rejects a timestamp
// older than 5 minutes and compares X-CRP-Signature in constant time with
// "sha256=" + hex(HMAC-SHA256(secret, timestamp + "." + body)).
const (
	HeaderTimestamp = "X-CRP-Timestamp"
	HeaderSignature = "X-CRP-Signature"
	HeaderDelivery  = "X-CRP-Delivery"
	HeaderTest      = "X-CRP-Test"
)

const (
	webhookTimeout = 10 * time.Second
	smtpTimeout    = 15 * time.Second
	// schemaVersion is the version of the webhook body (R5.20).
	schemaVersion = 1
)

// Sign returns the value of the X-CRP-Signature header.
func Sign(secret []byte, timestamp string, body []byte) string {
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(timestamp))
	m.Write([]byte("."))
	m.Write(body)
	return "sha256=" + hex.EncodeToString(m.Sum(nil))
}

func randomID(prefix string) string {
	var b [10]byte
	_, _ = rand.Read(b[:])
	return prefix + hex.EncodeToString(b[:])
}

// ---------------------------------------------------------------- webhook

// testBody is the JSON of a test delivery: the fields of R5.20, marked as a test (R5.29).
type testBody struct {
	SchemaVersion int    `json:"schema_version"`
	EventID       string `json:"event_id"`
	DeliveryID    string `json:"delivery_id"`
	Type          string `json:"type"`
	Test          bool   `json:"test"`
	Subject       string `json:"subject"`
	State         string `json:"state"`
	Tone          string `json:"tone"`
	Time          string `json:"time"`
	Summary       string `json:"summary"`
}

func (s *Service) httpClient() *http.Client {
	tr := &http.Transport{
		// No proxy: the guard sees the address that is really dialled.
		Proxy:               nil,
		DialContext:         s.dialContext(webhookTimeout),
		TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: s.cfg.Trust},
		TLSHandshakeTimeout: webhookTimeout,
		DisableKeepAlives:   true,
	}
	return &http.Client{
		Transport: tr,
		Timeout:   webhookTimeout,
		// R5.19: never follow a redirect.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func (s *Service) testWebhook(ctx context.Context, c store.NotificationChannel) (bool, string) {
	host := hostOf(c.URL)
	secret, err := s.cfg.Secrets.Get(ctx, signingKey(c.ID))
	if err != nil {
		// R6.3: never send unsigned.
		if errors.Is(err, secrets.ErrNotFound) {
			return false, "Nothing was sent. This webhook has no signing secret, and the controller never sends an unsigned notice. Set a secret and try again."
		}
		return false, "Nothing was sent. The secret store did not give the signing secret, and the controller never sends an unsigned notice."
	}
	now := s.cfg.Now().UTC()
	body, _ := json.Marshal(testBody{
		SchemaVersion: schemaVersion, EventID: randomID("evt_"), DeliveryID: randomID("dlv_"),
		Type: "test", Test: true, Subject: "crsp", State: "firing", Tone: "note",
		Time:    now.Format(time.RFC3339),
		Summary: "This is a test notice from crsp. Nothing is wrong.",
	})
	var sent testBody
	_ = json.Unmarshal(body, &sent)
	ts := strconv.FormatInt(now.Unix(), 10)

	rctx, cancel := context.WithTimeout(ctx, webhookTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(rctx, http.MethodPost, c.URL, bytes.NewReader(body))
	if err != nil {
		return false, "Nothing was sent. The webhook address is not valid."
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "crsp-notify/1")
	req.Header.Set(HeaderTimestamp, ts)
	req.Header.Set(HeaderSignature, Sign(secret, ts, body))
	req.Header.Set(HeaderDelivery, sent.DeliveryID)
	req.Header.Set(HeaderTest, "true")

	res, err := s.httpClient().Do(req)
	if err != nil {
		return false, "Could not reach " + host + ". " + describe(err, host, webhookTimeout)
	}
	defer func() { _ = res.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 4096))
	switch {
	case res.StatusCode >= 200 && res.StatusCode < 300:
		return true, fmt.Sprintf("Delivered. %s answered HTTP %d.", host, res.StatusCode)
	case res.StatusCode >= 300 && res.StatusCode < 400:
		return false, fmt.Sprintf("%s answered with a redirect (HTTP %d). The controller does not follow redirects; use the final address.", host, res.StatusCode)
	default:
		return false, fmt.Sprintf("%s refused the notice with HTTP %d.", host, res.StatusCode)
	}
}

// hostOf returns the host of a URL for messages. A webhook URL can carry a
// token in its path or query, so the rest is never shown.
func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "the webhook"
	}
	return u.Host
}

// describe turns a network error into one plain sentence without the URL.
func describe(err error, host string, limit time.Duration) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	var ne net.Error
	var ge *guardError
	switch {
	case errors.As(err, &ge):
		return "The address " + ge.why
	case errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout()):
		return fmt.Sprintf("The connection timed out after %d s.", int(limit.Seconds()))
	}
	var dns *net.DNSError
	if errors.As(err, &dns) {
		return "The name " + dns.Name + " was not found."
	}
	if strings.Contains(err.Error(), "certificate") || strings.Contains(err.Error(), "x509") {
		return "The certificate was not accepted: " + oneLine(err.Error()) + "."
	}
	return oneLine(err.Error()) + "."
}

func oneLine(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if len(s) > 300 {
		s = s[:300] + "..."
	}
	return strings.TrimRight(s, ".")
}

// ---------------------------------------------------------------- email

func (s *Service) testEmail(ctx context.Context, c store.NotificationChannel) (bool, string) {
	var password string
	if c.SMTPHasPassword {
		b, err := s.cfg.Secrets.Get(ctx, passwordKey(c.ID))
		if err != nil {
			return false, "Nothing was sent. The secret store did not give the mail server password."
		}
		password = string(b)
	}
	now := s.cfg.Now().UTC()
	msg := buildTestMail(c.SMTPFrom, c.Address, now)
	where := c.SMTPHost + ":" + strconv.Itoa(c.SMTPPort)

	if err := s.sendMail(ctx, c, password, msg); err != nil {
		var se *smtpStepError
		if errors.As(err, &se) {
			return false, se.text(password)
		}
		return false, "Could not reach " + where + ". " + scrub(describe(err, c.SMTPHost, smtpTimeout), password)
	}
	return true, "Sent a test email to " + c.Address + " through " + where + "."
}

// smtpStepError says which step of the dialogue the server refused.
type smtpStepError struct {
	step string
	err  error
}

func (e *smtpStepError) Error() string { return e.step + ": " + e.err.Error() }
func (e *smtpStepError) Unwrap() error { return e.err }

func (e *smtpStepError) text(password string) string {
	return "The mail server did not accept the test. It refused " + e.step + ": " + scrub(oneLine(e.err.Error()), password) + "."
}

// scrub removes the password from text that came from a server or an error.
func scrub(text, password string) string {
	if len(password) >= 4 {
		text = strings.ReplaceAll(text, password, "[hidden]")
	}
	return text
}

func buildTestMail(from, to string, now time.Time) []byte {
	var b bytes.Buffer
	h := func(k, v string) { b.WriteString(k + ": " + v + "\r\n") }
	h("From", from)
	h("To", to)
	h("Subject", "[crsp test] Test notice")
	h("Date", now.Format(time.RFC1123Z))
	h("Message-ID", "<"+randomID("")+"@crsp>")
	h("MIME-Version", "1.0")
	h("Content-Type", "text/plain; charset=utf-8")
	h("X-CRP-Test", "true")
	b.WriteString("\r\n")
	b.WriteString("This is a test notice from crsp.\r\nNothing is wrong. The channel works.\r\n")
	return b.Bytes()
}

// sendMail sends one message: implicit TLS on port 465, otherwise STARTTLS
// where the server offers it. A password is sent only over TLS (net/smtp
// refuses a plain connection except to localhost).
func (s *Service) sendMail(ctx context.Context, c store.NotificationChannel, password string, msg []byte) error {
	ctx, cancel := context.WithTimeout(ctx, smtpTimeout)
	defer cancel()
	addr := net.JoinHostPort(c.SMTPHost, strconv.Itoa(c.SMTPPort))
	tlsCfg := &tls.Config{ServerName: c.SMTPHost, MinVersion: tls.VersionTLS12, RootCAs: s.cfg.Trust}

	conn, err := s.dialContext(smtpTimeout)(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(smtpTimeout))
	if c.SMTPPort == 465 {
		tc := tls.Client(conn, tlsCfg)
		if err := tc.HandshakeContext(ctx); err != nil {
			return err
		}
		conn = tc
	}
	cl, err := smtp.NewClient(conn, c.SMTPHost)
	if err != nil {
		return &smtpStepError{"the greeting", err}
	}
	defer func() { _ = cl.Close() }()
	if name, err := os.Hostname(); err == nil && name != "" && !strings.ContainsAny(name, " \r\n") {
		_ = cl.Hello(name)
	}
	if c.SMTPPort != 465 {
		if ok, _ := cl.Extension("STARTTLS"); ok {
			if err := cl.StartTLS(tlsCfg); err != nil {
				return err
			}
		}
	}
	if password != "" {
		_, tlsOn := cl.TLSConnectionState()
		if !tlsOn && !isLoopbackHost(c.SMTPHost) {
			return &smtpStepError{"the sign-in", errors.New("the server does not offer STARTTLS, so the password was not sent")}
		}
		if err := cl.Auth(pickAuth(cl, c.SMTPUsername, password, c.SMTPHost)); err != nil {
			return &smtpStepError{"the sign-in", err}
		}
	}
	if err := cl.Mail(c.SMTPFrom); err != nil {
		return &smtpStepError{"the sender " + c.SMTPFrom, err}
	}
	if err := cl.Rcpt(c.Address); err != nil {
		return &smtpStepError{"the address " + c.Address, err}
	}
	w, err := cl.Data()
	if err != nil {
		return &smtpStepError{"the message", err}
	}
	if _, err := w.Write(msg); err != nil {
		return &smtpStepError{"the message", err}
	}
	if err := w.Close(); err != nil {
		return &smtpStepError{"the message", err}
	}
	_ = cl.Quit()
	return nil
}

func isLoopbackHost(h string) bool {
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(strings.Trim(h, "[]"))
	return ip != nil && ip.IsLoopback()
}

// pickAuth uses PLAIN, or LOGIN when the server offers only that.
func pickAuth(cl *smtp.Client, user, password, host string) smtp.Auth {
	if ok, params := cl.Extension("AUTH"); ok {
		mechs := " " + strings.ToUpper(params) + " "
		if !strings.Contains(mechs, " PLAIN ") && strings.Contains(mechs, " LOGIN ") {
			return loginAuth{user: user, password: password}
		}
	}
	return smtp.PlainAuth("", user, password, host)
}

// loginAuth is the LOGIN mechanism, which some providers offer instead of PLAIN.
type loginAuth struct{ user, password string }

func (a loginAuth) Start(*smtp.ServerInfo) (string, []byte, error) { return "LOGIN", nil, nil }

func (a loginAuth) Next(from []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	switch strings.ToLower(strings.TrimSpace(string(from))) {
	case "username:":
		return []byte(a.user), nil
	case "password:":
		return []byte(a.password), nil
	}
	return nil, errors.New("unexpected prompt from the server")
}
