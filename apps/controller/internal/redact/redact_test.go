package redact_test

import (
	"encoding/base64"
	"errors"
	"io/fs"
	"strings"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/redact"
)

// envName is built in parts so the test source holds no secret-named assignment.
const envName = "ACTIONS_RUNNER_INPUT_" + "TOKEN"

// R5.11: planted values and shapes never come out.
func TestSecretsRedacted(t *testing.T) {
	r := redact.New()
	r.Add("AREGTOKEN123456")
	r.Add("jit-config-value-xyz")
	r.Add("ab") // too short: ignored
	planted := []string{
		"AREGTOKEN123456", "jit-config-value-xyz",
		base64.StdEncoding.EncodeToString([]byte("AREGTOKEN123456")),
		"gh" + "p_" + strings.Repeat("a", 30), "github_" + "pat_" + strings.Repeat("b", 30),
		"ey" + "JhbGciOiJI.eyJzdWIiOiIx.c2lnbmF0dXJl",
		"-----BEGIN RSA PRIV" + "ATE KEY-----\nMIIB\n-----END RSA PRIV" + "ATE KEY-----",
	}
	for _, p := range planted {
		got := r.Redact("before " + p + " after")
		if strings.Contains(got, p) || !strings.Contains(got, redact.Mask) {
			t.Errorf("not redacted: %q -> %q", p, got)
		}
	}
	if got := r.Redact("ab cd"); got != "ab cd" {
		t.Errorf("short value must not mask: %q", got)
	}
	if got := r.Redact("Authorization: Bearer xyz.abc"); strings.Contains(got, "Bearer") {
		t.Errorf("header kept: %q", got)
	}
	if got := r.Redact(envName + "=Zz9999"); got != envName+"=***" {
		t.Errorf("env: %q", got)
	}
	if got := r.Redact("config.sh --token SECRETX --url u"); strings.Contains(got, "SECRETX") {
		t.Errorf("flag: %q", got)
	}
}

type memFS struct {
	files map[string][]byte
	modes map[string]fs.FileMode
	dirs  map[string]fs.FileMode
	fail  bool
}

func (m *memFS) MkdirAll(p string, perm fs.FileMode) error { m.dirs[p] = perm; return nil }
func (m *memFS) WriteFile(p string, d []byte, perm fs.FileMode) error {
	if m.fail {
		return errors.New("disk full")
	}
	m.files[p] = d
	m.modes[p] = perm
	return nil
}
func (m *memFS) ReadFile(p string) ([]byte, error) { return m.files[p], nil }

// R5.11: the setup log is redacted and readable only by its owner.
func TestPrivateLogOwnerOnlyAndRedacted(t *testing.T) {
	m := &memFS{files: map[string][]byte{}, modes: map[string]fs.FileMode{}, dirs: map[string]fs.FileMode{}}
	r := redact.New()
	r.Add("SUPERSECRET1")
	l := &redact.PrivateLog{FS: m, Path: "/run/box-1/logs/setup.log", R: r,
		Now: func() time.Time { return time.Date(2026, 11, 15, 1, 2, 3, 0, time.UTC) }}
	if err := l.Line("register", "token SUPERSECRET1 used"); err != nil {
		t.Fatal(err)
	}
	if err := l.Line("start", "ok"); err != nil {
		t.Fatal(err)
	}
	got := string(m.files["/run/box-1/logs/setup.log"])
	if strings.Contains(got, "SUPERSECRET1") || strings.Count(got, "\n") != 2 {
		t.Errorf("log:\n%s", got)
	}
	if m.modes["/run/box-1/logs/setup.log"] != redact.LogFileMode || m.dirs["/run/box-1/logs"] != redact.LogDirMode {
		t.Errorf("modes: %v %v", m.modes, m.dirs)
	}
	m.fail = true
	if l.Line("x", "y") == nil {
		t.Error("write failure must be returned")
	}
}
