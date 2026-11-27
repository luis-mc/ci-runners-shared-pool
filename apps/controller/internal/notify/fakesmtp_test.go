package notify

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"net"
	"strings"
	"sync"
	"testing"
)

// fakeSMTP is an in-process SMTP server: EHLO, optional STARTTLS, AUTH PLAIN,
// MAIL, RCPT, DATA and QUIT. It records what it was told.
type fakeSMTP struct {
	t        *testing.T
	ln       net.Listener
	tlsCfg   *tls.Config // nil: STARTTLS is not offered
	wantAuth string      // "user\x00pass" expected after the base64 of AUTH PLAIN; empty: no auth offered
	rejectTo string      // RCPT to this address gets 550

	mu       sync.Mutex
	From, To string
	Data     string
	AuthLine string // the decoded AUTH PLAIN payload
	Secure   bool   // the message came over TLS
}

func newFakeSMTP(t *testing.T, tlsCfg *tls.Config, wantAuth string) *fakeSMTP {
	t.Helper()
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeSMTP{t: t, ln: ln, tlsCfg: tlsCfg, wantAuth: wantAuth}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for c, err := ln.Accept(); err == nil; c, err = ln.Accept() {
			go f.serve(c)
		}
	}()
	return f
}

func (f *fakeSMTP) port() int { return f.ln.Addr().(*net.TCPAddr).Port }

func (f *fakeSMTP) serve(c net.Conn) {
	defer func() { _ = c.Close() }()
	secure := false
	r := bufio.NewReader(c)
	say := func(s string) { _, _ = c.Write([]byte(s + "\r\n")) }
	say("220 fake ESMTP")
	for line, err := r.ReadString('\n'); err == nil; line, err = r.ReadString('\n') {
		line = strings.TrimRight(line, "\r\n")
		up := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(up, "EHLO"), strings.HasPrefix(up, "HELO"):
			_, _ = c.Write([]byte("250-fake\r\n"))
			if f.tlsCfg != nil && !secure {
				_, _ = c.Write([]byte("250-STARTTLS\r\n"))
			}
			if f.wantAuth != "" {
				_, _ = c.Write([]byte("250-AUTH PLAIN\r\n"))
			}
			say("250 8BITMIME")
		case up == "STARTTLS":
			say("220 go ahead")
			tc := tls.Server(c, f.tlsCfg)
			if err := tc.HandshakeContext(context.Background()); err != nil {
				return
			}
			c, secure = tc, true
			r = bufio.NewReader(c)
			say = func(s string) { _, _ = c.Write([]byte(s + "\r\n")) }
		case strings.HasPrefix(up, "AUTH PLAIN "):
			raw, _ := base64.StdEncoding.DecodeString(line[len("AUTH PLAIN "):])
			f.mu.Lock()
			f.AuthLine = strings.ReplaceAll(string(raw), "\x00", "|")
			f.mu.Unlock()
			if string(raw) == "\x00"+f.wantAuth {
				say("235 ok")
			} else {
				say("535 bad credentials")
			}
		case strings.HasPrefix(up, "MAIL FROM:"):
			f.mu.Lock()
			f.From = strings.Trim(strings.Fields(line[len("MAIL FROM:"):])[0], "<> ")
			f.mu.Unlock()
			say("250 ok")
		case strings.HasPrefix(up, "RCPT TO:"):
			to := strings.Trim(line[len("RCPT TO:"):], "<> ")
			if f.rejectTo != "" && to == f.rejectTo {
				say("550 no such user")
				continue
			}
			f.mu.Lock()
			f.To = to
			f.mu.Unlock()
			say("250 ok")
		case up == "DATA":
			say("354 go")
			var b strings.Builder
			for l, err := r.ReadString('\n'); ; l, err = r.ReadString('\n') {
				if err != nil {
					return
				}
				if l == ".\r\n" {
					break
				}
				b.WriteString(l)
			}
			f.mu.Lock()
			f.Data, f.Secure = b.String(), secure
			f.mu.Unlock()
			say("250 queued")
		case up == "QUIT":
			say("221 bye")
			return
		default:
			say("502 not here")
		}
	}
}

func (f *fakeSMTP) got() (from, to, data, auth string, secure bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.From, f.To, f.Data, f.AuthLine, f.Secure
}
