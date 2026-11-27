package container

import (
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runnercache"
)

func TestLaneMountArgsMountFiveFoldersReadWriteAndExportThreeVariables(t *testing.T) {
	d := runnercache.LaneFor("/var/crsp/cache", "12", 1)
	args, err := LaneMountArgs(d)
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Join(args, " ")
	for _, want := range []string{
		"target=/ci-tools", "target=/rust-target", "target=/opt/hostedtoolcache", "target=/wine", "target=/ms-playwright",
		"-e CI_TOOLS_DIR=/ci-tools", "-e CARGO_TARGET_DIR=/rust-target", "-e RUNNER_TOOL_CACHE=/opt/hostedtoolcache",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in %s", want, text)
		}
	}
	if strings.Contains(text, "readonly") {
		t.Error("a lane is mounted read-write")
	}
	if n := strings.Count(text, "--mount"); n != 5 {
		t.Errorf("%d mounts, want 5", n)
	}
	if n := strings.Count(text, " -e "); n != 2 && n != 3 { // the first -e has no leading space only at the start
		t.Errorf("variables: %s", text)
	}
}

func TestLaneMountArgsRefuseSocketsAndBadPaths(t *testing.T) {
	d := runnercache.LaneFor("/var/run/docker", "1", 1)
	if _, err := LaneMountArgs(d); err != ErrSocketMount {
		t.Errorf("socket: %v", err)
	}
	d = runnercache.LaneFor("/c,evil", "1", 1)
	if _, err := LaneMountArgs(d); err == nil {
		t.Error("a comma in the path was accepted")
	}
	if _, err := LaneMountArgs(runnercache.LaneDirs{}); err == nil {
		t.Error("an empty lane was accepted")
	}
}
