package container

import (
	"slices"
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/images"
	"github.com/luis-mc/ci-runners-shared-pool/internal/backend"
)

// R5.93: one long-lived container, restarted unless stopped, caps from the
// pool size, and the hardening of every container run.
func TestPersistentRunArgsAreLongLivedAndSafe(t *testing.T) {
	a, err := PersistentRunArgs(PersistentSpec{ImageTag: "crsp-linux-base:abc", ImageKey: "abc", Limits: Limits{MemoryMB: 8192, CPUs: 4}, UID: 20007})
	if err != nil {
		t.Fatal(err)
	}
	line := strings.Join(a, " ")
	for _, want := range []string{"--restart unless-stopped", "--init", "--user 20007:20007", "--cap-drop ALL", "--read-only",
		"--memory 8192m", "--cpus 4", "-v " + PersistentPool + "-state:/state", "-v " + PersistentPool + "-deps:/deps"} {
		if !strings.Contains(line, want) {
			t.Errorf("run args lack %q: %s", want, line)
		}
	}
	for _, never := range []string{"--privileged", "docker.sock", "--ephemeral", "--cap-add"} {
		if strings.Contains(line, never) {
			t.Errorf("run args hold %q", never)
		}
	}
	if err := CheckRunArgs(a); err != nil {
		t.Fatal(err)
	}
	if _, err := PersistentRunArgs(PersistentSpec{}); err == nil {
		t.Error("a container without an image was built")
	}
}

func TestOwnVolumesIsOneShotWithOnlyChown(t *testing.T) {
	a := ownVolumesArgs("img", 1001)
	if !slices.Contains(a, "--network") || !slices.Contains(a, "CHOWN") || slices.Contains(a, "--privileged") {
		t.Fatalf("%v", a)
	}
}

func TestParsePersistentStates(t *testing.T) {
	got := ParsePersistentStates("r1|2048|\nr2|0|the clean-up of the last job left /tmp/x in place\n\njunk\n")
	if len(got) != 2 || got[0].DependencyBytes != 2048 || got[0].UncleanReason != "" || got[1].UncleanReason == "" {
		t.Fatalf("%+v", got)
	}
}

// The compose file is the readable form of the run options.
func TestComposeFileStatesTheSameOptions(t *testing.T) {
	b, err := images.FS.ReadFile("persistent/compose.yaml")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{"restart: unless-stopped", "read_only: true", "cap_drop: [ALL]", "no-new-privileges", "mem_limit", "cpus:"} {
		if !strings.Contains(s, want) {
			t.Errorf("compose.yaml lacks %q", want)
		}
	}
	if strings.Contains(s, "privileged: true") || strings.Contains(s, "docker.sock") {
		t.Error("compose.yaml is not safe")
	}
}

// Every script of the container is embedded.
func TestPersistentScriptsAreEmbedded(t *testing.T) {
	for _, n := range persistentFiles {
		if b, err := images.FS.ReadFile("persistent/" + n); err != nil || len(b) == 0 {
			t.Errorf("%s: %v", n, err)
		}
	}
}

func newPersistentRig(t *testing.T, uid int) *rig {
	t.Helper()
	return newRig(t, func(c *Config) { c.IDs = fixedID(uid) })
}

func persistentSpec() backend.RunnerSpec {
	return backend.RunnerSpec{ID: "r_p1", Name: "p1", OS: "linux", Arch: "x64", Container: true,
		GitHubURL: "https://github.com/luis-mc/demo", Image: backend.ImageRef{ID: "crsp-pool:abc123"}}
}

// OPS62: a pool made with uid 64000 gets its files written as 64000, not as
// the image's runner user.
func TestPersistentFilesAreWrittenAsThePoolUID(t *testing.T) {
	r := newPersistentRig(t, 64000)
	if _, err := r.b.StartPersistent(ctx, persistentSpec(), backend.StartConfig{Registration: "reg-tok"}); err != nil {
		t.Fatalf("start persistent: %v", err)
	}
	c := r.f.containers[PersistentPool]
	if c == nil || c.user() != "64000:64000" {
		t.Fatalf("pool: %+v", c)
	}
	for _, n := range persistentFiles {
		if got := c.owners[StateDir+"/bin/"+n]; got != "64000:64000" {
			t.Errorf("%s written as %q", n, got)
		}
	}
}

// OPS62: no exec into the pool names the user: the engine runs it as the
// container's Config.User; a name would be the image's runner (1001).
func TestPersistentExecsNeverNameTheUser(t *testing.T) {
	r := newPersistentRig(t, 64000)
	if _, err := r.b.StartPersistent(ctx, persistentSpec(), backend.StartConfig{Registration: "reg-tok"}); err != nil {
		t.Fatal(err)
	}
	if err := r.b.stopPersistent(ctx, "p1"); err != nil {
		t.Fatal(err)
	}
	if err := r.b.cleanUpPersistent(ctx, "p1"); err != nil {
		t.Fatal(err)
	}
	if err := r.b.ApplyHygiene(ctx, "p1", true, true); err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, call := range r.f.calls {
		if !strings.HasPrefix(call, "docker exec") {
			continue
		}
		n++
		if strings.Contains(call, " -u ") {
			t.Errorf("exec names a user: %s", call)
		}
	}
	if n < 10 {
		t.Errorf("only %d execs seen", n)
	}
}

// OPS62: a pool that exists has its volumes given to its own uid again.
func TestExistingPersistentPoolVolumesAreRepaired(t *testing.T) {
	r := newPersistentRig(t, 64000)
	if _, err := r.b.StartPersistent(ctx, persistentSpec(), backend.StartConfig{Registration: "reg-tok"}); err != nil {
		t.Fatal(err)
	}
	r.f.chowned = nil
	if _, err := r.b.StartPersistent(ctx, persistentSpec(), backend.StartConfig{Registration: "reg-tok"}); err != nil {
		t.Fatalf("second start: %v", err)
	}
	if len(r.f.chowned) != 1 || r.f.chowned[0] != "64000:64000" {
		t.Errorf("volumes owned as %v, want [64000:64000]", r.f.chowned)
	}
}

// OPS62: a job pool made with a non-1001 uid is entered as that uid too.
func TestJobPoolExecsNeverNameTheUser(t *testing.T) {
	r := newPersistentRig(t, 64010)
	img := r.image()
	if _, err := r.b.Start(ctx, r.spec("j1", "demo", img), backend.StartConfig{JITConfig: "jit"}); err != nil {
		t.Logf("start: %v", err)
	}
	for _, call := range r.f.calls {
		if strings.HasPrefix(call, "docker exec") && strings.Contains(call, " -u ") {
			t.Errorf("exec names a user: %s", call)
		}
	}
}
