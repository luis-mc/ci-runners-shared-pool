package debugaccess

import (
	"strings"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/redact"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/store"
)

// fakeToken is built at run time so no secret-shaped text sits in the source.
func fakeToken() string { return "gh" + "p_" + strings.Repeat("Ab3", 12) }

func fakeKey() string {
	return "-----BEGIN " + "OPENSSH PRIVATE KEY-----\n" + strings.Repeat("QUJD", 16) + "\n-----END " + "OPENSSH PRIVATE KEY-----\n"
}

// R5.25: only redacted text is stored, a secret split across writes is still caught, and the transcript expires in 30 days.
func TestRedactedAndKept30Days(t *testing.T) {
	e := newEnv(t)
	e.turnOn(nil)
	rd := redact.New()
	e.svc.Redact = rd.Redact
	d := e.mustOpen(admin, 1)
	rec := e.svc.NewRecorder(d)
	tok := fakeToken()
	key := fakeKey()
	// The token is split across two writes, and the key across three.
	for _, part := range []string{"$ env\nTOKEN=" + tok[:10], tok[10:] + "\n", key[:30], key[30:70], key[70:], "$ ls\nok\n"} {
		if err := rec.Write(e.ctx, []byte(part)); err != nil {
			t.Fatal(err)
		}
	}
	if err := rec.Close(e.ctx); err != nil {
		t.Fatal(err)
	}
	got, ok, err := e.svc.Transcript(e.ctx, admin, d.ID)
	if err != nil || !ok {
		t.Fatalf("transcript: %v %v", ok, err)
	}
	if strings.Contains(got.Text, tok) || strings.Contains(got.Text, strings.Repeat("QUJD", 16)) {
		t.Fatalf("secret stored: %q", got.Text)
	}
	if !strings.Contains(got.Text, "$ ls\nok\n") || !strings.Contains(got.Text, redact.Mask) {
		t.Fatalf("text = %q", got.Text)
	}
	if got.ExpiresAt != e.now.Add(30*24*time.Hour) {
		t.Fatalf("expires %v", got.ExpiresAt)
	}
	if _, _, err := e.svc.Transcript(e.ctx, viewer, d.ID); codeOf(err) != CodeViewer {
		t.Fatalf("viewer reads a transcript: %v", err)
	}
	// 30 days on, the sweep drops it; the session row stays.
	e.now = e.now.Add(30*24*time.Hour + time.Second)
	if _, err := e.svc.Sweep(e.ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := e.db.DebugTranscript(e.ctx, d.ID); ok {
		t.Fatal("transcript kept past 30 days")
	}
	if _, err := e.svc.Session(e.ctx, d.ID); err != nil {
		t.Fatal("session row gone with the transcript")
	}
}

// R6.6: 5 MB per session, then the copy is marked cut; the pool cap drops the oldest first.
func TestCaps(t *testing.T) {
	e := newEnv(t)
	e.turnOn(nil)
	d := e.mustOpen(admin, 1)
	rec := e.svc.NewRecorder(d)
	line := strings.Repeat("y", 1023) + "\n"
	big := []byte(strings.Repeat(line, 1024)) // 1 MB
	for i := 0; i < 6; i++ {
		if err := rec.Write(e.ctx, big); err != nil {
			t.Fatal(err)
		}
	}
	got, _, _ := e.db.DebugTranscript(e.ctx, d.ID)
	if got.Bytes != TranscriptMaxSession || !got.Truncated || int64(len(got.Text)) != got.Bytes {
		t.Fatalf("bytes %d truncated %v", got.Bytes, got.Truncated)
	}
	// Pool cap: three transcripts, a cap that holds two.
	for _, id := range []string{"a", "b", "c"} {
		e.now = e.now.Add(time.Minute)
		if err := e.db.AppendDebugTranscript(e.ctx, store.DebugTranscript{SessionID: id, Text: "xx", Bytes: 2,
			ExpiresAt: e.now.Add(TranscriptKeep), UpdatedAt: e.now}); err != nil {
			t.Fatal(err)
		}
	}
	n, err := e.db.PruneDebugTranscripts(e.ctx, e.now, 4)
	if err != nil || n != 2 {
		t.Fatalf("pruned %d, %v", n, err)
	}
	if _, ok, _ := e.db.DebugTranscript(e.ctx, "c"); !ok {
		t.Fatal("newest dropped")
	}
	if _, ok, _ := e.db.DebugTranscript(e.ctx, "a"); ok {
		t.Fatal("oldest kept")
	}
}

// R5.12: the shell environment is the allow list only.
func TestShellEnvironmentIsAllowListOnly(t *testing.T) {
	rd := redact.New()
	tok := fakeToken()
	pool := map[string]string{
		"PATH": "/usr/bin:/bin", "HOME": "/home/crsp-debug", "LANG": "C.UTF-8", "CI_TOOLS_DIR": "/opt/ci-tools",
		"GITHUB_TOKEN": tok, "ACTIONS_RUNNER_INPUT_JITCONFIG": "abc", "AWS_SECRET_ACCESS_KEY": "zzz",
		"RUNNER_TOKEN": "rrr", "CARGO_TARGET_DIR": "/opt/" + tok, "TZ": "UTC\nX=1",
	}
	got := strings.Join(ShellEnv(pool, rd.Redact), " ")
	for _, bad := range []string{"GITHUB_TOKEN", "JITCONFIG", "AWS_", "RUNNER_TOKEN", tok, "X=1"} {
		if strings.Contains(got, bad) {
			t.Fatalf("env carries %s: %s", bad, got)
		}
	}
	want := "CI_TOOLS_DIR=/opt/ci-tools HOME=/home/crsp-debug LANG=C.UTF-8 PATH=/usr/bin:/bin TERM=xterm-256color"
	if got != want {
		t.Fatalf("env = %s", got)
	}
	for _, n := range AllowedEnvNames() {
		if secretName.MatchString(n) {
			t.Fatalf("allow list holds a secret-looking name %s", n)
		}
	}
}

// Long is the 80% mark of debug.session.long (R5.27).
func TestLongAt80Percent(t *testing.T) {
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s := store.DebugSession{OpenedAt: at, EndsAt: at.Add(30 * time.Minute)}
	if Long(s, at.Add(23*time.Minute)) || !Long(s, at.Add(24*time.Minute)) {
		t.Fatal("80% mark")
	}
}
