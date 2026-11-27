package webui

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

// OPS17: a browser refusing the self-signed certificate logs at DEBUG, other
// server errors at WARN.
func TestServerErrorLogLevels(t *testing.T) {
	var buf bytes.Buffer
	l := ServerErrorLog(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	l.Print("http: TLS handshake error from 192.168.1.5:5000: remote error: tls: unknown certificate")
	l.Print("http: Accept error: too many open files")
	out := buf.String()
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], "level=DEBUG") || !strings.Contains(lines[1], "level=WARN") {
		t.Fatalf("levels:\n%s", out)
	}
}
