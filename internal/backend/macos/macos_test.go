package macos

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/backend"
	"github.com/luis-mc/ci-runners-shared-pool/internal/backend/persistent"
	"github.com/luis-mc/ci-runners-shared-pool/internal/backend/sys"
	"github.com/luis-mc/ci-runners-shared-pool/internal/backend/sys/sysfake"
	"github.com/luis-mc/ci-runners-shared-pool/internal/runners"
)

type rig struct {
	b    *Backend
	cmd  *sysfake.Commands
	fs   *sysfake.FS
	body []byte
}

const label = "actions.runner.luis-mc-demo.box-1"

func newRig() *rig {
	r := &rig{cmd: &sysfake.Commands{}, fs: sysfake.NewFS(), body: []byte("tarball")}
	fe := &sysfake.Fetcher{FS: r.fs, Body: r.body}
	verify := func(path, want string) error {
		d, _ := r.fs.ReadFile(path)
		sum := sha256.Sum256(d)
		if hex.EncodeToString(sum[:]) != want {
			return runners.ErrHashMismatch
		}
		return nil
	}
	r.cmd.Rules = []sysfake.Rule{
		{Match: "-read /Groups/crsp-runners PrimaryGroupID", Result: sys.Result{Stdout: "PrimaryGroupID: 399\n"}},
		{Match: "-read /Groups/crsp-runners", Result: sys.Result{ExitCode: 56}},
		{Match: "-read /Users/", Result: sys.Result{ExitCode: 56}},
		{Match: "-list /Users UniqueID", Result: sys.Result{Stdout: "root 0\ndaemon 1\n_www 70\ncrsp-old 300\ncrsp-other 301\nluis 501\n"}},
	}
	r.b = New(sys.Deps{Cmd: r.cmd, FS: r.fs, Fetch: fe, Verify: verify}, Config{})
	return r
}

func (r *rig) spec() backend.RunnerSpec {
	sum := sha256.Sum256(r.body)
	return backend.RunnerSpec{ID: "r1", Name: "box-1", OS: "macos", Arch: "arm64",
		Labels: []string{"self-hosted", "macos", "arm64"}, GitHubURL: "https://github.com/luis-mc/demo",
		RunnerVersion: "2.330.0", RunnerSHA256: hex.EncodeToString(sum[:])}
}

func (r *rig) index(prefix string) int {
	for i, t := range r.cmd.Texts() {
		if strings.HasPrefix(t, prefix) {
			return i
		}
	}
	return -1
}

func started(t *testing.T) *rig {
	t.Helper()
	r := newRig()
	if _, err := r.b.Start(context.Background(), r.spec(), backend.StartConfig{Registration: "REGTOKEN"}); err != nil {
		t.Fatal(err)
	}
	return r
}

// R5.23: a launchd daemon with its own hidden user; folder 0700.
func TestDaemonPlistUserAndFolder(t *testing.T) {
	r := started(t)
	plist := string(r.fs.Files["/Library/LaunchDaemons/"+label+".plist"])
	for _, want := range []string{
		"<key>UserName</key>\n  <string>crsp-box-1</string>",
		"<key>GroupName</key>\n  <string>crsp-runners</string>",
		"<key>WorkingDirectory</key>\n  <string>/usr/local/var/crsp/runners/box-1</string>",
		"<key>KeepAlive</key>\n  <dict>\n    <key>SuccessfulExit</key>\n    <false/>\n  </dict>",
		"<string>" + label + "</string>",
	} {
		if !strings.Contains(plist, want) {
			t.Errorf("plist lacks %q:\n%s", want, plist)
		}
	}
	if r.index("launchctl bootstrap system /Library/LaunchDaemons/"+label+".plist") < 0 {
		t.Errorf("not bootstrapped: %v", r.cmd.Texts())
	}
	if r.index("chmod 0700 /usr/local/var/crsp/runners/box-1") < 0 ||
		r.index("chown -R crsp-box-1:crsp-runners /usr/local/var/crsp/runners/box-1") < 0 {
		t.Errorf("folder owner or mode: %v", r.cmd.Texts())
	}
}

// R5.23: hidden, no login shell, no password, not an admin, a free id below 500.
func TestHiddenUser(t *testing.T) {
	r := started(t)
	texts := strings.Join(r.cmd.Texts(), "\n")
	for _, want := range []string{
		"dscl . -create /Users/crsp-box-1 UserShell /usr/bin/false",
		"dscl . -create /Users/crsp-box-1 IsHidden 1",
		"dscl . -create /Users/crsp-box-1 Password *",
		"dscl . -create /Users/crsp-box-1 UniqueID 302",
		"dscl . -create /Users/crsp-box-1 PrimaryGroupID 399",
		"dseditgroup -o create -q crsp-runners",
	} {
		if !strings.Contains(texts, want) {
			t.Errorf("missing call %q in:\n%s", want, texts)
		}
	}
	for _, bad := range []string{"admin", "sysadminctl", "-append /Groups", "sudoers"} {
		if strings.Contains(texts, bad) {
			t.Errorf("the user must not be an admin: %s", bad)
		}
	}
	for _, n := range []string{"job-started", "job-completed"} {
		if !strings.Contains(string(r.fs.Files["/usr/local/var/crsp/runners/box-1/hooks/"+n+".sh"]), "/usr/local/bin/crsp") {
			t.Errorf("hook %s", n)
		}
	}
}

func TestTokenOnlyInEnv(t *testing.T) {
	r := started(t)
	n := 0
	for _, c := range r.cmd.Calls {
		if strings.Contains(c.String(), "REGTOKEN") {
			t.Error("token in an argument")
		}
		if c.SecretEnv["ACTIONS_RUNNER_INPUT_TOKEN"] == "REGTOKEN" {
			n++
			if !strings.Contains(c.String(), "-u crsp-box-1") || !strings.Contains(c.String(), "--disableupdate") {
				t.Errorf("configure: %s", c.String())
			}
		}
	}
	if n != 1 {
		t.Errorf("token reached %d processes", n)
	}
}

func TestRerunAlreadyAndStaleEnv(t *testing.T) {
	r := started(t)
	r.cmd.Calls = nil
	if _, err := r.b.Start(context.Background(), r.spec(), backend.StartConfig{Registration: "REGTOKEN"}); err != nil {
		t.Fatal(err)
	}
	if len(r.cmd.Calls) != 0 {
		t.Errorf("already: %v", r.cmd.Texts())
	}
	r.fs.Files["/usr/local/var/crsp/runners/box-1/.env"] = []byte("old\n")
	if _, err := r.b.Start(context.Background(), r.spec(), backend.StartConfig{Registration: "REGTOKEN"}); err != nil {
		t.Fatal(err)
	}
	if r.index("launchctl bootout system/"+label) < 0 || r.index("launchctl bootstrap system") < r.index("pgrep") || r.index("dscl . -create") >= 0 {
		t.Errorf("stale env: %v", r.cmd.Texts())
	}
}

func TestReregisterRemovesFirstAndStopsOnFailure(t *testing.T) {
	r := started(t)
	folder := "/usr/local/var/crsp/runners/box-1"
	meta, _ := persistent.ReadMeta(r.fs, folder+"/"+persistent.MetaFile)
	meta.Account = "somebody"
	_ = persistent.WriteMeta(r.fs, folder+"/"+persistent.MetaFile, *meta)

	r.cmd.Calls = nil
	r.cmd.Rules = append([]sysfake.Rule{{Match: "-u somebody " + folder + "/config.sh remove", Result: sys.Result{ExitCode: 1}}}, r.cmd.Rules...)
	_, err := r.b.Start(context.Background(), r.spec(), backend.StartConfig{Registration: "REG2", Removal: "REMOVETOKEN"})
	if !errors.Is(err, ErrRemovalFailed) {
		t.Fatalf("err = %v", err)
	}
	if r.index("chmod") >= 0 || r.index("dscl . -create") >= 0 {
		t.Error("nothing changes after a failed removal")
	}
	if _, ok := r.fs.Files["/Library/LaunchDaemons/"+label+".plist"]; !ok {
		t.Error("the old daemon plist must stay")
	}
	if r.index("launchctl bootstrap system") < 0 {
		t.Error("old daemon loaded again")
	}

	r.cmd.Calls, r.cmd.Rules = nil, r.cmd.Rules[1:]
	if _, err := r.b.Start(context.Background(), r.spec(), backend.StartConfig{Registration: "REG2", Removal: "REMOVETOKEN"}); err != nil {
		t.Fatal(err)
	}
	rm := r.index("sudo --preserve-env=ACTIONS_RUNNER_INPUT_TOKEN -n -H -u somebody " + folder + "/config.sh remove")
	reg := r.index("sudo --preserve-env=ACTIONS_RUNNER_INPUT_TOKEN -n -H -u crsp-box-1 " + folder + "/config.sh --unattended")
	if rm < 0 || reg < rm {
		t.Errorf("remove at %d, register at %d: %v", rm, reg, r.cmd.Texts())
	}
}

func TestWrongHashRefused(t *testing.T) {
	r := newRig()
	spec := r.spec()
	spec.RunnerSHA256 = strings.Repeat("ef", 32)
	_, err := r.b.Start(context.Background(), spec, backend.StartConfig{Registration: "REG"})
	if !errors.Is(err, runners.ErrHashMismatch) {
		t.Fatalf("err = %v", err)
	}
	if r.index("tar") >= 0 || r.index("dscl") >= 0 || len(r.fs.Names()) != 0 {
		t.Errorf("after a bad hash: %v %v", r.cmd.Texts(), r.fs.Names())
	}
}

func findRules(extra ...sysfake.Rule) []sysfake.Rule {
	return append([]sysfake.Rule{
		{Match: "launchctl list", Result: sys.Result{Stdout: "PID\tStatus\tLabel\n123\t0\t" + label + "\n-\t0\tactions.runner.old-x.hand\n-\t0\tcom.apple.thing\n"}},
		{Match: "ls -1 /Library/LaunchDaemons", Result: sys.Result{Stdout: label + ".plist\ncom.apple.thing.plist\n"}},
		{Match: "plutil -convert json -o - /Library/LaunchDaemons/" + label + ".plist", Result: sys.Result{Stdout: `{"UserName":"crsp-box-1","WorkingDirectory":"/usr/local/var/crsp/runners/box-1"}`}},
		{Match: "plutil -convert json -o - /Library/LaunchDaemons/actions.runner.old-x.hand.plist", Result: sys.Result{ExitCode: 1}},
	}, extra...)
}

// R5.5: from launchd, not folders; an unreadable one is still reported.
func TestFindFromLaunchd(t *testing.T) {
	r := started(t)
	r.cmd.Rules = findRules()
	found, err := r.b.Find(context.Background())
	if err != nil || len(found) != 2 {
		t.Fatalf("%v %v", found, err)
	}
	if !found[0].Managed || !found[0].Running || found[0].Account != "crsp-box-1" {
		t.Errorf("%+v", found[0])
	}
	if found[1].Managed || !found[1].FolderUnreadable || found[1].Running {
		t.Errorf("%+v", found[1])
	}
}

func TestCleanUpTwice(t *testing.T) {
	r := started(t)
	r.cmd.Rules = findRules(sysfake.Rule{Match: "-read /Users/crsp-box-1", Result: sys.Result{}})
	h := backend.Handle{Ref: label}
	if err := r.b.CleanUp(context.Background(), h); err != nil {
		t.Fatal(err)
	}
	if r.index("dscl . -delete /Users/crsp-box-1") < 0 || r.index("launchctl bootout system/"+label) < 0 {
		t.Errorf("%v", r.cmd.Texts())
	}
	if _, ok := r.fs.Files["/Library/LaunchDaemons/"+label+".plist"]; ok {
		t.Error("plist left")
	}
	r.cmd.Rules = []sysfake.Rule{
		{Match: "launchctl list", Result: sys.Result{Stdout: "PID\tStatus\tLabel\n"}},
		{Match: "launchctl bootout", Result: sys.Result{ExitCode: 113}},
	}
	if err := r.b.CleanUp(context.Background(), h); err != nil {
		t.Fatalf("second CleanUp: %v", err)
	}
}

func TestDetect(t *testing.T) {
	r := newRig()
	r.cmd.Rules = []sysfake.Rule{{Match: "id -u", Result: sys.Result{Stdout: "501\n"}}}
	if s, _ := r.b.Detect(context.Background()); s.Usable || !s.Present || s.Reason == "" {
		t.Errorf("%+v", s)
	}
	r.cmd.Rules = []sysfake.Rule{{Match: "id -u", Result: sys.Result{Stdout: "0\n"}}}
	if s, _ := r.b.Detect(context.Background()); !s.Usable {
		t.Errorf("%+v", s)
	}
}

// runners R5.46: a listener that outlives its daemon blocks the load.
func TestRestartNeverStartsASecondListener(t *testing.T) {
	r := started(t)
	r.b.d.Sleep = func(context.Context, time.Duration) {}
	r.fs.Files["/usr/local/var/crsp/runners/box-1/.env"] = []byte("old\n")
	r.cmd.Rules = []sysfake.Rule{{Match: "pgrep", Result: sys.Result{Stdout: "88\n"}}}
	r.cmd.Calls = nil
	_, err := r.b.Start(context.Background(), r.spec(), backend.StartConfig{Registration: "REGTOKEN"})
	if !errors.Is(err, persistent.ErrListenerAlive) {
		t.Fatalf("err = %v", err)
	}
	if r.index("launchctl bootstrap") >= 0 {
		t.Errorf("a daemon was loaded beside a live listener: %v", r.cmd.Texts())
	}
}
