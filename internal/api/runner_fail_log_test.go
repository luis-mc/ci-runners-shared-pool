package api

import (
	"bytes"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"testing"
)

// R5.33: an unknown error answers "internal error" and the real one is logged at ERROR.
func TestRunnerFailLogsTheRealError(t *testing.T) {
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(old)
	code, body := runnerFail(errors.New(`unknown runner scope "pool"`))
	if code != http.StatusInternalServerError || body.Error.Message != "internal error" {
		t.Fatalf("%d %+v", code, body)
	}
	if out := buf.String(); !strings.Contains(out, "level=ERROR") || !strings.Contains(out, "unknown runner scope") {
		t.Fatalf("log = %q", out)
	}
}
