package persistent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/internal/backend/sys"
	"github.com/luis-mc/ci-runners-shared-pool/internal/backend/sys/sysfake"
	"github.com/luis-mc/ci-runners-shared-pool/internal/runners"
)

func meta() Meta {
	return Meta{Name: "box-1", Account: "crsp-box-1", Folder: "/r/box-1", GitHubURL: "https://github.com/o/r",
		Labels: []string{"self-hosted", "linux"}, Service: "s"}
}

// R5.20 (port of the test_dl310 rerun tests).
func TestDecide(t *testing.T) {
	w := meta()
	cases := []struct {
		name string
		have func() *Meta
		env  string
		want Action
	}{
		{"nothing there", func() *Meta { return nil }, "", Fresh},
		{"all match", func() *Meta { m := meta(); return &m }, "E", Already},
		{"label order does not matter", func() *Meta { m := meta(); m.Labels = []string{"linux", "self-hosted"}; return &m }, "E", Already},
		{"other account", func() *Meta { m := meta(); m.Account = "old"; return &m }, "E", Reregister},
		{"other labels", func() *Meta { m := meta(); m.Labels = []string{"self-hosted"}; return &m }, "E", Reregister},
		{"other repository", func() *Meta { m := meta(); m.GitHubURL = "https://github.com/o/x"; return &m }, "E", Reregister},
		{"other symlink right", func() *Meta { m := meta(); m.SymlinkRight = true; return &m }, "E", UpdateRights},
		{"stale env only", func() *Meta { m := meta(); return &m }, "old", RewriteEnv},
		{"account beats env", func() *Meta { m := meta(); m.Account = "old"; return &m }, "old", Reregister},
	}
	for _, c := range cases {
		if got := Decide(c.have(), c.env, w, "E"); got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
}

func TestEnvSortedAndServiceName(t *testing.T) {
	if got := Env(map[string]string{"B": "2", "A": "1"}); got != "A=1\nB=2\n" {
		t.Errorf("%q", got)
	}
	if got := ServiceName("https://github.com/luis-mc/demo", "box-1"); got != "actions.runner.luis-mc-demo.box-1" {
		t.Errorf("%s", got)
	}
	if got := ServiceName("https://github.com/acme", "box-1"); got != "actions.runner.acme.box-1" {
		t.Errorf("%s", got)
	}
}

func TestConfigArgsOneByOne(t *testing.T) {
	a := ConfigArgs("https://github.com/o/r", "n", "grp", []string{"self-hosted", "linux"})
	want := []string{"--unattended", "--url", "https://github.com/o/r", "--name", "n", "--labels", "self-hosted,linux",
		"--disableupdate", "--replace", "--runnergroup", "grp"}
	if strings.Join(a, "|") != strings.Join(want, "|") {
		t.Errorf("%v", a)
	}
	for _, x := range a {
		if strings.Contains(x, "TOKEN") || x == "--token" {
			t.Error("the token never goes in an argument")
		}
	}
}

func TestMetaRoundTrip(t *testing.T) {
	f := sysfake.NewFS()
	if m, err := ReadMeta(f, "/x/"+MetaFile); m != nil || err != nil {
		t.Fatalf("missing file: %v %v", m, err)
	}
	if err := WriteMeta(f, "/x/"+MetaFile, meta()); err != nil {
		t.Fatal(err)
	}
	m, err := ReadMeta(f, "/x/"+MetaFile)
	if err != nil || m.Name != "box-1" {
		t.Fatalf("%v %v", m, err)
	}
}

// R5.37: a download that does not match is deleted and refused.
func TestDownloadDeletesOnMismatch(t *testing.T) {
	f := sysfake.NewFS()
	d := sys.Deps{FS: f, Fetch: &sysfake.Fetcher{FS: f, Body: []byte("x")},
		Verify: func(string, string) error { return errors.New("mismatch") }}
	if err := Download(context.Background(), d, "u", "/a", "h"); err == nil {
		t.Fatal("want error")
	}
	if f.Exists("/a") {
		t.Error("file kept")
	}
}

// R5.37: a wrong hash is deleted and fetched once more; the
// second download that matches is kept.
func TestDownloadFixesAWrongHashByRedownload(t *testing.T) {
	f := sysfake.NewFS()
	fe := &sysfake.Fetcher{FS: f, Body: []byte("x")}
	calls := 0
	d := sys.Deps{FS: f, Fetch: fe, Verify: func(string, string) error {
		calls++
		if calls == 1 {
			return runners.ErrHashMismatch
		}
		return nil
	}}
	if err := Download(context.Background(), d, "u", "/a", "h"); err != nil || calls != 2 || !f.Exists("/a") {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
	// Wrong twice: refused and deleted. A bad pin is never retried.
	calls = 0
	d.Verify = func(string, string) error { calls++; return runners.ErrHashMismatch }
	if err := Download(context.Background(), d, "u", "/b", "h"); err == nil || calls != 2 || f.Exists("/b") {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
	calls = 0
	d.Verify = func(string, string) error { calls++; return runners.ErrBadHash }
	if err := Download(context.Background(), d, "u", "/c", "h"); err == nil || calls != 1 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
}

// R5.37, R5.79: a body whose real SHA-256 differs from the pin is refused and
// deleted after one more try; a body that matches the pin is kept.
func TestDownloadChecksTheRealHashAgainstThePin(t *testing.T) {
	f := sysfake.NewFS()
	fe := &sysfake.Fetcher{FS: f, Body: []byte("runner bytes")}
	verify := func(path, want string) error {
		b, err := f.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(b)
		if hex.EncodeToString(sum[:]) != want {
			return runners.ErrHashMismatch
		}
		return nil
	}
	d := sys.Deps{FS: f, Fetch: fe, Verify: verify}
	good := sha256.Sum256([]byte("runner bytes"))
	if err := Download(context.Background(), d, "u", "/ok", hex.EncodeToString(good[:])); err != nil || !f.Exists("/ok") {
		t.Fatalf("matching pin: %v", err)
	}
	err := Download(context.Background(), d, "u", "/bad", strings.Repeat("ab", 32))
	if !errors.Is(err, runners.ErrHashMismatch) || f.Exists("/bad") {
		t.Fatalf("mismatch must be refused and deleted: %v", err)
	}
}
