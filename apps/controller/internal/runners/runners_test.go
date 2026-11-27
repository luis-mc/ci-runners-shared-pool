package runners

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/addresses"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// R5.13 (port of the slug tests of test_dl310_windows_runner.py).
func TestAccountName(t *testing.T) {
	valid := regexp.MustCompile(`^crsp-[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)
	for _, in := range []string{"box-1", "Build Box #2", "UPPER_case.name", "a", "a-very-long-runner-name-that-never-fits-in-twenty", "---", "ünï"} {
		got := AccountName(in, nil)
		if len(got) > MaxAccountLen || !valid.MatchString(got) && got != "crsp-runner" {
			t.Errorf("%q -> %q", in, got)
		}
		if again := AccountName(in, nil); again != got {
			t.Errorf("%q not stable: %q then %q", in, got, again)
		}
	}
	if got := AccountName("box-1", nil); got != "crsp-box-1" {
		t.Errorf("short name stays plain: %s", got)
	}
	long := AccountName("a-very-long-runner-name-that-never-fits-in-twenty", nil)
	if !regexp.MustCompile(`-[0-9a-f]{4}$`).MatchString(long) || len(long) != MaxAccountLen {
		t.Errorf("a name that does not fit gets a 4-character hash: %s", long)
	}
	if AccountName("a-very-long-runner-name-that-never-fits-in-twenty", nil) == AccountName("a-very-long-runner-name-that-never-fits-in-thirty", nil) {
		t.Error("two long names share an account")
	}
	// Taken by another: a hash is added; stable on every run.
	taken := func(n string) bool { return n == "crsp-box-1" }
	got := AccountName("box-1", taken)
	if got == "crsp-box-1" || len(got) > MaxAccountLen || got != AccountName("box-1", taken) {
		t.Errorf("taken name: %s", got)
	}
}

func TestValidSHA256(t *testing.T) {
	good := sha256.Sum256([]byte("x"))
	if !ValidSHA256(hex.EncodeToString(good[:])) {
		t.Error("a real hash is valid")
	}
	for _, bad := range []string{"", "abc", strings.Repeat("0", 64), strings.Repeat("a", 64), strings.Repeat("z", 64), strings.Repeat("ab", 33)} {
		if ValidSHA256(bad) {
			t.Errorf("%q must be wrong", bad)
		}
	}
}

// R5.37 (port of test_dl310 pin tests and test_dl165_pool_refresh_hash_result.py).
func TestPinnedHash(t *testing.T) {
	for _, os := range []string{"linux", "windows", "macos"} {
		for _, arch := range []string{"x64", "arm64"} {
			p, err := PinFor(os, arch)
			if err != nil || !ValidSHA256(p.SHA256) || p.Version == "" {
				t.Errorf("%s/%s: %+v %v", os, arch, p, err)
			}
			if !strings.HasPrefix(p.DownloadURL(addresses.Set{}), "https://github.com/actions/runner/releases/download/v"+p.Version+"/actions-runner-") {
				t.Errorf("url %s", p.DownloadURL(addresses.Set{}))
			}
		}
	}
	if _, err := PinFor("plan9", "x64"); err == nil {
		t.Error("unknown OS")
	}
	if u := (Pin{OS: "windows", Arch: "x64", Version: "2.338.2"}).DownloadURL(addresses.Set{}); !strings.HasSuffix(u, "actions-runner-win-x64-2.338.2.zip") {
		t.Error(u)
	}
	if u := (Pin{OS: "macos", Arch: "arm64", Version: "2.338.2"}).DownloadURL(addresses.Set{}); !strings.HasSuffix(u, "actions-runner-osx-arm64-2.338.2.tar.gz") {
		t.Error(u)
	}

	dir := t.TempDir()
	f := filepath.Join(dir, "r.zip")
	if err := os.WriteFile(f, []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("payload"))
	if err := VerifyFile(f, hex.EncodeToString(sum[:])); err != nil {
		t.Errorf("match: %v", err)
	}
	if err := VerifyFile(f, strings.Repeat("ab", 32)); !errors.Is(err, ErrHashMismatch) {
		t.Errorf("mismatch: %v", err)
	}
	if _, err := os.Stat(f); err == nil {
		t.Error("a file that does not match is deleted")
	}
	_ = os.WriteFile(f, []byte("payload"), 0o600)
	if err := VerifyFile(f, strings.Repeat("a", 64)); !errors.Is(err, ErrBadHash) {
		t.Errorf("repeated character: %v", err)
	}
	if _, err := os.Stat(f); err == nil {
		t.Error("deleted on a wrong hash")
	}
}

// R5.36 (port of test_dl316_windows_only_generic.py).
func TestGenericLabelsOnly(t *testing.T) {
	verified := map[string]bool{"docker": true}
	ok, err := ValidateLabels([]string{"linux", "self-hosted", "x64", "docker", "pool-a", "linux"}, "pool-a", verified)
	if err != nil || strings.Join(ok, ",") != "docker,linux,pool-a,self-hosted,x64" {
		t.Fatalf("%v %v", ok, err)
	}
	for _, bad := range []string{"my-project", "build-box", "ubuntu-latest", "", "Linux", "kvm", "gpu"} {
		if _, err := ValidateLabels([]string{"self-hosted", bad}, "pool-a", verified); err == nil {
			t.Errorf("%q must be refused", bad)
		} else {
			var le *LabelError
			if !errors.As(err, &le) || le.Reason == "" {
				t.Errorf("%q: no reason: %v", bad, err)
			}
		}
	}
	if _, err := ValidateLabels([]string{"kvm"}, "", map[string]bool{"kvm": true}); err != nil {
		t.Errorf("a checked capability is fine: %v", err)
	}
	if got := DefaultLabels("linux", "x64"); strings.Join(got, ",") != "self-hosted,linux,x64" {
		t.Error(got)
	}
}

// R5.3.
func TestStateTransitions(t *testing.T) {
	ok := [][2]State{{Creating, Idle}, {Creating, Failed}, {Idle, Busy}, {Busy, Idle}, {Busy, Removing},
		{Idle, Draining}, {Busy, Draining}, {Draining, Updating}, {Draining, Removing}, {Updating, Idle},
		{Idle, Offline}, {Offline, Idle}, {Failed, Removing}}
	for _, p := range ok {
		if !CanTransition(p[0], p[1]) {
			t.Errorf("%s -> %s must be allowed", p[0], p[1])
		}
	}
	for _, p := range [][2]State{{Removing, Idle}, {Creating, Busy}, {Failed, Idle}, {Offline, Busy}, {Idle, Updating}} {
		if CanTransition(p[0], p[1]) {
			t.Errorf("%s -> %s must be refused", p[0], p[1])
		}
		if _, err := Next(p[0], p[1]); err == nil {
			t.Errorf("Next %s -> %s", p[0], p[1])
		}
	}
	if s, err := Next(Idle, Busy); err != nil || s != Busy {
		t.Error("Next")
	}
}

// R6.4: the idle check runs right before the act; timeout asks, never kills.
func TestIdleCheckRightBeforeAct(t *testing.T) {
	clock := time.Unix(0, 0)
	var events []string
	busyFor := 3
	d := Drain{
		Poll: time.Minute,
		Now:  func() time.Time { return clock },
		Sleep: func(_ context.Context, dur time.Duration) error {
			clock = clock.Add(dur)
			events = append(events, "sleep")
			return nil
		},
		Idle: func(context.Context) (bool, string, error) {
			events = append(events, "check")
			busyFor--
			return busyFor < 0, "demo/ci#12", nil
		},
	}
	err := d.Act(context.Background(), func(context.Context) error { events = append(events, "act"); return nil })
	if err != nil {
		t.Fatal(err)
	}
	if n := len(events); events[n-1] != "act" || events[n-2] != "check" {
		t.Errorf("the check must come right before the act: %v", events)
	}
}

func TestTimeoutAsksNeverKills(t *testing.T) {
	clock := time.Unix(0, 0)
	var warned []string
	acted := false
	d := Drain{
		Poll: time.Minute, Timeout: 45 * time.Minute, WarnAfter: 30 * time.Minute,
		Now:    func() time.Time { return clock },
		Sleep:  func(_ context.Context, dur time.Duration) error { clock = clock.Add(dur); return nil },
		Idle:   func(context.Context) (bool, string, error) { return false, "demo/ci#12", nil },
		OnWarn: func(job string) { warned = append(warned, job) },
	}
	err := d.Act(context.Background(), func(context.Context) error { acted = true; return nil })
	if !IsDrainTimeout(err) || !strings.Contains(err.Error(), "demo/ci#12") {
		t.Fatalf("err = %v", err)
	}
	if acted {
		t.Error("the act must not run on a timeout")
	}
	if len(warned) != 1 || warned[0] != "demo/ci#12" {
		t.Errorf("warn once, naming the job: %v", warned)
	}
}

// Section 8: the runner list cannot be read, so no change is made.
func TestDrainFailsClosed(t *testing.T) {
	acted := false
	d := Drain{Idle: func(context.Context) (bool, string, error) { return false, "", errors.New("boom") }}
	if err := d.Act(context.Background(), func(context.Context) error { acted = true; return nil }); err == nil || acted {
		t.Errorf("err=%v acted=%v", err, acted)
	}
}

// A runner already named crsp-… gets the prefix once (OPS18), and the name
// never goes past the 20 characters Windows allows.
func TestAccountNameSinglePrefix(t *testing.T) {
	for _, in := range []string{"crsp-light-comics-organizer-windows", "crsp-box-1", "crsp-crsp-x", "CRSP_Light"} {
		got := AccountName(in, nil)
		if strings.HasPrefix(got, AccountPrefix+AccountPrefix) {
			t.Errorf("%q -> %q has a doubled prefix", in, got)
		}
		if len(got) > MaxAccountLen {
			t.Errorf("%q -> %q is %d long", in, got, len(got))
		}
		if !strings.HasPrefix(got, AccountPrefix) {
			t.Errorf("%q -> %q lacks the prefix", in, got)
		}
	}
	if got := AccountName("crsp-box-1", nil); got != "crsp-box-1" {
		t.Errorf("got %s", got)
	}
	if got := AccountName("crsp-", nil); got != "crsp-runner" {
		t.Errorf("got %s", got)
	}
}
