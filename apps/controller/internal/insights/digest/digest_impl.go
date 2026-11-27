package digest

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	_ "time/tzdata" // Europe/Lisbon without a zone database on the machine

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/insights"
)

// KeepFiles is how many digest files stay on disk (R5.38, R6.2).
const KeepFiles = 52

// topCount is how many findings the digest lists (R5.38).
const topCount = 3

// Findings is what the digest reads about findings. Implemented over internal/store.
type Findings interface {
	// Open returns every open finding.
	Open(ctx context.Context) ([]insights.StoredFinding, error)
	// OpenedBetween returns the findings first seen in [from, to).
	OpenedBetween(ctx context.Context, from, to time.Time) ([]insights.StoredFinding, error)
	// ResolvedBetween returns the findings resolved in [from, to).
	ResolvedBetween(ctx context.Context, from, to time.Time) ([]insights.StoredFinding, error)
}

// Index is the insight_digests table.
type Index interface {
	Put(ctx context.Context, d Digest) error
	// Last returns the newest n rows (Text left empty), newest first.
	Last(ctx context.Context, n int) ([]Digest, error)
	Delete(ctx context.Context, w Week) error
}

// WeekOf returns the ISO week containing t, in t's location.
func WeekOf(t time.Time) Week {
	y, w := t.ISOWeek()
	return Week(fmt.Sprintf("%04d-W%02d", y, w))
}

// weekStart returns Monday 00:00 of the ISO week containing t, in t's location.
func weekStart(t time.Time) time.Time {
	d := (int(t.Weekday()) + 6) % 7 // days since Monday
	y, m, day := t.Date()
	return time.Date(y, m, day-d, 0, 0, 0, 0, t.Location())
}

// Hour is the hour of Monday the digest is written (owner decision D18: 08:00), in the zone ZoneName.
const Hour = 8

// ZoneName is the zone the digest schedule is kept in (owner decision D18), whatever zone the machine is in.
const ZoneName = "Europe/Lisbon"

// Location is Europe/Lisbon. The zone data is built into the program, so a machine without a zone database
// still writes at 08:00 Lisbon time.
func Location() *time.Location {
	loc, err := time.LoadLocation(ZoneName)
	if err != nil {
		return time.UTC
	}
	return loc
}

// NextRun returns the next Monday 08:00 strictly after t, in t's location (R5.38).
func NextRun(t time.Time) time.Time {
	s := weekStart(t)
	y, m, d := s.Date()
	run := time.Date(y, m, d, Hour, 0, 0, 0, t.Location())
	if !run.After(t) {
		run = time.Date(y, m, d+7, Hour, 0, 0, 0, t.Location())
	}
	return run
}

// FileName is the digest's path relative to the data dir.
func FileName(w Week) string {
	return filepath.ToSlash(filepath.Join("ci-data", "digests", string(w)+".md"))
}

type writer struct {
	dir   string
	find  Findings
	jobs  insights.JobSource
	index Index
}

// NewWriter returns the Writer: it writes <dataDir>/ci-data/digests/<YYYY>-W<ww>.md for the ISO week before
// the one containing now. It only writes a local file and never sends anything (R6.7).
func NewWriter(dataDir string, f Findings, jobs insights.JobSource, idx Index) Writer {
	return &writer{dir: dataDir, find: f, jobs: jobs, index: idx}
}

// Write writes the digest of the week that ended before now. Writing it again replaces the file.
func (w *writer) Write(ctx context.Context, now time.Time) (Digest, error) {
	thisStart := weekStart(now)
	y, m, d := thisStart.Date()
	from := time.Date(y, m, d-7, 0, 0, 0, 0, now.Location())
	to := thisStart
	week := WeekOf(from)

	open, err := w.find.Open(ctx)
	if err != nil {
		return Digest{}, fmt.Errorf("digest open findings: %w", err)
	}
	opened, err := w.find.OpenedBetween(ctx, from, to)
	if err != nil {
		return Digest{}, fmt.Errorf("digest opened findings: %w", err)
	}
	resolved, err := w.find.ResolvedBetween(ctx, from, to)
	if err != nil {
		return Digest{}, fmt.Errorf("digest resolved findings: %w", err)
	}
	var tot Totals
	if w.jobs != nil {
		facts, err := w.jobs.JobsBetween(ctx, insights.Window{From: from.UTC(), To: to.UTC()}, 0)
		if err != nil {
			return Digest{}, fmt.Errorf("digest jobs: %w", err)
		}
		tot = SumTotals(facts)
	}
	top := insights.TopN(open, topCount)
	text := Render(week, from, to.Add(-time.Second), top, opened, resolved, tot)

	rel := FileName(week)
	path := filepath.Join(w.dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return Digest{}, fmt.Errorf("digest dir: %w", err)
	}
	if err := writeAtomic(path, []byte(text)); err != nil {
		return Digest{}, err
	}
	dg := Digest{Week: week, Path: rel, Text: text, WrittenAt: now.UTC()}
	for _, f := range top {
		dg.TopIDs = append(dg.TopIDs, f.ID)
	}
	if err := w.index.Put(ctx, dg); err != nil {
		return Digest{}, fmt.Errorf("digest index: %w", err)
	}
	if err := w.prune(ctx); err != nil {
		return dg, err
	}
	return dg, nil
}

func writeAtomic(path string, b []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("digest write: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("digest rename: %w", err)
	}
	return nil
}

// prune keeps the newest KeepFiles digests: older rows and their files go.
func (w *writer) prune(ctx context.Context) error {
	rows, err := w.index.Last(ctx, 1<<20)
	if err != nil {
		return fmt.Errorf("digest prune list: %w", err)
	}
	if len(rows) <= KeepFiles {
		return nil
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Week > rows[j].Week })
	for _, r := range rows[KeepFiles:] {
		if err := os.Remove(filepath.Join(w.dir, filepath.FromSlash(r.Path))); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("digest prune file: %w", err)
		}
		if err := w.index.Delete(ctx, r.Week); err != nil {
			return fmt.Errorf("digest prune row: %w", err)
		}
	}
	return nil
}

type reader struct {
	dir   string
	index Index
}

// NewReader returns the Reader over the index and the files under dataDir.
func NewReader(dataDir string, idx Index) Reader { return &reader{dir: dataDir, index: idx} }

// Last returns the newest n digests with their text. A row whose file is gone keeps an empty text.
func (r *reader) Last(ctx context.Context, n int) ([]Digest, error) {
	rows, err := r.index.Last(ctx, n)
	if err != nil {
		return nil, fmt.Errorf("digest list: %w", err)
	}
	for i := range rows {
		b, err := os.ReadFile(filepath.Join(r.dir, filepath.FromSlash(rows[i].Path)))
		if err == nil {
			rows[i].Text = string(b)
		}
	}
	return rows, nil
}

// Totals are the week's sums of wait, hold and run (R5.38) over the jobs that have the times.
type Totals struct {
	Jobs            int
	Wait, Hold, Run time.Duration
}

// SumTotals sums wait, hold and run per R5.12: a crsp job splits at slot_at; another runner's job has wait
// created to started and no hold. A time that is unknown or would be negative counts as nothing (R5.9).
func SumTotals(facts []insights.JobFact) Totals {
	var t Totals
	for _, f := range facts {
		if f.CompletedAt.IsZero() {
			continue
		}
		t.Jobs++
		if !f.CreatedAt.IsZero() {
			switch {
			case !f.SlotAt.IsZero() && !f.StartedAt.IsZero():
				t.Wait += pos(f.SlotAt.Sub(f.CreatedAt))
				t.Hold += pos(f.StartedAt.Sub(f.SlotAt))
			case !f.StartedAt.IsZero():
				t.Wait += pos(f.StartedAt.Sub(f.CreatedAt))
			}
		}
		if !f.StartedAt.IsZero() {
			t.Run += pos(f.CompletedAt.Sub(f.StartedAt))
		}
	}
	return t
}

func pos(d time.Duration) time.Duration {
	if d < 0 {
		return 0
	}
	return d
}

func minutes(d time.Duration) int { return int((d + 30*time.Second) / time.Minute) }

// Render builds the Markdown text. Times are UTC date and 24-hour (R6.9). Finding text is shown as text.
func Render(week Week, from, to time.Time, top, opened, resolved []insights.StoredFinding, tot Totals) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# CI digest %s\n\n%s to %s. Written on your machine, never sent.\n\n", week,
		from.Format("2006-01-02"), to.Format("2006-01-02"))
	b.WriteString("## Do next\n\n")
	if len(top) == 0 {
		b.WriteString("Nothing to fix.\n\n")
	}
	for i, f := range top {
		fmt.Fprintf(&b, "%d. %s", i+1, oneLine(f.Title))
		if f.Repository != "" {
			fmt.Fprintf(&b, " (%s)", oneLine(f.Repository))
		}
		fmt.Fprintf(&b, ": about %d min a week (estimate).\n   Fix: %s\n", f.MinutesSavedWeek, oneLine(f.Fix))
	}
	b.WriteString("\n## Findings this week\n\n")
	fmt.Fprintf(&b, "Opened: %d. Resolved: %d.\n\n", len(opened), len(resolved))
	for _, f := range opened {
		fmt.Fprintf(&b, "- opened: %s\n", oneLine(f.Title))
	}
	for _, f := range resolved {
		fmt.Fprintf(&b, "- resolved: %s\n", oneLine(f.Title))
	}
	b.WriteString("\n## Guardrails\n\n")
	changes := guardrailChanges(opened, resolved)
	if len(changes) == 0 {
		b.WriteString("No guardrail changes.\n")
	}
	for _, c := range changes {
		b.WriteString("- " + c + "\n")
	}
	b.WriteString("\n## Totals\n\n")
	fmt.Fprintf(&b, "Jobs finished: %d. Wait: %d min. Hold: %d min. Run: %d min.\n", tot.Jobs, minutes(tot.Wait),
		minutes(tot.Hold), minutes(tot.Run))
	return b.String()
}

func guardrailChanges(opened, resolved []insights.StoredFinding) []string {
	var out []string
	for _, f := range opened {
		if f.Guardrail != "" {
			out = append(out, fmt.Sprintf("%s: %s now fails", oneLine(f.Repository), f.Guardrail))
		}
	}
	for _, f := range resolved {
		if f.Guardrail != "" {
			out = append(out, fmt.Sprintf("%s: %s now passes", oneLine(f.Repository), f.Guardrail))
		}
	}
	sort.Strings(out)
	return out
}

// oneLine keeps a finding's text on one Markdown line: it never carries markup or a line break across.
func oneLine(s string) string {
	s = strings.NewReplacer("\r", " ", "\n", " ", "<", "&lt;", ">", "&gt;").Replace(s)
	return strings.TrimSpace(s)
}

// Clock is the schedule's time source (the engine's Clock).
type Clock = insights.Clock

// Run writes the digest at every Monday 08:00 of loc (use Location()) until ctx ends. A failed write is passed to
// onErr and the schedule goes on.
func Run(ctx context.Context, w Writer, c Clock, loc *time.Location, onErr func(error)) {
	for {
		now := c.Now().In(loc)
		next := NextRun(now)
		select {
		case <-ctx.Done():
			return
		case <-c.After(next.Sub(now)):
		}
		if _, err := w.Write(ctx, next); err != nil && onErr != nil {
			onErr(err)
		}
	}
}
