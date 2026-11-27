package secrets

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func exerciseStore(t *testing.T, s Store) {
	t.Helper()
	ctx := context.Background()
	if _, err := s.Get(ctx, "crsp/a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing key: %v", err)
	}
	pem := []byte("-----BEGIN KEY-----\nline1\nline2\n-----END KEY-----\n")
	if err := s.Put(ctx, "crsp/a", pem); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, "crsp/a")
	if err != nil || string(got) != string(pem) {
		t.Fatalf("round trip: %q %v", got, err)
	}
	if err := s.Put(ctx, "crsp/a", []byte("new")); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get(ctx, "crsp/a"); string(got) != "new" {
		t.Fatalf("replace: %q", got)
	}
	if err := s.Delete(ctx, "crsp/a"); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, "crsp/a"); err != nil {
		t.Fatalf("delete missing must not fail: %v", err)
	}
	if _, err := s.Get(ctx, "crsp/a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after delete: %v", err)
	}
}

func TestMemoryStore(t *testing.T) { exerciseStore(t, NewMemory()) }

func TestMemoryDownFailsClosed(t *testing.T) {
	m := NewMemory()
	m.Down = true
	if _, err := m.Get(context.Background(), "k"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("got %v", err)
	}
	if err := m.Put(context.Background(), "k", []byte("v")); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("got %v", err)
	}
}

func TestFileStore(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "secrets")
	f, err := NewFile(dir)
	if err != nil {
		t.Fatal(err)
	}
	exerciseStore(t, f)
	if f.Backend() != "file" {
		t.Fatal(f.Backend())
	}
}

func TestFileStoreModes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX modes")
	}
	dir := filepath.Join(t.TempDir(), "secrets")
	f, err := NewFile(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Put(context.Background(), "crsp/x", []byte("v")); err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(dir)
	if st.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode %v", st.Mode().Perm())
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("want one file, got %d (temp file left behind?)", len(entries))
	}
	fi, _ := entries[0].Info()
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("file mode %v", fi.Mode().Perm())
	}
	if strings.Contains(entries[0].Name(), "crsp/x") {
		t.Fatal("file name must not hold the raw key")
	}
}

// fakeTool imitates secret-tool and security with an in-memory map.
type fakeTool struct {
	data  map[string]string
	calls [][]string
	stdin []string
}

func exitErr(t *testing.T, code int) error {
	t.Helper()
	err := exec.CommandContext(context.Background(), "sh", "-c", "exit "+string(rune('0'+code%10))).Run()
	if code == 44 {
		err = exec.CommandContext(context.Background(), "sh", "-c", "exit 44").Run()
	}
	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		t.Skip("cannot build an ExitError here")
	}
	return err
}

func (f *fakeTool) secretTool(t *testing.T) Runner {
	return func(_ context.Context, stdin []byte, name string, args ...string) ([]byte, error) {
		f.calls = append(f.calls, append([]string{name}, args...))
		f.stdin = append(f.stdin, string(stdin))
		switch args[0] {
		case "store":
			f.data[args[len(args)-1]] = string(stdin)
			return nil, nil
		case "lookup":
			v, ok := f.data[args[len(args)-1]]
			if !ok {
				return nil, exitErr(t, 1)
			}
			return []byte(v), nil
		case "clear":
			delete(f.data, args[len(args)-1])
			return nil, nil
		}
		return nil, errors.New("unexpected")
	}
}

func TestSecretServiceUsesStdinNotArgs(t *testing.T) {
	ft := &fakeTool{data: map[string]string{}}
	s := NewSecretService(ft.secretTool(t))
	exerciseStore(t, s)
	for _, c := range ft.calls {
		for _, a := range c {
			if strings.Contains(a, "BEGIN KEY") || strings.Contains(a, "6e6577") {
				t.Fatalf("value leaked to the command line: %v", c)
			}
		}
	}
}

func TestKeychainRoundTrip(t *testing.T) {
	data := map[string]string{}
	run := func(_ context.Context, _ []byte, _ string, args ...string) ([]byte, error) {
		acct := ""
		for i, a := range args {
			if a == "-a" {
				acct = args[i+1]
			}
		}
		switch args[0] {
		case "add-generic-password":
			data[acct] = args[len(args)-1]
			return nil, nil
		case "find-generic-password":
			v, ok := data[acct]
			if !ok {
				return nil, exitErr(t, 44)
			}
			return []byte(v + "\n"), nil
		case "delete-generic-password":
			if _, ok := data[acct]; !ok {
				return nil, exitErr(t, 44)
			}
			delete(data, acct)
			return nil, nil
		}
		return nil, errors.New("unexpected")
	}
	exerciseStore(t, NewKeychain(run))
}

func TestOpenReturnsAStore(t *testing.T) {
	s, err := Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if s.Backend() == "" {
		t.Fatal("backend must be named")
	}
}

// dpapiLike refuses an empty value the way Windows DPAPI does.
func dpapiLike(b []byte) ([]byte, error) {
	if len(b) == 0 {
		return nil, errors.New("empty secret")
	}
	return b, nil
}

func TestFileTransformRefusesEmpty(t *testing.T) {
	f, err := NewFile(filepath.Join(t.TempDir(), "s"))
	if err != nil {
		t.Fatal(err)
	}
	f.transform, f.untransform = dpapiLike, func(b []byte) ([]byte, error) { return b, nil }
	ctx := context.Background()
	if err := f.Put(ctx, "crsp/a", []byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := f.Put(ctx, "crsp/b", nil); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("empty value: %v", err)
	}
}
