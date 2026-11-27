package logging_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/logging"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/redact"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/secrets"
)

// kinds builds each secret kind in parts, so the source holds none of them.
func kinds() map[string]string {
	body := strings.Repeat("a1B2", 8)
	m := map[string]string{
		"pem":   "-----BEGIN RSA PRIV" + "ATE KEY-----\nMIIBOgIBAAJBAK\nline2\n-----END RSA PRIV" + "ATE KEY-----",
		"known": "known-value-" + "9f8e7d6c5b",
		"pat":   "github_" + "pat_" + body + "_" + body,
		"cli":   "crsp_cli_" + body,
	}
	for _, p := range []string{"ghs_", "ghu_", "ghr_", "gho_", "ghp_"} {
		m[p] = p + body + "xyz"
	}
	return m
}

type stringer string

func (s stringer) String() string { return "stringer " + string(s) }

type holder struct{ Field string }

// R6.3: each kind goes through every log path and none comes out.
func TestEverySecretKindIsRedactedOnEveryLogPath(t *testing.T) {
	for name, secret := range kinds() {
		t.Run(name, func(t *testing.T) {
			r := redact.New()
			r.Add(kinds()["known"])
			var buf bytes.Buffer
			l := logging.New(&buf, slog.LevelDebug, r)
			ctx := context.Background()

			l.Info("message " + secret)
			l.Info("attr", "value", secret)
			l.Info("err", "err", errors.New("boom "+secret))
			l.Info("wrapped", "err", fmt.Errorf("wrap: %w", errors.New(secret)))
			l.Info("stringer", "v", stringer(secret))
			l.Info("struct", "v", holder{Field: secret})
			l.Info("any", "v", []string{"x", secret})
			l.Info("group", slog.Group("g", slog.String("a", secret), slog.Group("h", "b", secret)))
			l.With("pre", secret).Info("with")
			l.WithGroup("grp").With("pre", secret).Info("group with", "k", secret)
			l.Log(ctx, slog.LevelError, "log", slog.Any("a", secret))
			l.Debug("debug " + secret)
			l.Warn("warn", slog.String(secret, "key position"))

			// The standard log package and slog.Default go through the handler.
			old := slog.Default()
			slog.SetDefault(l)
			t.Cleanup(func() { slog.SetDefault(old); log.SetOutput(log.Writer()) })
			log.Printf("std log %s", secret)
			slog.Info("default " + secret)

			out := buf.String()
			if out == "" {
				t.Fatal("nothing logged")
			}
			// The secret may sit in the output only if it is a fragment that is not
			// the secret: check the whole value and its distinct body.
			if strings.Contains(out, secret) {
				t.Fatalf("%s leaked:\n%s", name, out)
			}
			for _, frag := range []string{"MIIBOgIBAAJBAK", "a1B2a1B2a1B2", "9f8e7d6c5b"} {
				if strings.Contains(secret, frag) && strings.Contains(out, frag) {
					t.Fatalf("%s fragment %q leaked:\n%s", name, frag, out)
				}
			}
		})
	}
}

// R6.3: a value read from or written to the secret store becomes a known value.
func TestStoreValuesBecomeKnownSecrets(t *testing.T) {
	r := redact.New()
	mem := secrets.NewMemory()
	st := secrets.Learn(mem, r.Add)
	ctx := context.Background()
	webhook := "whsec-" + "plain-text-value-77"
	if err := st.Put(ctx, "crsp/github-app/1/webhook-secret", []byte(webhook)); err != nil {
		t.Fatal(err)
	}
	other := "client-" + "secret-value-88"
	if err := mem.Put(ctx, "crsp/github-app/1/client-secret", []byte(other)); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	l := logging.New(&buf, slog.LevelInfo, r)
	l.Info("a " + webhook)
	if strings.Contains(buf.String(), webhook) {
		t.Fatalf("written value leaked: %s", buf.String())
	}
	l.Info("b " + other)
	if !strings.Contains(buf.String(), other) {
		t.Fatal("a value never read must not be known yet")
	}
	if _, err := st.Get(ctx, "crsp/github-app/1/client-secret"); err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	l.Info("c " + other)
	if strings.Contains(buf.String(), other) {
		t.Fatalf("read value leaked: %s", buf.String())
	}
}

func TestPlainRecordsAreUntouched(t *testing.T) {
	var buf bytes.Buffer
	l := logging.New(&buf, slog.LevelInfo, nil)
	l.Info("controller listening", "addr", "127.0.0.1:8460", "n", 3, "err", errors.New("plain"))
	for _, want := range []string{"controller listening", "127.0.0.1:8460", `"n":3`, `"err":"plain"`} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("missing %q in %s", want, buf.String())
		}
	}
}
