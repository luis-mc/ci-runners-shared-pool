// Package tests reads test reports (docs/features/ci-run-insights R5.33 to R5.37): parsers for JUnit XML,
// Playwright JSON, vitest JSON, go test -json, cargo libtest JSON and dotnet TRX; test times, flaky,
// duplicated and assertion-free tests. No static scan of test source (owner decision D7). CIX8 contract:
// types and interfaces only.
package tests

import (
	"context"
	"io"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/insights"
)

// Format is a report format.
type Format string

// Formats. JUnit covers cargo nextest and pytest JUnit output too.
const (
	JUnit      Format = "junit"
	Playwright Format = "playwright"
	Vitest     Format = "vitest"
	GoTest     Format = "gotest"
	Libtest    Format = "libtest"
	TRX        Format = "trx"
	// CIRunTimes and CITimings are the dev-standards timing artifacts: `ci-run-times` (one case per CI job)
	// and `ci-timings.json` (one case per test file, `{"seconds": {"path": 12.3}}` or `{"path": 12.3}`).
	CIRunTimes Format = "ci-run-times"
	CITimings  Format = "ci-timings"
)

// Result is how one test case ended.
type Result string

// Results.
const (
	Passed  Result = "passed"
	Failed  Result = "failed"
	Skipped Result = "skipped"
	Error   Result = "error"
)

// TestKind decides the G1 limit: unit 10 s, integration 60 s, e2e 2 min (R5.34).
type TestKind string

// Test kinds.
const (
	Unit        TestKind = "unit"
	Integration TestKind = "integration"
	E2E         TestKind = "e2e"
)

// Case is one test case run, as a parser reads it. Assertions is -1 when the format does not report them
// (R5.37): never 0 for "not reported".
type Case struct {
	Suite      string
	Name       string
	File       string
	Result     Result
	Duration   time.Duration
	Retries    int
	Assertions int
}

// Parser reads one format. Detect looks at the first bytes of a file (content, not the name, R5.33). Parse
// streams: it holds under 64 MiB for any file (R6.3) and calls emit once per case.
type Parser interface {
	Format() Format
	Detect(head []byte) bool
	Parse(r io.Reader, emit func(Case) error) error
}

// Row is a stored test result (test_results).
type Row struct {
	RepositoryID int64
	RunID        int64
	RunAttempt   int
	JobID        int64 // 0 when the report is per run
	JobIdent     string
	HeadSHA      string
	ReportID     int64
	Format       Format
	TestID       string // normalised format + suite + name: no durations, no random seeds
	Kind         TestKind
	Case
	FinishedAt time.Time
}

// Normaliser makes the stable test id and the test kind from a case and its job.
type Normaliser interface {
	TestID(f Format, c Case) string
	Kind(jobName, file string) TestKind
}

// Store keeps test_results rows.
type Store interface {
	// ReplaceReport stores the rows of one report file, replacing earlier rows of the same report.
	ReplaceReport(ctx context.Context, reportID int64, rows []Row) error
	// Between returns the rows of one repository finished in w, in pages.
	Between(ctx context.Context, repositoryID int64, w insights.Window, limit, offset int) ([]Row, error)
}

// View is a Tests segment (listTestInsights).
type View string

// Views.
const (
	Slowest      View = "slowest"
	Flaky        View = "flaky"
	Duplicated   View = "duplicated"
	NoAssertions View = "no_assertions"
)

// Insight is one test in a view.
type Insight struct {
	TestID            string
	Name, Suite, File string
	Repository        string
	JobIdents         []string
	Kind              TestKind
	Runs, Failures    int
	P50, P95          time.Duration
	OverLimit         time.Duration // the G1 limit when the p95 is over it, else 0
	FlakyRuns         int
	Assertions        int
	LastSeen          time.Time
}

// Reader answers listTestInsights.
type Reader interface {
	Tests(ctx context.Context, v View, repositoryID int64, days, limit int, cursor string) (items []Insight, total int, next string, reportsFound bool, err error)
}

// The tests analyser implements insights.Analyser with these kinds.
var Kinds = []insights.Kind{insights.KindTestSlow, insights.KindTestFlaky, insights.KindTestDuplicate, insights.KindTestNoAssertions}
