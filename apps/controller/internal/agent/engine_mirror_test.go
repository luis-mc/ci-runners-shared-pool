package agent_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/agent"
	enginemirror "github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry/mirror/engineconf"
)

// registry R5.65: the engine-mirror host step writes the engine files, and a
// second run changes nothing and says so.
func TestHostCommandEngineMirrorWritesIdempotently(t *testing.T) {
	d := t.TempDir()
	p := enginemirror.Paths{DockerDaemon: filepath.Join(d, "daemon.json"), ContainerdCerts: filepath.Join(d, "certs.d"),
		PodmanRegistries: filepath.Join(d, "registries.conf.d")}
	r := startHostWith(t, func(c *agent.Config) { c.EngineMirrorPaths = &p })
	r.drain(t)
	argv := []string{enginemirror.Command, "ctl.lan:8443", "on", "docker.io,ghcr.io"}
	out, err := r.svc.RunHostCommand(context.Background(), "owner", r.id, argv, time.Minute)
	if err != nil || !out.OK || !strings.Contains(out.Output, "changed:") {
		t.Fatalf("first run %+v err %v", out, err)
	}
	b, err := os.ReadFile(p.DockerDaemon)
	if err != nil || !strings.Contains(string(b), "https://ctl.lan:8443/mirror/docker.io") {
		t.Fatalf("daemon.json %q err %v", b, err)
	}
	out, err = r.svc.RunHostCommand(context.Background(), "owner", r.id, argv, time.Minute)
	if err != nil || !out.OK || !strings.Contains(out.Output, "already in place") {
		t.Fatalf("second run %+v err %v", out, err)
	}
	out, err = r.svc.RunHostCommand(context.Background(), "owner", r.id, []string{enginemirror.Command, "h", "maybe", ""}, time.Minute)
	if err != nil || out.OK {
		t.Fatalf("bad arguments %+v err %v", out, err)
	}
}
