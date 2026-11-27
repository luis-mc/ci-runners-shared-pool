package agent

import (
	"context"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/dockercompact"
	agentv1 "github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/gen/agent/v1"
)

// dockerHost is the OS access of the docker-compact helper.
func (c Config) dockerHost() dockercompact.Host {
	if c.DockerHost != nil {
		return c.DockerHost
	}
	return dockercompact.OSHost{}
}

// dockerDiskReport reads the Docker disk file for the machine report. It
// changes nothing. Linux and macOS have no disk-file step: present is false.
func dockerDiskReport(ctx context.Context, h dockercompact.Host) *agentv1.DockerDiskReport {
	in := dockercompact.Inspect(ctx, h)
	r := &agentv1.DockerDiskReport{Present: len(in.Files) > 0, UsedKnown: in.UsedKnown, UsedBytes: in.UsedBytes, CanCompact: in.Elevated}
	for _, f := range in.Files {
		r.Files = append(r.Files, f.Path)
		r.FileBytes += f.Before
	}
	return r
}

// runDockerCompact runs the docker-compact helper and answers once (caches
// R5.50). The caller has checked that the machine is held and idle. The helper
// starts Docker again after any failure; its output is the report.
func (l *link) runDockerCompact(ctx context.Context, id string) {
	rep, err := dockercompact.Do(ctx, l.cfg.dockerHost())
	out := []byte(rep.Text())
	if err != nil {
		l.answerHost(id, false, "failed", 1, out, err)
		return
	}
	l.answerHost(id, true, "done", 0, out, nil)
}
