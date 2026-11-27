package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/textproto"
	"strconv"
	"strings"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/store"
)

// Message is one notice ready to send through any channel (R5.20, R6.7, R6.8).
type Message struct {
	EventID    string
	DeliveryID string
	Type       string
	Subject    string
	State      string
	Tone       string
	Time       time.Time
	Summary    string
	Link       string
	Count      int // more than 0 for a grouped notice
}

// compose builds the message of a delivery. A grouped delivery says how many notices it stands for (R5.13).
// The tone and the subject are in the words, never in colour alone (R6.8).
func (s *Service) compose(d store.NoticeDelivery, n store.Notice) Message {
	m := Message{
		EventID: n.ID, DeliveryID: d.ID, Type: n.Type, Subject: nameOr(n.SubjectName, n.SubjectID), State: n.State, Tone: n.Tone,
		Time: store.ParseTime(n.CreatedAt), Summary: n.Summary, Link: s.link(n.Link),
	}
	if d.GroupCount > 0 && d.BurstGroup != "" {
		m.Count = int(d.GroupCount)
		m.Summary = groupedSummary(n.Type, n.State, m.Count)
		m.Subject = fmt.Sprint(m.Count, " notices")
		m.Link = s.link("/notices")
	}
	return m
}

// link makes a console link. The base is the console address when the controller knows it; a path alone otherwise.
func (s *Service) link(path string) string {
	if path == "" {
		path = "/notices"
	}
	if s.cfg.ConsoleURL == "" {
		return path
	}
	return strings.TrimRight(s.cfg.ConsoleURL, "/") + path
}

// Title is the first line: the event in plain words and the tone as a word.
func (m Message) Title() string {
	return Title(m.Type, m.State) + " (" + ToneWords[m.Tone] + ")"
}

type webhookBody struct {
	SchemaVersion int    `json:"schema_version"`
	EventID       string `json:"event_id"`
	DeliveryID    string `json:"delivery_id"`
	Type          string `json:"type"`
	Subject       string `json:"subject"`
	State         string `json:"state"`
	Tone          string `json:"tone"`
	Time          string `json:"time"`
	Summary       string `json:"summary"`
	Link          string `json:"link"`
	Count         int    `json:"count,omitempty"`
}

// sendWebhook makes one signed POST (R5.19 to R5.21). The delivery id is the same on every retry. It never sends
// unsigned (R6.3). The failure text names the host only, because the path and query can hold a token.
func (s *Service) sendWebhook(ctx context.Context, c store.NotificationChannel, d store.NoticeDelivery, m Message) outcome {
	host := hostOf(c.URL)
	secret, why, err := s.readSecret(ctx, signingKey(c.ID))
	if err != nil {
		return outcome{secretGone: true, msg: why}
	}
	now := s.cfg.Now().UTC()
	body, _ := json.Marshal(webhookBody{
		SchemaVersion: schemaVersion, EventID: m.EventID, DeliveryID: m.DeliveryID, Type: m.Type, Subject: m.Subject,
		State: m.State, Tone: m.Tone, Time: m.Time.UTC().Format(time.RFC3339), Summary: m.Summary, Link: m.Link, Count: m.Count,
	})
	ts := strconv.FormatInt(now.Unix(), 10)
	rctx, cancel := context.WithTimeout(ctx, webhookTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(rctx, http.MethodPost, c.URL, bytes.NewReader(body))
	if err != nil {
		return outcome{permanent: true, msg: "The webhook address is not valid."}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "crsp-notify/1")
	req.Header.Set(HeaderTimestamp, ts)
	req.Header.Set(HeaderSignature, Sign(secret, ts, body))
	req.Header.Set(HeaderDelivery, m.DeliveryID)
	res, err := s.httpClient().Do(req)
	if err != nil {
		var ge *guardError
		if errors.As(err, &ge) {
			return outcome{permanent: true, msg: "Could not reach " + host + ". " + describe(err, host, webhookTimeout)}
		}
		return outcome{msg: "Could not reach " + host + ". " + describe(err, host, webhookTimeout)}
	}
	defer func() { _ = res.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 4096))
	return webhookOutcome(host, res.StatusCode, res.Header.Get("Retry-After"), now)
}

// webhookOutcome applies R5.23: 2xx delivered; 408, 429 and 5xx are tried again (a Retry-After is honoured up to
// 1 h); a redirect and any other 4xx are not.
func webhookOutcome(host string, code int, retryAfter string, now time.Time) outcome {
	switch {
	case code >= 200 && code < 300:
		return outcome{ok: true}
	case code == http.StatusRequestTimeout || code == http.StatusTooManyRequests || code >= 500:
		return outcome{msg: fmt.Sprintf("%s answered HTTP %d.", host, code), retryAfter: parseRetryAfter(retryAfter, now)}
	case code >= 300 && code < 400:
		return outcome{permanent: true, msg: fmt.Sprintf("%s answered with a redirect (HTTP %d). The controller does not follow redirects.", host, code)}
	}
	return outcome{permanent: true, msg: fmt.Sprintf("%s refused the notice with HTTP %d.", host, code)}
}

// parseRetryAfter reads seconds or an HTTP date; anything else is no hint.
func parseRetryAfter(v string, now time.Time) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil && t.After(now) {
		return t.Sub(now)
	}
	return 0
}

// sendEmail sends one message through the channel's mail server (R5.28). A 5xx answer of the server is final; a
// network error or a 4xx answer is tried again.
func (s *Service) sendEmail(ctx context.Context, c store.NotificationChannel, d store.NoticeDelivery, m Message) outcome {
	var password string
	if c.SMTPHasPassword {
		b, _, err := s.readSecret(ctx, passwordKey(c.ID))
		if err != nil {
			return outcome{secretGone: true, msg: "The secret store did not give the mail server password."}
		}
		password = string(b)
	}
	msg := buildMail(c.SMTPFrom, c.Address, m, s.cfg.Now().UTC())
	err := s.sendMail(ctx, c, password, msg)
	if err == nil {
		return outcome{ok: true}
	}
	where := c.SMTPHost + ":" + strconv.Itoa(c.SMTPPort)
	var se *smtpStepError
	if errors.As(err, &se) {
		var te *textproto.Error
		permanent := errors.As(se.err, &te) && te.Code >= 500
		return outcome{permanent: permanent, msg: scrub(se.text(password), password)}
	}
	var ge *guardError
	return outcome{permanent: errors.As(err, &ge), msg: "Could not reach " + where + ". " + scrub(describe(err, c.SMTPHost, smtpTimeout), password)}
}

func buildMail(from, to string, m Message, now time.Time) []byte {
	var b bytes.Buffer
	h := func(k, v string) { b.WriteString(k + ": " + oneLine(v) + "\r\n") }
	h("From", from)
	h("To", to)
	h("Subject", "[crsp] "+m.Title())
	h("Date", now.Format(time.RFC1123Z))
	h("Message-ID", "<"+randomID("")+"@crsp>")
	h("MIME-Version", "1.0")
	h("Content-Type", "text/plain; charset=utf-8")
	h("X-CRP-Delivery", m.DeliveryID)
	b.WriteString("\r\n")
	b.WriteString(m.Title() + "\r\n\r\n")
	b.WriteString(m.Summary + "\r\n")
	b.WriteString("Time: " + m.Time.UTC().Format("2006-01-02 15:04 UTC") + "\r\n")
	b.WriteString("Open the console: " + m.Link + "\r\n")
	return b.Bytes()
}

// DesktopNotice is one notice a tray shows (R5.15, section 4 "Desktop notice").
type DesktopNotice struct {
	DeliveryID string
	Title      string
	Body       string
	Link       string
	At         time.Time
}

// PendingDesktop returns the desktop notices of one user that no tray has taken yet, oldest first. It is the seam
// the tray notice stream reads (R5.15, R5.17); the stream and the tray are not built here.
func (s *Service) PendingDesktop(ctx context.Context, login string, limit int) ([]DesktopNotice, error) {
	rows, err := s.cfg.DB.ListWaitingTray(ctx, login, limit)
	if err != nil {
		return nil, err
	}
	out := make([]DesktopNotice, 0, len(rows))
	for _, d := range rows {
		n, err := s.cfg.DB.GetNotice(ctx, d.NoticeID)
		if err != nil {
			continue
		}
		m := s.compose(d, n)
		out = append(out, DesktopNotice{DeliveryID: d.ID, Title: Title(n.Type, n.State), Body: m.Summary, Link: m.Link, At: m.Time})
	}
	return out, nil
}

// AckDesktop marks a desktop delivery as shown by the tray.
func (s *Service) AckDesktop(ctx context.Context, deliveryID string) error {
	d, err := s.cfg.DB.GetDelivery(ctx, deliveryID)
	if err != nil {
		return err
	}
	if d.Status != store.DeliveryWaitingTray {
		return nil
	}
	now := s.cfg.Now().UTC()
	return s.cfg.DB.SetDeliveryResult(ctx, d.ID, store.DeliveryDelivered, d.Attempt+1, now, "", now, true)
}
