// Package guardrails is the status of each project against the dev-standards test guardrails G1 to G7
// (docs/features/ci-run-insights R5.31, R5.32): read from the guard's report artifact when a run uploads
// one, filled by crsp's own checks otherwise. CIX8 contract: types and interfaces only.
package guardrails

import (
	"context"
	"io"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/insights"
)

// State is one guardrail's state.
type State string

// States. Unknown means neither the guard report nor a crsp check covers it (G4, G5 without a report).
const (
	Pass    State = "pass"
	Broken  State = "break"
	Off     State = "off"
	Unknown State = "unknown"
)

// Source says where a state came from.
type Source string

// Sources.
const (
	FromGuardReport Source = "guard_report"
	FromCrsp        Source = "crsp"
)

// Break is one place a guardrail breaks.
type Break struct {
	File    string
	Line    int
	Message string
}

// Entry is one guardrail of one report.
type Entry struct {
	Guardrail insights.Guardrail
	State     State
	Count     int
	Reason    string // for Off: the reason the repository gave
	Breaks    []Break
}

// Report is the parsed guard report: the JSON of `test_guardrails.py --json` (mode, results.G1 to G7 with
// state pass, break or off, reason, count, breaks of file, line and message), plus the known-list count when
// the report holds it (-1 otherwise).
type Report struct {
	Mode       string
	Entries    []Entry
	KnownCount int
}

// Parser reads a guard report. Detect says whether the content has the report's shape (R5.31: found by
// content, the artifact name pattern only narrows the search).
type Parser interface {
	Detect(head []byte) bool
	Parse(r io.Reader) (Report, error)
}

// Status is one repository's latest state per guardrail, with its source and date (R5.32).
type Status struct {
	RepositoryID    int64
	Repository      string
	Guardrails      []StatusEntry
	KnownCountByDay []DayCount
}

// StatusEntry is the latest state of one guardrail.
type StatusEntry struct {
	Entry
	Source     Source
	ReportedAt time.Time
}

// DayCount is the known-list count of one day.
type DayCount struct {
	Day   string
	Count int
}

// Store keeps guardrail_reports rows and answers listGuardrailStatus.
type Store interface {
	// SaveReport stores one guard report of a run.
	SaveReport(ctx context.Context, repositoryID, runID int64, headSHA string, r Report, at time.Time) error
	// SaveOwn stores crsp's own state of one guardrail (run id 0).
	SaveOwn(ctx context.Context, repositoryID int64, e Entry, at time.Time) error
	// Status returns the status of one repository or of all (0).
	Status(ctx context.Context, repositoryID int64) ([]Status, error)
}

// The guardrails analyser implements insights.Analyser with KindGuardrailG1 to G7: it writes crsp's own
// states (G1 from test times over the limit, G2, G3 and G6 from workflow checks and test retries, G7 from
// whether any test report was found in 7 days; G4 and G5 Unknown) and one finding per repository and
// breaking guardrail.
var Kinds = []insights.Kind{
	insights.KindGuardrailG1, insights.KindGuardrailG2, insights.KindGuardrailG3, insights.KindGuardrailG4,
	insights.KindGuardrailG5, insights.KindGuardrailG6, insights.KindGuardrailG7,
}
