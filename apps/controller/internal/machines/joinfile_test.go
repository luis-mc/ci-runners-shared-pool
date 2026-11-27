package machines_test

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/machines"
)

type fakePackages struct {
	sums []byte
	err  error
}

func (f fakePackages) Sums(context.Context) ([]byte, string, error) { return f.sums, "1.2.3", f.err }

var joinSums = []byte(strings.Repeat("a", 64) + "  crsp-linux-amd64.deb\n" + strings.Repeat("b", 64) + "  crsp-windows-amd64.msi\n" +
	strings.Repeat("c", 64) + "  crsp-darwin-arm64.pkg\n")

func fileEnv(t *testing.T, p machines.PackageSource) *env {
	return newEnv(t, func(o *machines.Options) { o.Packages = p; o.ConsoleURL = "https://pool.lan:8460" })
}

var codeInFile = regexp.MustCompile(`(?:JOIN_CODE|\$JoinCode) = ?'([^']+)'|JOIN_CODE='([^']+)'`)

func codeOf(t *testing.T, content []byte) string {
	t.Helper()
	m := codeInFile.FindSubmatch(content)
	if m == nil {
		t.Fatalf("no join code in the file:\n%s", content)
	}
	if len(m[1]) > 0 {
		return string(m[1])
	}
	return string(m[2])
}

// R5.65: the file holds a code that works once and lives 15 minutes (R5.4).
func TestJoinFileCodeWorksOnceAndExpires(t *testing.T) {
	e := fileEnv(t, fakePackages{sums: joinSums})
	ctx := context.Background()
	f, err := e.Svc.CreateJoinFile(ctx, "owner", "linux", 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := f.JoinCode.ExpiresAt.Sub(f.JoinCode.CreatedAt); got != 15*time.Minute {
		t.Fatalf("lifetime %v, want 15m", got)
	}
	if want := "crsp-join-linux-until-20261114T1215Z.sh"; f.Name != want {
		t.Errorf("name %q, want %q", f.Name, want)
	}
	for _, want := range []string{"pool.lan:8461", "https://pool.lan:8460", e.CA.Fingerprint()} {
		if !strings.Contains(string(f.Content), want) {
			t.Errorf("file lacks %q", want)
		}
	}
	code := codeOf(t, f.Content)
	if _, err := joinWith(e, code); err != nil {
		t.Fatalf("the code of the file did not join: %v", err)
	}
	if _, err := joinWith(e, code); err == nil {
		t.Fatal("the code joined twice")
	}

	g, err := e.Svc.CreateJoinFile(ctx, "owner", "windows", 0)
	if err != nil {
		t.Fatal(err)
	}
	e.Clk.Add(16 * time.Minute)
	if _, err := joinWith(e, codeOf(t, g.Content)); err == nil {
		t.Fatal("an expired code joined")
	}
}

// R5.4 stays: a new file makes the older unused code invalid.
func TestJoinFileWithdrawsAnOlderCode(t *testing.T) {
	e := fileEnv(t, fakePackages{sums: joinSums})
	ctx := context.Background()
	a, _ := e.Svc.CreateJoinFile(ctx, "owner", "linux", 0)
	if _, err := e.Svc.CreateJoinFile(ctx, "owner", "macos", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := joinWith(e, codeOf(t, a.Content)); err == nil {
		t.Fatal("the older code still joined")
	}
}

// R5.65, R6.4: the audit entry says who, the OS and the expiry; never the code.
func TestJoinFileAuditHasNoCode(t *testing.T) {
	e := fileEnv(t, fakePackages{sums: joinSums})
	f, err := e.Svc.CreateJoinFile(context.Background(), "alice", "windows", 0)
	if err != nil {
		t.Fatal(err)
	}
	code := codeOf(t, f.Content)
	found := false
	for _, a := range e.Repo.AuditLog() {
		if strings.Contains(fmt.Sprintf("%+v", a), code) {
			t.Errorf("the audit log holds the code: %+v", a)
		}
		if a.Action == "join_file.create" && a.Actor == "alice" && a.Subject == f.JoinCode.ID && strings.Contains(a.Detail, "os windows") {
			found = true
		}
	}
	if !found {
		t.Errorf("no join_file.create entry: %v", e.Repo.AuditLog())
	}
}

func TestJoinFileRefusals(t *testing.T) {
	ctx := context.Background()
	if _, err := fileEnv(t, nil).Svc.CreateJoinFile(ctx, "owner", "linux", 0); !errors.Is(err, machines.ErrNotSupported) {
		t.Errorf("no packages: %v", err)
	}
	if _, err := fileEnv(t, fakePackages{err: errors.New("no network")}).Svc.CreateJoinFile(ctx, "owner", "linux", 0); !errors.Is(err, machines.ErrNotSupported) {
		t.Errorf("packages unavailable: %v", err)
	}
	e := fileEnv(t, fakePackages{sums: joinSums})
	if _, err := e.Svc.CreateJoinFile(ctx, "owner", "plan9", 0); !errors.Is(err, machines.ErrInvalid) {
		t.Errorf("bad os: %v", err)
	}
	if _, err := e.Svc.CreateJoinFile(ctx, "owner", "linux", 25*time.Hour); err == nil {
		t.Error("a 25 hour code must be refused")
	}
	// A release with no package for the OS: nothing is left behind that joins.
	only := fileEnv(t, fakePackages{sums: []byte(strings.Repeat("a", 64) + "  crsp-linux-amd64.deb\n")})
	before := len(only.Repo.AuditLog())
	if _, err := only.Svc.CreateJoinFile(ctx, "owner", "windows", 0); !errors.Is(err, machines.ErrNotSupported) {
		t.Errorf("no msi: %v", err)
	}
	for _, a := range only.Repo.AuditLog()[before:] {
		if a.Action == "join_file.create" {
			t.Errorf("a failed file was audited as made: %+v", a)
		}
	}
}
