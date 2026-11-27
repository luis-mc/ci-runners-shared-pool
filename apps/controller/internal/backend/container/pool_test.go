package container

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend/sys"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend/sys/sysfake"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runnercache"
)

type fixedID int

func (f fixedID) ID(context.Context, string) (int, error) { return int(f), nil }

// R5.72: the pool container runs as its persistent uid/gid; none set keeps JobUID.
func TestRunArgsUsesPersistentUID(t *testing.T) {
	p := PoolSpec{Owner: "o", Repo: "r", ImageTag: "img", UID: 64010}
	a, err := RunArgs(p)
	if err != nil {
		t.Fatal(err)
	}
	if i := slices.Index(a, "--user"); i < 0 || a[i+1] != "64010:64010" {
		t.Fatalf("args = %v", a)
	}
	if !slices.ContainsFunc(a, func(s string) bool { return strings.Contains(s, "uid=64010,gid=64010") }) {
		t.Fatalf("tmpfs not owned by the uid: %v", a)
	}
	p.UID = 0
	a, err = RunArgs(p)
	if err != nil {
		t.Fatal(err)
	}
	if i := slices.Index(a, "--user"); a[i+1] != "1001:1001" {
		t.Fatalf("default = %v", a)
	}
}

// R5.72: the lane keeps its owner across a new container.
func TestOwnLaneChownsToUID(t *testing.T) {
	cmd := &sysfake.Commands{}
	b := New(sys.Deps{Cmd: cmd}, newFake(), Config{IDs: fixedID(64010), HostOS: "linux"})
	lane := runnercache.LaneFor("/cache", "repo1", 1)
	uid, err := b.identity(context.Background(), "pool")
	if err != nil || uid != 64010 {
		t.Fatalf("identity = %d, %v", uid, err)
	}
	if err := b.ownLane(context.Background(), uid, lane); err != nil {
		t.Fatal(err)
	}
	texts := cmd.Texts()
	if len(texts) == 0 || !strings.HasPrefix(texts[0], "chown -R 64010:64010 ") {
		t.Fatalf("calls = %v", texts)
	}
	// No id source: nothing is changed.
	cmd2 := &sysfake.Commands{}
	b2 := New(sys.Deps{Cmd: cmd2}, newFake(), Config{})
	if err := b2.ownLane(context.Background(), 0, lane); err != nil || len(cmd2.Texts()) != 0 {
		t.Fatalf("unexpected chown: %v %v", err, cmd2.Texts())
	}
}

// OPS89: a Windows host has no chown and NTFS has no uid; the lane is left to
// the engine and no command runs on the host.
func TestOwnLaneNeverRunsChownOnAWindowsHost(t *testing.T) {
	cmd := &sysfake.Commands{}
	b := New(sys.Deps{Cmd: cmd}, newFake(), Config{IDs: fixedID(64010), HostOS: "windows"})
	lane := runnercache.LaneFor(`C:\cache`, "repo1", 1)
	if err := b.ownLane(context.Background(), 64010, lane); err != nil {
		t.Fatal(err)
	}
	if got := cmd.Texts(); len(got) != 0 {
		t.Fatalf("host commands on Windows: %v", got)
	}
}
