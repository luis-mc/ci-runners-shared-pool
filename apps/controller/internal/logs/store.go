package logs

import (
	"context"
	"log/slog"
	"time"
)

// LogsFile is the file name of the log database inside the data folder (R6.10).
// It is a different file from the main database, so heavy log writes never
// block a scheduler write.
const LogsFile = "logs.db"

// Line is one stored log line: a runner setup step or an agent log line
// (R5.20). Level is an slog level; Machine, Runner and Job are the ids the line
// has, empty when it has none.
type Line struct {
	ID        int64
	At        time.Time
	Level     slog.Level
	Machine   string
	Runner    string
	Job       string
	Component string
	Message   string
	// Attrs are the extra attributes as JSON, already redacted.
	Attrs string
}

// Size is the number of bytes the line counts against the per-machine cap.
func (l Line) Size() int64 {
	return int64(len(l.Message) + len(l.Attrs) + len(l.Component) + len(l.Machine) + len(l.Runner) + len(l.Job) + 32)
}

// Store is the port of the log database. The SQLite adapter is
// internal/store/logsdb: only internal/store may import the driver (STACKSPEC
// 3.2), and internal/logs stays free of network packages (R6.7).
type Store interface {
	// Insert writes the lines in one transaction.
	Insert(ctx context.Context, lines []Line) error
	// Search returns one page, newest first.
	Search(ctx context.Context, q Query) (Page, error)
	// Prune deletes whole days older than cutoff, then, per machine, the oldest
	// days until the machine holds at most maxBytesPerMachine (R6.8). It returns
	// the number of lines deleted.
	Prune(ctx context.Context, cutoff time.Time, maxBytesPerMachine int64) (int64, error)
}
