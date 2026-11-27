package packages

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/update"
)

type fakeSource struct {
	rel update.Release
	err error
}

func (f fakeSource) Latest(context.Context) (update.Release, error) { return f.rel, f.err }

func sum(b string) string { h := sha256.Sum256([]byte(b)); return hex.EncodeToString(h[:]) }

// newStore serves a release with one good package and one whose bytes do not match.
func newStore(t *testing.T, calls *[]string) *Store {
	t.Helper()
	files := map[string]string{
		"https://example.test/SHA256SUMS": sum("deb-bytes") + "  crsp-linux-amd64.deb\n" + sum("expected") + "  crsp-linux-arm64.deb\n",
		"https://example.test/good":       "deb-bytes",
		"https://example.test/bad":        "tampered",
	}
	return &Store{
		Source: fakeSource{rel: update.Release{Version: "1.2.3", Assets: map[string]string{
			"SHA256SUMS": "https://example.test/SHA256SUMS", "crsp-linux-amd64.deb": "https://example.test/good", "crsp-linux-arm64.deb": "https://example.test/bad",
			"crsp-linux-amd64.rpm": "https://example.test/good"}}},
		Dir: t.TempDir(),
		Fetch: func(_ context.Context, url, dst string) error {
			*calls = append(*calls, url)
			return os.WriteFile(dst, []byte(files[url]), 0o600)
		},
		AllowURL: func(string) bool { return true },
	}
}

func TestSumsAndVerifiedPackage(t *testing.T) {
	var calls []string
	s := newStore(t, &calls)
	sums, v, err := s.Sums(context.Background())
	if err != nil || v != "1.2.3" || !strings.Contains(string(sums), "crsp-linux-amd64.deb") {
		t.Fatalf("sums %q %q %v", sums, v, err)
	}
	f, n, err := s.Open(context.Background(), "crsp-linux-amd64.deb")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	b, _ := io.ReadAll(f)
	if string(b) != "deb-bytes" || n != int64(len(b)) {
		t.Errorf("served %q (%d)", b, n)
	}
	g, _, err := s.Open(context.Background(), "crsp-linux-amd64.deb")
	if err != nil {
		t.Fatal(err)
	}
	_ = g.Close()
	if len(calls) != 2 {
		t.Errorf("fetched %v, want SHA256SUMS and the package once", calls)
	}
}

func TestPackageThatDoesNotMatchSumsIsNeverServedOrKept(t *testing.T) {
	var calls []string
	s := newStore(t, &calls)
	_, _, err := s.Open(context.Background(), "crsp-linux-arm64.deb")
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err %v, want ErrUnavailable", err)
	}
	if _, statErr := os.Stat(filepath.Join(s.Dir, "1.2.3", "crsp-linux-arm64.deb")); !os.IsNotExist(statErr) {
		t.Errorf("the mismatching file was kept")
	}
}

func TestNameNotInSumsIsRefused(t *testing.T) {
	var calls []string
	s := newStore(t, &calls)
	for _, name := range []string{"crsp-linux-amd64.rpm", "../etc/passwd", "SHA256SUMS", ""} {
		if _, _, err := s.Open(context.Background(), name); !errors.Is(err, ErrNotListed) {
			t.Errorf("%q: err %v, want ErrNotListed", name, err)
		}
	}
}

func TestNoReleaseIsUnavailable(t *testing.T) {
	s := &Store{Source: fakeSource{err: update.ErrNoRelease}, Dir: t.TempDir()}
	if _, _, err := s.Sums(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Errorf("err %v", err)
	}
	if _, _, err := (&Store{}).Sums(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Errorf("nil source: err %v", err)
	}
}
