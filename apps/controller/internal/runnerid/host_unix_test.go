//go:build !windows

package runnerid

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestHostFiles(t *testing.T) {
	dir := t.TempDir()
	pw := filepath.Join(dir, "passwd")
	gr := filepath.Join(dir, "group")
	if err := os.WriteFile(pw, []byte("alice:x:64005:64006:A:/home/alice:/bin/sh\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(gr, []byte("devs:x:64010:alice\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h := HostFiles(pw, gr)
	for id, want := range map[int]bool{64005: true, 64006: true, 64010: true, 64011: false} {
		got, err := h(context.Background(), id)
		if err != nil || got != want {
			t.Errorf("id %d = %v, %v; want %v", id, got, err, want)
		}
	}
	if got, err := HostFiles(filepath.Join(dir, "none"), "")(context.Background(), 1); err != nil || got {
		t.Errorf("missing files = %v, %v", got, err)
	}
}
