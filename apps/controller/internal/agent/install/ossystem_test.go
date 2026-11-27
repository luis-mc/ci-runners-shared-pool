package install_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/agent/install"
)

func TestOSSystemRunSHAAndHTTPSOnly(t *testing.T) {
	s := install.NewOSSystem(install.OSOptions{})
	if runtime.GOOS != "windows" {
		var lines []string
		code, err := s.Run(context.Background(), []string{"sh", "-c", "echo out; echo err 1>&2; exit 3"}, func(st, l string) { lines = append(lines, st+":"+l) })
		if err != nil || code != 3 || len(lines) != 2 {
			t.Fatalf("code=%d err=%v lines=%v", code, err, lines)
		}
	}
	if _, err := s.Run(context.Background(), []string{"definitely-not-a-program-crsp"}, func(string, string) {}); err == nil {
		t.Error("a missing program must be an error")
	}
	p := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(p, []byte("abc"), 0o600); err != nil {
		t.Fatal(err)
	}
	h, err := s.SHA256(p)
	if err != nil || h != "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" {
		t.Errorf("sha = %s %v", h, err)
	}
	if err := s.Download(context.Background(), "http://download.docker.com/x", p, nil); err == nil || !strings.Contains(err.Error(), "https") {
		t.Errorf("plain http was accepted: %v", err)
	}
}
