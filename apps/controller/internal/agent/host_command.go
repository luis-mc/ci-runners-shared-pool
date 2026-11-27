package agent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/dockercompact"
	agentv1 "github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/gen/agent/v1"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/machines"
	enginemirror "github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry/mirror/engineconf"
)

// Host maintenance (cli R5.29, HND24). The controller sends RunHostCommand to a
// machine it holds in drain and has seen idle. The agent checks that again: it
// runs the command only while its gate holds new jobs and no job runs, so a
// maintenance command never meets a running job.

// MaxHostOutput is how many of the last bytes of a command's output go back.
const MaxHostOutput = 64 << 10

// DefaultHostTimeout is how long a host command may run when the message names
// no limit.
const DefaultHostTimeout = 30 * time.Minute

// tailBuffer keeps the last max bytes written to it.
type tailBuffer struct {
	max int
	buf []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.buf = append(t.buf, p...)
	if over := len(t.buf) - t.max; over > 0 {
		t.buf = append(t.buf[:0], t.buf[over:]...)
	}
	return len(p), nil
}

func (l *link) answerHost(id string, ok bool, outcome string, code int, out []byte, err error) {
	res := &agentv1.RunnerResult{CommandId: id, Ok: ok, Outcome: outcome, ExitCode: int32(code), Output: l.redactor().Redact(string(bytes.ToValidUTF8(out, []byte("?"))))} //nolint:gosec // exit codes are small
	if err != nil {
		res.Error = l.redactor().Redact(err.Error())
	}
	_ = l.send(&agentv1.SessionRequest{Message: &agentv1.SessionRequest_RunnerResult{RunnerResult: res}})
}

// runHostCommand runs argv directly (no shell) and answers once.
func (l *link) runHostCommand(ctx context.Context, m *agentv1.RunHostCommand) {
	id, argv := m.GetCommandId(), m.GetArgv()
	if len(argv) == 0 || argv[0] == "" {
		l.answerHost(id, false, "failed", -1, nil, errors.New("no command to run"))
		return
	}
	if l.cfg.Gate.State() == machines.StateActive {
		l.answerHost(id, false, "failed", -1, nil, errors.New("the machine takes new jobs; drain it first"))
		return
	}
	if idle, waiting := l.cfg.Gate.Hold(); !idle {
		l.answerHost(id, false, "failed", -1, nil, fmt.Errorf("%d jobs still run on this machine", len(waiting)))
		return
	}
	limit := time.Duration(m.GetTimeoutSeconds()) * time.Second
	if limit <= 0 {
		limit = DefaultHostTimeout
	}
	runCtx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	if argv[0] == dockercompact.Command {
		l.runDockerCompact(runCtx, id)
		return
	}
	if argv[0] == enginemirror.Command {
		l.runEngineMirror(id, argv[1:], l.cfg.engineMirrorPaths())
		return
	}
	out := &tailBuffer{max: MaxHostOutput}
	cmd := exec.CommandContext(runCtx, argv[0], argv[1:]...) //nolint:gosec // the owner names the command; no shell is used
	cmd.Stdout, cmd.Stderr = out, out
	err := cmd.Run()
	switch {
	case err == nil:
		l.answerHost(id, true, "done", 0, out.buf, nil)
	case errors.Is(runCtx.Err(), context.DeadlineExceeded):
		l.answerHost(id, false, "failed", -1, out.buf, fmt.Errorf("the command ran longer than %s and was stopped", limit))
	default:
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			l.answerHost(id, false, "failed", ee.ExitCode(), out.buf, fmt.Errorf("the command exited with code %d", ee.ExitCode()))
			return
		}
		l.answerHost(id, false, "failed", -1, out.buf, err)
	}
}
