package digest

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/insights"
)

type fakeFind struct {
	open, opened, resolved []insights.StoredFinding
	from, to               time.Time
}

func (f *fakeFind) Open(context.Context) ([]insights.StoredFinding, error) { return f.open, nil }
func (f *fakeFind) OpenedBetween(_ context.Context, from, to time.Time) ([]insights.StoredFinding, error) {
	f.from, f.to = from, to
	return f.opened, nil
}
func (f *fakeFind) ResolvedBetween(context.Context, time.Time, time.Time) ([]insights.StoredFinding, error) {
	return f.resolved, nil
}

type memIndex struct{ rows map[Week]Digest }

func (m *memIndex) Put(_ context.Context, d Digest) error { m.rows[d.Week] = d; return nil }
func (m *memIndex) Delete(_ context.Context, w Week) error {
	delete(m.rows, w)
	return nil
}
func (m *memIndex) Last(_ context.Context, n int) ([]Digest, error) {
	var out []Digest
	for _, d := range m.rows {
		d.Text = ""
		out = append(out, d)
	}
	for i := range out { // newest week first
		for j := i + 1; j < len(out); j++ {
			if out[j].Week > out[i].Week {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	if n < len(out) {
		out = out[:n]
	}
	return out, nil
}

type jobs []insights.JobFact

func (j jobs) JobsBetween(context.Context, insights.Window, int64) ([]insights.JobFact, error) {
	return j, nil
}

func sf(id int64, mins int, title string) insights.StoredFinding {
	return insights.StoredFinding{ID: id, Finding: insights.Finding{Title: title, Fix: "fix " + title, MinutesSavedWeek: mins, Repository: "acme/api"}}
}

var monday = time.Date(2026, 11, 23, 8, 0, 0, 0, time.UTC)

// R5.38: Monday 08:00 writes <data dir>/ci-data/digests/<YYYY>-W<ww>.md with the top 3 findings.
func TestWeeklyFileTopThree(t *testing.T) {
	dir := t.TempDir()
	ff := &fakeFind{
		open:     []insights.StoredFinding{sf(1, 5, "five"), sf(2, 50, "fifty"), sf(3, 20, "twenty"), sf(4, 1, "one"), sf(5, 9, "nine")},
		opened:   []insights.StoredFinding{sf(2, 50, "fifty")},
		resolved: []insights.StoredFinding{sf(9, 3, "old")},
	}
	ff.resolved[0].Guardrail = "G1"
	created := time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC)
	js := jobs{{CompletedAt: created.Add(10 * time.Minute), StartedAt: created.Add(5 * time.Minute), SlotAt: created.Add(3 * time.Minute), CreatedAt: created}}
	idx := &memIndex{rows: map[Week]Digest{}}
	dg, err := NewWriter(dir, ff, js, idx).Write(context.Background(), monday)
	if err != nil {
		t.Fatal(err)
	}
	if dg.Week != "2026-W47" || dg.Path != "ci-data/digests/2026-W47.md" {
		t.Fatalf("%+v", dg)
	}
	b, err := os.ReadFile(filepath.Join(dir, "ci-data", "digests", "2026-W47.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	for _, want := range []string{"1. fifty", "2. twenty", "3. nine", "(estimate)", "Opened: 1. Resolved: 1.", "acme/api: G1 now passes",
		"Wait: 3 min. Hold: 2 min. Run: 5 min."} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in:\n%s", want, text)
		}
	}
	if strings.Contains(text, "four") || strings.Contains(text, "4. ") {
		t.Errorf("more than three findings:\n%s", text)
	}
	if !ff.from.Equal(time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC)) || !ff.to.Equal(time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("week range %v %v", ff.from, ff.to)
	}
	if len(dg.TopIDs) != 3 || dg.TopIDs[0] != 2 || idx.rows["2026-W47"].Path == "" {
		t.Fatalf("%+v", dg)
	}
	last, err := NewReader(dir, idx).Last(context.Background(), 12)
	if err != nil || len(last) != 1 || last[0].Text != text {
		t.Fatalf("reader %v %v", last, err)
	}
}

// R5.38: next run is the next Monday 08:00 in the zone it is given.
func TestNextRunMonday0800(t *testing.T) {
	loc := time.FixedZone("x", 3600)
	cases := []struct{ in, want time.Time }{
		{time.Date(2026, 11, 23, 7, 59, 0, 0, loc), time.Date(2026, 11, 23, 8, 0, 0, 0, loc)},
		{time.Date(2026, 11, 23, 8, 0, 0, 0, loc), time.Date(2026, 11, 30, 8, 0, 0, 0, loc)},
		{time.Date(2026, 11, 22, 23, 0, 0, 0, loc), time.Date(2026, 11, 23, 8, 0, 0, 0, loc)}, // Sunday
		{time.Date(2026, 11, 25, 12, 0, 0, 0, loc), time.Date(2026, 11, 30, 8, 0, 0, 0, loc)},
	}
	for _, c := range cases {
		if got := NextRun(c.in); !got.Equal(c.want) {
			t.Errorf("NextRun(%v)=%v want %v", c.in, got, c.want)
		}
	}
}

// R5.38: the last 52 files are kept.
func TestKeepsLast52(t *testing.T) {
	dir := t.TempDir()
	idx := &memIndex{rows: map[Week]Digest{}}
	start := monday
	ctx := context.Background()
	w := NewWriter(dir, &fakeFind{}, nil, idx)
	for i := 0; i < 54; i++ {
		if _, err := w.Write(ctx, start.AddDate(0, 0, 7*i)); err != nil {
			t.Fatal(err)
		}
	}
	files, _ := os.ReadDir(filepath.Join(dir, "ci-data", "digests"))
	if len(files) != KeepFiles || len(idx.rows) != KeepFiles {
		t.Fatalf("files=%d rows=%d", len(files), len(idx.rows))
	}
	if _, err := os.Stat(filepath.Join(dir, "ci-data", "digests", "2026-W47.md")); !os.IsNotExist(err) {
		t.Fatal("oldest file must be gone")
	}
}

// R5.38 and R6.7: finding text is one line of text, never markup.
func TestRenderEscapesMarkup(t *testing.T) {
	out := Render("2026-W47", monday, monday, []insights.StoredFinding{sf(1, 1, "<b>x</b>\nsecond")}, nil, nil, Totals{})
	if strings.Contains(out, "<b>") || strings.Contains(out, "x</b>\nsecond") {
		t.Fatal(out)
	}
}

type fakeClock struct {
	now   time.Time
	waits []time.Duration
	ch    chan time.Time
}

func (c *fakeClock) Now() time.Time { return c.now }
func (c *fakeClock) After(d time.Duration) <-chan time.Time {
	c.waits = append(c.waits, d)
	return c.ch
}

type recWriter struct {
	at  []time.Time
	err error
}

func (r *recWriter) Write(_ context.Context, now time.Time) (Digest, error) {
	r.at = append(r.at, now)
	return Digest{}, r.err
}

// R5.38, R6.8: Run waits until Monday 08:00, writes, and a failed write does not stop the schedule.
func TestRunSchedule(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	c := &fakeClock{now: time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC), ch: make(chan time.Time, 1)}
	c.ch <- time.Time{} // the first wait is over at once
	rw := &recWriter{err: errors.New("disk full")}
	var errs int
	done := make(chan struct{})
	go func() {
		Run(ctx, rw, c, time.UTC, func(error) {
			errs++
			cancel() // stop after the first failed write; the second wait sees a cancelled context
		})
		close(done)
	}()
	<-done
	if len(rw.at) != 1 || !rw.at[0].Equal(monday) || errs != 1 {
		t.Fatalf("writes %v errs %d", rw.at, errs)
	}
	if c.waits[0] != monday.Sub(c.now) {
		t.Fatalf("waits %v", c.waits)
	}
}

// D18: the schedule is Monday 08:00 Europe/Lisbon, in winter (UTC+0) and in summer time (UTC+1).
func TestNextRunLisbon(t *testing.T) {
	loc := Location()
	if loc.String() != "Europe/Lisbon" {
		t.Fatalf("zone = %v", loc)
	}
	winter := NextRun(time.Date(2026, 11, 20, 12, 0, 0, 0, loc))
	if want := time.Date(2026, 11, 23, 8, 0, 0, 0, time.UTC); !winter.Equal(want) {
		t.Errorf("winter = %v want %v", winter, want)
	}
	summer := NextRun(time.Date(2026, 7, 8, 12, 0, 0, 0, loc))
	if want := time.Date(2026, 7, 13, 7, 0, 0, 0, time.UTC); !summer.Equal(want) {
		t.Errorf("summer = %v want %v (08:00 Lisbon summer time is 07:00 UTC)", summer, want)
	}
}
