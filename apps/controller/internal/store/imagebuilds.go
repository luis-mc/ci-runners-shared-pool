package store

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry/builds"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/store/sqlcgen"
)

// ImageBuilds is the build record store (registry R5.69, IMG1) over
// queries/image_builds.sql. It implements builds.Store.
func (s *Store) ImageBuilds() builds.Store { return imageBuilds{q: s.q} }

type imageBuilds struct{ q *sqlcgen.Queries }

var _ builds.Store = imageBuilds{}

const buildTimeLayout = "2006-01-02T15:04:05.000Z"

func buildStamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(buildTimeLayout)
}

func buildTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

func buildRecord(r sqlcgen.ImageBuild) builds.Record {
	return builds.Record{
		ID: r.ID, MachineID: r.MachineID, Profile: r.ProfileID, Arch: r.Arch, Key: r.ImageTag, Reason: r.Reason,
		State: r.State, Step: int(r.Step), StepsTotal: int(r.StepsTotal), StepName: r.StepName,
		TypicalSeconds: r.TypicalSeconds, SizeBytes: r.SizeBytes, FailureStep: r.FailureStep, FailureClass: r.FailureClass,
		FailureWhy: r.FailureWhy, LogTail: r.LogTail, PushState: r.PushState,
		QueuedAt: buildTime(r.QueuedAt), StartedAt: buildTime(r.StartedAt), FinishedAt: buildTime(r.FinishedAt),
	}
}

// Insert writes a queued build. QueuedAt is the database clock.
func (b imageBuilds) Insert(ctx context.Context, r builds.Record) error {
	return b.q.InsertImageBuild(ctx, sqlcgen.InsertImageBuildParams{
		ID: r.ID, MachineID: r.MachineID, ProfileID: r.Profile, Arch: r.Arch, ImageTag: r.Key, Reason: r.Reason, TypicalSeconds: r.TypicalSeconds,
	})
}

// Get reads one build.
func (b imageBuilds) Get(ctx context.Context, id string) (builds.Record, bool, error) {
	row, err := b.q.GetImageBuild(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return builds.Record{}, false, nil
	}
	if err != nil {
		return builds.Record{}, false, err
	}
	return buildRecord(row), true, nil
}

// SetProgress keeps the step and moves the build to the given state.
func (b imageBuilds) SetProgress(ctx context.Context, id, state string, step, total int, name string, startedAt time.Time) error {
	return b.q.SetImageBuildProgress(ctx, sqlcgen.SetImageBuildProgressParams{
		State: state, Step: int64(step), StepsTotal: int64(total), StepName: name, StartedAt: buildStamp(startedAt), ID: id,
	})
}

// Finish writes the outcome and the finish time.
func (b imageBuilds) Finish(ctx context.Context, id string, o builds.Outcome, at time.Time) error {
	return b.q.FinishImageBuild(ctx, sqlcgen.FinishImageBuildParams{
		State: o.State, SizeBytes: o.SizeBytes, FailureStep: o.FailureStep, FailureClass: o.FailureClass,
		FailureWhy: o.FailureWhy, LogTail: o.LogTail, FinishedAt: buildStamp(at), ID: id,
	})
}

// Typical is the mean seconds of the last 5 done builds of the profile and arch.
func (b imageBuilds) Typical(ctx context.Context, profile, arch string) (float64, error) {
	v, err := b.q.TypicalImageBuildSeconds(ctx, sqlcgen.TypicalImageBuildSecondsParams{ProfileID: profile, Arch: arch})
	if err != nil {
		return 0, err
	}
	switch x := v.(type) {
	case float64:
		return x, nil
	case int64:
		return float64(x), nil
	case []byte:
		return strconv.ParseFloat(string(x), 64)
	case string:
		return strconv.ParseFloat(x, 64)
	}
	return 0, nil
}

// List gives the newest builds first.
func (b imageBuilds) List(ctx context.Context, machineID string, limit int) ([]builds.Record, error) {
	var rows []sqlcgen.ImageBuild
	var err error
	if machineID != "" {
		rows, err = b.q.ListImageBuildsOfMachine(ctx, sqlcgen.ListImageBuildsOfMachineParams{MachineID: machineID, Limit: int64(limit)})
	} else {
		rows, err = b.q.ListImageBuilds(ctx, int64(limit))
	}
	if err != nil {
		return nil, err
	}
	out := make([]builds.Record, 0, len(rows))
	for _, r := range rows {
		out = append(out, buildRecord(r))
	}
	return out, nil
}
