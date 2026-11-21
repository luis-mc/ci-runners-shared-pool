package jobsize

import (
	"context"
	"fmt"
	"path"
	"strings"
	"time"
)

// Rule is an override: a job pattern (a name or a glob) and the class it sets.
type Rule struct {
	Repository string
	Pattern    string
	Class      Class
	Origin     string // OriginConsole or OriginFile
}

// The two places an override comes from. The console setting wins over the file.
const (
	OriginConsole = "console"
	OriginFile    = "file"
)

// ScanRow is what the store keeps of the scan of one job.
type ScanRow struct {
	Repository string
	Job        string
	Name       string
	Workflow   string
	Weight     Weight
	CPU, GiB   float64
	Class      Class
	Confidence Confidence
	Tests      int
	// PrevTests is the test count of the scan before; 0 when none.
	PrevTests int
	Reasons   []string
	Services  []string
	Commit    string
	ScannedAt time.Time
	// ResetNote is set when the test count moved by more than 2x since the
	// scan before and the size went back to the scan guess; "" otherwise.
	ResetNote string
	EstSec    int
}

// Measured is a size from measured runs (OPS66 writes it; the scan only reads it).
type Measured struct {
	Repository string
	Job        string
	Weight     Weight
	Runs       int
}

// Size is the answer for one job: the numbers, the class and where they came from.
type Size struct {
	Weight Weight
	Class  Class
	Source Source
	// Pattern is the override pattern that matched, for SourceOverride.
	Pattern string
	Origin  string
}

// Match reports whether pattern matches the job key or the display name.
func Match(pattern string, names ...string) bool {
	for _, n := range names {
		if n == "" {
			continue
		}
		if ok, err := path.Match(pattern, n); err == nil && ok {
			return true
		}
	}
	return false
}

// MeasuredAfter is how many runs a measured size needs before it replaces the
// scan guess (about 5, owner decision).
const MeasuredAfter = 5

// Resolve picks the size of one job. The order is the console override, the
// file override, the measured size (after MeasuredAfter runs), the scan guess,
// then DefaultWeight. ok is false only when nothing is known and the caller
// should fall back to the name classes.
func Resolve(rules []Rule, measured *Measured, scan *ScanRow, names ...string) (Size, bool) {
	for _, origin := range []string{OriginConsole, OriginFile} {
		for _, r := range rules {
			if r.Origin == origin && Match(r.Pattern, names...) {
				return Size{Weight: ClassWeight(r.Class), Class: r.Class, Source: SourceOverride, Pattern: r.Pattern, Origin: origin}, true
			}
		}
	}
	if measured != nil && measured.Runs >= MeasuredAfter {
		return Size{Weight: measured.Weight, Class: ClassOfPeak(float64(measured.Weight.CPU), float64(measured.Weight.Memory)), Source: SourceMeasured}, true
	}
	if scan != nil {
		return Size{Weight: scan.Weight, Class: scan.Class, Source: SourceScan}, true
	}
	return Size{Weight: DefaultWeight, Class: Light, Source: SourceDefault}, false
}

// Reconcile compares a new scan row with the one before. When the test count
// moved by more than 2x, up or down, the size is the scan guess again: the
// measured size is dropped (reset is true) and the row says so for the console.
func Reconcile(prev *ScanRow, next ScanRow) (row ScanRow, reset bool) {
	if prev == nil || prev.Tests <= 0 {
		return next, false
	}
	next.PrevTests = prev.Tests
	if next.Tests > 2*prev.Tests || next.Tests*2 < prev.Tests {
		next.ResetNote = fmt.Sprintf("Tests went from %d to %d, so the size went back to the scan guess.", prev.Tests, next.Tests)
		return next, true
	}
	return next, false
}

// Store keeps the scan rows, the overrides and the measured sizes.
type Store interface {
	ScanRows(ctx context.Context, repository string) ([]ScanRow, error)
	// ReplaceScan swaps the repository's scan rows and its file rules for the
	// new ones, and drops the measured size of each job in reset.
	ReplaceScan(ctx context.Context, repository string, rows []ScanRow, file []Rule, fileErr string, reset []string) error
	Rules(ctx context.Context, repository string) ([]Rule, error)
	FileError(ctx context.Context, repository string) (string, error)
	// SetConsoleRule saves a console override; an empty class removes it.
	SetConsoleRule(ctx context.Context, repository, pattern string, class Class) error
	Measured(ctx context.Context, repository string) ([]Measured, error)
	// Table is the saved JSON of edited cost rows, "" when none.
	Table(ctx context.Context) (string, error)
}

// Service scans repositories and answers for the console and the scheduler.
type Service struct {
	Store   Store
	Scanner interface {
		Scan(ctx context.Context, repository, commit string, table CostTable) (Scan, error)
	}
	Now func() time.Time
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

// ScanRepo scans repository at commit and keeps the result. A commit already
// scanned is skipped. An error leaves the old rows in place: a failed scan
// never changes a size, and nothing here holds or blocks a job.
func (s *Service) ScanRepo(ctx context.Context, repository, commit string) error {
	old, err := s.Store.ScanRows(ctx, repository)
	if err != nil {
		return err
	}
	for _, o := range old {
		if o.Commit == commit {
			return nil
		}
	}
	res, err := s.Scanner.Scan(ctx, repository, commit, s.CostTable(ctx))
	if err != nil {
		return err
	}
	prev := map[string]*ScanRow{}
	for i := range old {
		prev[old[i].Job] = &old[i]
	}
	var rows []ScanRow
	var reset []string
	for _, g := range res.Jobs {
		row, r := Reconcile(prev[g.Job], ScanRow{
			Repository: repository, Job: g.Job, Name: g.Name, Workflow: g.Workflow, Weight: g.Weight, CPU: g.CPU, GiB: g.GiB,
			Class: g.Class, Confidence: g.Confidence, Tests: g.Tests, Reasons: g.Reasons, Services: g.Services,
			Commit: commit, ScannedAt: s.now(), EstSec: g.EstSeconds,
		})
		if !r && prev[g.Job] != nil {
			// A quiet rescan keeps an older reset note until the tests move again.
			row.ResetNote = prev[g.Job].ResetNote
		}
		if r {
			reset = append(reset, g.Job)
		}
		rows = append(rows, row)
	}
	var file []Rule
	for _, f := range res.File {
		file = append(file, Rule{Repository: repository, Pattern: f.Pattern, Class: f.Class, Origin: OriginFile})
	}
	return s.Store.ReplaceScan(ctx, repository, rows, file, res.FileError, reset)
}

// View is one row of the console: the job, its size, where it came from, and
// both override values.
type View struct {
	Repository string
	Job        string
	Size       Size
	Scan       *ScanRow
	Measured   *Measured
	Console    string // the console override class for this job's pattern, "" when none
	File       string // the file override class, "" when none
}

// Views lists the jobs of a repository with their resolved sizes. Rules that
// match no scanned job still get a row, so an override can be seen and removed.
func (s *Service) Views(ctx context.Context, repository string) ([]View, string, error) {
	rows, err := s.Store.ScanRows(ctx, repository)
	if err != nil {
		return nil, "", err
	}
	rules, err := s.Store.Rules(ctx, repository)
	if err != nil {
		return nil, "", err
	}
	ms, err := s.Store.Measured(ctx, repository)
	if err != nil {
		return nil, "", err
	}
	ferr, err := s.Store.FileError(ctx, repository)
	if err != nil {
		return nil, "", err
	}
	mj := map[string]*Measured{}
	for i := range ms {
		mj[ms[i].Job] = &ms[i]
	}
	used := map[string]bool{}
	var out []View
	cover := func(origin string, names ...string) string {
		for _, r := range rules {
			if r.Origin == origin && Match(r.Pattern, names...) {
				used[origin+"\x00"+r.Pattern] = true
				return string(r.Class)
			}
		}
		return ""
	}
	seen := map[string]bool{}
	for i := range rows {
		r := &rows[i]
		sz, _ := Resolve(rules, mj[r.Job], r, r.Job, r.Name)
		out = append(out, View{Repository: repository, Job: r.Job, Size: sz, Scan: r, Measured: mj[r.Job],
			Console: cover(OriginConsole, r.Job, r.Name), File: cover(OriginFile, r.Job, r.Name)})
		seen[r.Job] = true
	}
	for _, m := range ms {
		if seen[m.Job] {
			continue
		}
		sz, _ := Resolve(rules, &m, nil, m.Job)
		mm := m
		out = append(out, View{Repository: repository, Job: m.Job, Size: sz, Measured: &mm,
			Console: cover(OriginConsole, m.Job), File: cover(OriginFile, m.Job)})
		seen[m.Job] = true
	}
	for _, r := range rules {
		if used[r.Origin+"\x00"+r.Pattern] || seen[r.Pattern] || strings.TrimSpace(r.Pattern) == "" {
			continue
		}
		v := View{Repository: repository, Job: r.Pattern, Size: Size{Weight: ClassWeight(r.Class), Class: r.Class, Source: SourceOverride, Pattern: r.Pattern, Origin: r.Origin}}
		if r.Origin == OriginConsole {
			v.Console = string(r.Class)
		} else {
			v.File = string(r.Class)
		}
		out = append(out, v)
		seen[r.Pattern] = true
	}
	return out, ferr, nil
}

// SetOverride saves a console override for a job pattern; class "" removes it.
func (s *Service) SetOverride(ctx context.Context, repository, pattern string, class Class) error {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" || strings.ContainsAny(pattern, "\n\r\x00") || len(pattern) > 200 {
		return fmt.Errorf("the job pattern is empty or not valid")
	}
	if _, err := path.Match(pattern, ""); err != nil {
		return fmt.Errorf("the job pattern %q is not a valid glob", pattern)
	}
	if class != "" && !ValidClass(class) {
		return fmt.Errorf("the class must be light, normal or heavy")
	}
	return s.Store.SetConsoleRule(ctx, repository, pattern, class)
}

// CostTable is the table in force: the defaults and the saved edits.
func (s *Service) CostTable(ctx context.Context) CostTable {
	t := DefaultCostTable()
	raw, err := s.Store.Table(ctx)
	if err != nil {
		return t
	}
	patch, err := ParseCostPatch(raw)
	if err != nil {
		return t
	}
	return t.Merge(patch)
}

// CopyLine is the YAML line the console copies to .crsp.yml for an override.
func CopyLine(pattern string, class Class) string {
	return fmt.Sprintf("sizes: { %q: %s }", pattern, class)
}
