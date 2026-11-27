package agentlink

import (
	"context"
	"log/slog"
	"strconv"
	"time"

	agentv1 "github.com/luis-mc/ci-runners-shared-pool/internal/gen/agent/v1"
	"github.com/luis-mc/ci-runners-shared-pool/internal/logs"
)

// LogBatchToProto builds the message for a batch of agent lines (observability
// R5.20, R5.23). A numeric job is the GitHub job id; any other is the job key.
func LogBatchToProto(lines []logs.Line, dropped int64) *agentv1.LogBatch {
	out := &agentv1.LogBatch{Dropped: dropped, Lines: make([]*agentv1.LogLine, 0, len(lines))}
	for _, l := range lines {
		pl := &agentv1.LogLine{
			AtUnixMs: l.At.UnixMilli(), Level: protoLevel(l.Level), Component: l.Component,
			Message: l.Message, RunnerId: l.Runner, AttrsJson: l.Attrs,
		}
		if l.Job != "" {
			pl.Job = &agentv1.JobRef{Job: l.Job}
			if id, err := strconv.ParseInt(l.Job, 10, 64); err == nil {
				pl.Job = &agentv1.JobRef{GithubJobId: id}
			}
		}
		out.Lines = append(out.Lines, pl)
	}
	return out
}

// LogSink takes the LogBatch messages of agents on the controller and writes
// them to the log store through the fail-open writer (R6.9). It never answers
// and never fails: a lost line is counted in the writer's stats.
type LogSink struct {
	Out *logs.FailOpen
}

// LogBatch stores the lines of one batch under the machine that sent it.
func (s LogSink) LogBatch(_ context.Context, machineID string, m *agentv1.LogBatch) {
	if s.Out == nil {
		return
	}
	for _, pl := range m.GetLines() {
		l := logs.Line{
			At: time.UnixMilli(pl.GetAtUnixMs()).UTC(), Level: slogLevel(pl.GetLevel()), Machine: machineID,
			Runner: pl.GetRunnerId(), Component: pl.GetComponent(), Message: pl.GetMessage(), Attrs: pl.GetAttrsJson(),
		}
		if j := pl.GetJob(); j.GetGithubJobId() > 0 {
			l.Job = strconv.FormatInt(j.GetGithubJobId(), 10)
		} else if j != nil {
			l.Job = j.GetJob()
		}
		s.Out.Add(l)
	}
}

func protoLevel(l slog.Level) agentv1.LogLevel {
	switch {
	case l >= slog.LevelError:
		return agentv1.LogLevel_LOG_LEVEL_ERROR
	case l >= slog.LevelWarn:
		return agentv1.LogLevel_LOG_LEVEL_WARN
	case l >= slog.LevelInfo:
		return agentv1.LogLevel_LOG_LEVEL_INFO
	}
	return agentv1.LogLevel_LOG_LEVEL_DEBUG
}

func slogLevel(l agentv1.LogLevel) slog.Level {
	switch l {
	case agentv1.LogLevel_LOG_LEVEL_ERROR:
		return slog.LevelError
	case agentv1.LogLevel_LOG_LEVEL_WARN:
		return slog.LevelWarn
	case agentv1.LogLevel_LOG_LEVEL_DEBUG:
		return slog.LevelDebug
	}
	return slog.LevelInfo
}
