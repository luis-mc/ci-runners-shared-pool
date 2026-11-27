package agent

import (
	"context"
	"errors"
	"time"

	agentv1 "github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/gen/agent/v1"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runnerswap"
)

// RunnerFiles lets the agent replace the files of a persistent runner when the
// controller sends UpdateRunnerFiles (protocol version 2, runners R5.38). A nil
// RunnerFiles makes the agent say it cannot, and it does not set `runner_swap`
// in its machine report, so the controller never sends the message.
type RunnerFiles struct {
	// Folder finds the runner folder from the runner and service names.
	Folder func(ctx context.Context, name, service string) (string, error)
	// Swapper replaces the files.
	Swapper interface {
		Swap(ctx context.Context, req runnerswap.Request) (runnerswap.Result, error)
	}
}

// Outcomes of an UpdateRunnerFiles command, in RunnerResult.outcome.
const (
	OutcomeUpdated = "updated"
	OutcomeCurrent = "current"
	OutcomeWaiting = "waiting"
)

func (l *link) answerSwap(id, runnerID string, ok bool, outcome, version string, rolledBack bool, err error) {
	res := &agentv1.RunnerResult{CommandId: id, Ok: ok, Outcome: outcome, RunnerId: runnerID, Version: version, RolledBack: rolledBack}
	if err != nil {
		res.Error = l.redactor().Redact(err.Error())
	}
	_ = l.send(&agentv1.SessionRequest{Message: &agentv1.SessionRequest_RunnerResult{RunnerResult: res}})
}

// updateFiles holds the runner out of new jobs, waits until no job runs on it
// (the idle check and the hold are one step, Gate.HoldRunner), swaps the files,
// and lets the runner take jobs again. A job that runs is never cut: when the
// wait ends first the answer is `waiting` and nothing was touched.
func (l *link) updateFiles(ctx context.Context, u *agentv1.UpdateRunnerFiles) {
	id, rid, name := u.GetCommandId(), u.GetRunnerId(), u.GetRunnerName()
	rf := l.cfg.RunnerFiles
	if rf == nil || rf.Swapper == nil || rf.Folder == nil {
		l.answerSwap(id, rid, false, "failed", "", false, errors.New("this agent cannot replace runner files"))
		return
	}
	if err := runnerswap.Verify(u.GetBundle(), u.GetSha256()); err != nil {
		l.answerSwap(id, rid, false, "failed", "", false, err)
		return
	}
	folder, err := rf.Folder(ctx, name, u.GetServiceName())
	if err != nil {
		l.answerSwap(id, rid, false, "failed", "", false, err)
		return
	}
	var deadline <-chan time.Time
	if s := u.GetDrainTimeoutSeconds(); s > 0 {
		t := time.NewTimer(time.Duration(s) * time.Second)
		defer t.Stop()
		deadline = t.C
	}
	defer l.cfg.Gate.ReleaseRunner(name)
	poll := time.NewTicker(l.cfg.DrainPollEvery)
	defer poll.Stop()
	for {
		idle, waiting := l.cfg.Gate.HoldRunner(name)
		if idle {
			break
		}
		select {
		case <-ctx.Done():
			l.answerSwap(id, rid, false, "failed", "", false, ctx.Err())
			return
		case <-deadline:
			job := name
			if len(waiting) > 0 {
				job = waiting[0].Job
			}
			// The hold ends before the answer goes out: whoever reads the answer finds the runner free.
			l.cfg.Gate.ReleaseRunner(name)
			// The error text is the job's name; the controller words the rest.
			l.answerSwap(id, rid, true, OutcomeWaiting, "", false, errors.New(job))
			return
		case <-poll.C:
		}
	}
	res, err := rf.Swapper.Swap(ctx, runnerswap.Request{
		Folder: folder, Service: u.GetServiceName(), Version: u.GetVersion(), SHA256: u.GetSha256(), Bundle: u.GetBundle(),
	})
	switch {
	case err != nil:
		l.answerSwap(id, rid, false, "failed", res.From, res.RolledBack, err)
	case res.Current:
		l.answerSwap(id, rid, true, OutcomeCurrent, u.GetVersion(), false, nil)
	default:
		l.answerSwap(id, rid, true, OutcomeUpdated, u.GetVersion(), false, nil)
	}
}
