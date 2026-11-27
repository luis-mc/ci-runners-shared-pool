package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/internal/registry"
	"github.com/luis-mc/ci-runners-shared-pool/internal/toolstore"
)

type fakePlans struct {
	files []registry.StackFile
	yml   string
	err   error
}

func (f fakePlans) ToolPlanInput(context.Context, string, string) ([]registry.StackFile, []byte, error) {
	return f.files, []byte(f.yml), f.err
}

func toolFlow(t *testing.T, goos string, p fakePlans) (*JobToolFlow, *toolstore.FakeInstaller, *toolstore.FakeKiller, *toolstore.FakeIdentity) {
	t.Helper()
	root := t.TempDir()
	st := toolstore.NewStore(filepath.Join(root, "store"), goos, "amd64")
	t.Cleanup(func() { _ = os.Chmod(root, 0o755); unsealAll(st.Root) })
	in, k, id := &toolstore.FakeInstaller{}, &toolstore.FakeKiller{}, &toolstore.FakeIdentity{}
	return &JobToolFlow{Store: st, Installer: in, Plans: p, JobsRoot: filepath.Join(root, "jobs"), Killer: k, Identity: id,
		Cache: toolstore.Cache{Root: filepath.Join(root, "cache"), Clone: toolstore.CopyCloner{}}}, in, k, id
}

func unsealAll(dir string) {
	resetACL(dir)
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil {
			_ = os.Chmod(p, 0o755)
		}
		return nil
	})
}

func trusted() toolstore.Trust {
	return toolstore.Trust{Repository: "o/r", HeadRepository: "o/r", Ref: "main", DefaultBranch: "main", Green: true}
}

// R5.116: the job flow end to end with fakes: scan, check, install once, home, cache restore, save, wipe.
func TestToolFlowEndToEnd(t *testing.T) {
	if !ToolStoreServes("windows") || !ToolStoreServes("darwin") || ToolStoreServes("linux") {
		t.Fatal("ToolStoreServes")
	}
	f, in, k, id := toolFlow(t, "darwin", fakePlans{files: []registry.StackFile{{Path: "Cargo.toml"}, {Path: "rust-toolchain.toml", Content: []byte("channel = \"1.85\"")}}})
	ctx := context.Background()
	p, err := f.Prepare(ctx, ToolJob{Repository: "o/r", Commit: "c1", JobID: "job1", LockHash: "L1", BaseEnv: map[string]string{"PATH": "/usr/bin"}})
	if err != nil || in.Calls != 1 || p.Hit {
		t.Fatalf("prepare: %v calls %d hit %v", err, in.Calls, p.Hit)
	}
	if err := os.MkdirAll(p.CacheDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p.CacheDir, "crate"), []byte("c"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(p.Env(), "\n"), "CRSP_CACHE_DIR="+p.CacheDir) {
		t.Fatal("env lacks the cache dir")
	}
	if err := p.Finish(ctx, ToolOutcome{PID: 77, User: "crsp-job", Trust: trusted()}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p.Home.Root); err == nil {
		t.Fatal("home left")
	}
	if len(k.Pids) != 1 || len(id.Wiped) != 1 || !f.Cache.Has(p.Key) {
		t.Fatalf("killed %v wiped %v saved %v", k.Pids, id.Wiped, f.Cache.Has(p.Key))
	}
	// second job: nothing to install, cache hit, no second save
	p2, err := f.Prepare(ctx, ToolJob{Repository: "o/r", Commit: "c2", JobID: "job2", LockHash: "L1"})
	if err != nil || in.Calls != 1 || !p2.Hit {
		t.Fatalf("second prepare: %v calls %d hit %v", err, in.Calls, p2.Hit)
	}
	if b, err := os.ReadFile(filepath.Join(p2.CacheDir, "crate")); err != nil || string(b) != "c" {
		t.Fatalf("restored %q %v", b, err)
	}
	_ = p2.Finish(ctx, ToolOutcome{Trust: trusted()})
}

func TestToolFlowRedAndForkNeverSave(t *testing.T) {
	f, _, _, _ := toolFlow(t, "windows", fakePlans{files: []registry.StackFile{{Path: "go.mod", Content: []byte("go 1.24")}}})
	for name, tr := range map[string]toolstore.Trust{
		"red":  {Repository: "o/r", HeadRepository: "o/r", Ref: "main", DefaultBranch: "main"},
		"fork": {Repository: "o/r", HeadRepository: "f/r", Ref: "main", DefaultBranch: "main", Green: true},
	} {
		p, err := f.Prepare(context.Background(), ToolJob{Repository: "o/r", Commit: "c", JobID: "j-" + strings.ReplaceAll(name, " ", ""), LockHash: "L"})
		if err != nil {
			t.Fatal(err)
		}
		_ = os.MkdirAll(p.CacheDir, 0o755)
		if err := p.Finish(context.Background(), ToolOutcome{Trust: tr}); err != nil || f.Cache.Has(p.Key) {
			t.Fatalf("%s: err %v saved %v", name, err, f.Cache.Has(p.Key))
		}
	}
}

func TestToolFlowFailures(t *testing.T) {
	f, in, _, _ := toolFlow(t, "darwin", fakePlans{files: []registry.StackFile{{Path: "go.mod", Content: []byte("go 1.24")}}})
	in.Fail = map[string]error{"go-1.24.0": errors.New("offline")}
	if _, err := f.Prepare(context.Background(), ToolJob{Repository: "o/r", Commit: "c", JobID: "j"}); err == nil || !strings.Contains(err.Error(), "offline") {
		t.Fatalf("install failure: %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.JobsRoot, "j")); err == nil {
		t.Fatal("a home was built for a job without tools")
	}
	bad, _, _, _ := toolFlow(t, "darwin", fakePlans{yml: "tools:\n  - cobol 1\n"})
	if _, err := bad.Prepare(context.Background(), ToolJob{JobID: "j"}); err == nil {
		t.Fatal("bad tools: entry must fail")
	}
	scanFail, _, _, _ := toolFlow(t, "darwin", fakePlans{err: errors.New("no access")})
	if _, err := scanFail.Prepare(context.Background(), ToolJob{JobID: "j"}); err == nil {
		t.Fatal("scan error must fail")
	}
	lin, _, _, _ := toolFlow(t, "linux", fakePlans{})
	if _, err := lin.Prepare(context.Background(), ToolJob{JobID: "j"}); err == nil {
		t.Fatal("linux has no store")
	}
}

// R5.119: the probe says what the runner lacks and installs nothing; housekeeping keeps versions in use.
func TestToolFlowProbeAndHousekeep(t *testing.T) {
	f, in, _, _ := toolFlow(t, "darwin", fakePlans{yml: "tools:\n  - rust 1.85.0\n"})
	rep := f.ProbeToolPlan(context.Background(), "o/r", "c")
	if rep.Err != "" || len(rep.Items) != 1 || rep.Items[0].Present || in.Calls != 0 {
		t.Fatalf("probe = %+v calls %d", rep, in.Calls)
	}
	if got := toolstore.Lacks(rep.Items); len(got) != 1 || got[0].Tool != "rust" {
		t.Fatalf("lacks = %v", got)
	}
	if r := (&JobToolFlow{Store: f.Store, Plans: fakePlans{err: errors.New("x")}}).ProbeToolPlan(context.Background(), "o/r", "c"); r.Err == "" {
		t.Fatal("scan error not reported")
	}
	if rm, err := f.Housekeep(nil); err != nil || len(rm) != 0 {
		t.Fatalf("housekeep: %v %v", rm, err)
	}
}

// R5.112 (owner decision 22 Nov): a job that needs an Xcode the Mac lacks fails before it starts, with the
// words for the owner; nothing is installed and no home is built. The doctor probe says which one to install.
func TestToolFlowMissingXcodeFailsWithReason(t *testing.T) {
	f, in, _, _ := toolFlow(t, "darwin", fakePlans{yml: "tools:\n  - xcode 16.2\n"})
	f.Store.Xcodes = toolstore.XcodeFinder{
		Glob:    func(string) ([]string, error) { return nil, nil },
		Version: func(context.Context, string) (string, error) { return "", errors.New("none") },
	}
	_, err := f.Prepare(context.Background(), ToolJob{Repository: "o/r", Commit: "c1", JobID: "job1", BaseEnv: map[string]string{"PATH": "/usr/bin"}})
	if !strings.Contains(fmt.Sprint(err), "Install Xcode 16.2 in /Applications") || in.Calls != 0 {
		t.Fatalf("error = %v calls %d", err, in.Calls)
	}
	if _, serr := os.Stat(filepath.Join(f.JobsRoot, "job1")); serr == nil {
		t.Fatal("a home was built")
	}
	rep := f.ProbeToolPlan(context.Background(), "o/r", "c1")
	var hint string
	for _, it := range rep.Items {
		if it.Tool == "xcode" {
			hint = it.Hint
		}
	}
	if !strings.Contains(hint, "Install Xcode 16.2") {
		t.Fatalf("probe = %+v", rep)
	}
}
