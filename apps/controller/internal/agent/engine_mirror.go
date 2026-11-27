package agent

import (
	"runtime"
	"strings"

	enginemirror "github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry/mirror/engineconf"
)

// engineMirrorPaths are the engine configuration files the host step writes.
// Windows and macOS engines are Docker Desktop's: their mirror is a setting of
// the desktop app, so the step writes no file there.
func (c Config) engineMirrorPaths() enginemirror.Paths {
	if c.EngineMirrorPaths != nil {
		return *c.EngineMirrorPaths
	}
	if runtime.GOOS != "linux" {
		return enginemirror.Paths{SkipDocker: true, SkipContainerd: true, SkipPodman: true}
	}
	return enginemirror.DefaultPaths()
}

// runEngineMirror is the host step of registry R5.65: point the engines of this
// machine at the pool's pull-through mirror, or away from it. It is idempotent:
// when every file already holds the settings it changes nothing and says so. The
// caller has checked that the machine is held and idle, so a restart of an
// engine, which the owner's setup does after a change, never meets a job.
func (l *link) runEngineMirror(id string, args []string, p enginemirror.Paths) {
	s, err := enginemirror.ParseArgs(args)
	if err != nil {
		l.answerHost(id, false, "failed", 2, nil, err)
		return
	}
	changed, err := enginemirror.Apply(p, s)
	if err != nil {
		l.answerHost(id, false, "failed", 1, []byte(strings.Join(changed, "\n")), err)
		return
	}
	if len(changed) == 0 {
		l.answerHost(id, true, "done", 0, []byte("engine mirror settings already in place"), nil)
		return
	}
	l.answerHost(id, true, "done", 0, []byte("changed:\n"+strings.Join(changed, "\n")+"\nrestart the engine to pick the change up"), nil)
}
