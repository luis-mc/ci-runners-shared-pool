package ingest

import (
	"context"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/jobsdata/reconcile"
)

// OriginSink stores what ingest learns about who ran a job and where its steps came from. The SQLite
// implementation is store.InsightsJobsStore. The contract jobsdata.Writer has no such write, so it is a local
// interface (a gap for the orchestrator).
type OriginSink interface {
	SetRunnerOrigin(ctx context.Context, jobID int64, origin reconcile.Origin, group string, labels []string) error
	SetStepsSource(ctx context.Context, jobID int64, source string) error
}

// OnRunner records the runner of a job from a workflow_job webhook (R5.39): its name, runner group and labels.
// Call it after OnJob so the job row exists. The origin is crsp, other or hosted; a job with no runner yet
// (queued) keeps an empty origin, and a stored origin is never wiped.
func (r *Recorder) OnRunner(ctx context.Context, jobID int64, runnerName, runnerGroup string, labels []string) error {
	if jobID == 0 || r.cfg.Origins == nil || r.cfg.Classify == nil || !r.known(ctx, jobID) {
		return nil
	}
	origin, err := r.cfg.Classify.Origin(ctx, runnerName, runnerGroup)
	if err != nil {
		return err
	}
	if origin == reconcile.Unknown && runnerGroup == "" && len(labels) == 0 {
		return nil
	}
	return r.cfg.Origins.SetRunnerOrigin(ctx, jobID, origin, runnerGroup, labels)
}
