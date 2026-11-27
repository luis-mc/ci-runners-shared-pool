package agent_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/agent"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/secrets"
)

// R5.7: the Linux fallback is a file with mode 0600 in a folder of mode 0700.
func TestFileStoreModes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX modes")
	}
	dir := filepath.Join(t.TempDir(), "secrets")
	s, err := agent.NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := s.Put(ctx, agent.MachineKeySecret, []byte("k1")); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(dir); st.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode = %v", st.Mode().Perm())
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("%d files (a temp file was left behind?)", len(entries))
	}
	if info, _ := entries[0].Info(); info.Mode().Perm() != 0o600 {
		t.Fatalf("file mode = %v", info.Mode().Perm())
	}
	got, err := s.Get(ctx, agent.MachineKeySecret)
	if err != nil || string(got) != "k1" {
		t.Fatalf("%q %v", got, err)
	}
	// Replace, then delete; a missing key is ErrNotFound and deleting it is fine.
	_ = s.Put(ctx, agent.MachineKeySecret, []byte("k2"))
	if got, _ := s.Get(ctx, agent.MachineKeySecret); string(got) != "k2" {
		t.Fatalf("replace: %q", got)
	}
	if err := s.Delete(ctx, agent.MachineKeySecret); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, agent.MachineKeySecret); err != secrets.ErrNotFound {
		t.Fatalf("err = %v", err)
	}
	if err := s.Delete(ctx, agent.MachineKeySecret); err != nil {
		t.Fatalf("deleting a missing key: %v", err)
	}
	if s.Backend() != "file" {
		t.Fatalf("backend = %s", s.Backend())
	}
	// The key never lands in a file name.
	_ = s.Put(ctx, agent.MachineKeySecret, []byte("k3"))
	entries, _ = os.ReadDir(dir)
	for _, e := range entries {
		if e.Name() == string(agent.MachineKeySecret) {
			t.Fatal("secret key name used as a file name")
		}
	}
}
