package jobsize

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type memStore struct {
	rows     map[string][]ScanRow
	rules    map[string][]Rule
	measured map[string][]Measured
	ferr     map[string]string
	table    string
	fail     bool
}

func newMem() *memStore {
	return &memStore{rows: map[string][]ScanRow{}, rules: map[string][]Rule{}, measured: map[string][]Measured{}, ferr: map[string]string{}}
}

func (m *memStore) ScanRows(_ context.Context, r string) ([]ScanRow, error) { return m.rows[r], nil }
func (m *memStore) Rules(_ context.Context, r string) ([]Rule, error)       { return m.rules[r], nil }
func (m *memStore) Measured(_ context.Context, r string) ([]Measured, error) {
	return m.measured[r], nil
}
func (m *memStore) FileError(_ context.Context, r string) (string, error) { return m.ferr[r], nil }
func (m *memStore) Table(context.Context) (string, error)                 { return m.table, nil }
func (m *memStore) SetConsoleRule(_ context.Context, r, p string, c Class) error {
	var keep []Rule
	for _, x := range m.rules[r] {
		if x.Origin != OriginConsole || x.Pattern != p {
			keep = append(keep, x)
		}
	}
	if c != "" {
		keep = append(keep, Rule{Repository: r, Pattern: p, Class: c, Origin: OriginConsole})
	}
	m.rules[r] = keep
	return nil
}
func (m *memStore) ReplaceScan(_ context.Context, r string, rows []ScanRow, file []Rule, ferr string, reset []string) error {
	if m.fail {
		return errors.New("disk full")
	}
	m.rows[r] = rows
	var keep []Rule
	for _, x := range m.rules[r] {
		if x.Origin != OriginFile {
			keep = append(keep, x)
		}
	}
	m.rules[r] = append(keep, file...)
	m.ferr[r] = ferr
	var ms []Measured
	for _, x := range m.measured[r] {
		drop := false
		for _, j := range reset {
			drop = drop || j == x.Job
		}
		if !drop {
			ms = append(ms, x)
		}
	}
	m.measured[r] = ms
	return nil
}

type fakeScanner struct {
	res Scan
	err error
	n   int
}

func (f *fakeScanner) Scan(_ context.Context, repo, commit string, _ CostTable) (Scan, error) {
	f.n++
	r := f.res
	r.Repository, r.Commit = repo, commit
	return r, f.err
}

func TestResolvePrecedence(t *testing.T) {
	scan := &ScanRow{Job: "integration-db", Weight: Weight{2, 3}, Class: Heavy}
	meas := &Measured{Weight: Weight{1, 2}, Runs: 7}
	file := Rule{Pattern: "integration-*", Class: Light, Origin: OriginFile}
	cons := Rule{Pattern: "integration-db", Class: Normal, Origin: OriginConsole}
	cases := []struct {
		name   string
		rules  []Rule
		meas   *Measured
		scan   *ScanRow
		want   Source
		wantW  Weight
		wantOK bool
	}{
		{"console beats file", []Rule{file, cons}, meas, scan, SourceOverride, Weight{2, 2}, true},
		{"file beats measured", []Rule{file}, meas, scan, SourceOverride, Weight{1, 1}, true},
		{"measured beats scan", nil, meas, scan, SourceMeasured, Weight{1, 2}, true},
		{"too few runs fall to scan", nil, &Measured{Weight: Weight{1, 2}, Runs: 4}, scan, SourceScan, Weight{2, 3}, true},
		{"scan alone", nil, nil, scan, SourceScan, Weight{2, 3}, true},
		{"nothing known is the default", nil, nil, nil, SourceDefault, DefaultWeight, false},
		{"a file pattern misses", []Rule{{Pattern: "e2e-*", Class: Heavy, Origin: OriginFile}}, nil, scan, SourceScan, Weight{2, 3}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := Resolve(c.rules, c.meas, c.scan, "integration-db")
			if got.Source != c.want || got.Weight != c.wantW || ok != c.wantOK {
				t.Fatalf("got %+v ok=%v, want %s %v ok=%v", got, ok, c.want, c.wantW, c.wantOK)
			}
		})
	}
}

func TestReconcileTwoTimes(t *testing.T) {
	prev := &ScanRow{Tests: 100}
	cases := []struct {
		name  string
		prev  *ScanRow
		tests int
		reset bool
	}{
		{"no history", nil, 500, false},
		{"previous zero", &ScanRow{Tests: 0}, 500, false},
		{"same", prev, 100, false},
		{"exactly 2x up", prev, 200, false},
		{"over 2x up", prev, 201, true},
		{"exactly half", prev, 50, false},
		{"under half", prev, 49, true},
		{"gone to zero", prev, 0, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			row, reset := Reconcile(c.prev, ScanRow{Tests: c.tests})
			if reset != c.reset || (row.ResetNote != "") != c.reset {
				t.Fatalf("reset = %v note = %q, want %v", reset, row.ResetNote, c.reset)
			}
			if reset && !strings.Contains(row.ResetNote, "100") {
				t.Fatalf("note %q does not name the old count", row.ResetNote)
			}
		})
	}
}

func TestScanRepoResetDropsMeasured(t *testing.T) {
	st := newMem()
	sc := &fakeScanner{res: Scan{Jobs: []Guess{{Job: "e2e", Tests: 100, Weight: Weight{2, 2}, Class: Normal}}}}
	svc := &Service{Store: st, Scanner: sc, Now: func() time.Time { return time.Unix(1000, 0) }}
	ctx := context.Background()
	sha1, sha2, sha3 := strings.Repeat("1", 40), strings.Repeat("2", 40), strings.Repeat("3", 40)
	if err := svc.ScanRepo(ctx, "o/r", sha1); err != nil {
		t.Fatal(err)
	}
	st.measured["o/r"] = []Measured{{Repository: "o/r", Job: "e2e", Weight: Weight{1, 1}, Runs: 9}}
	// A small change keeps the measured size.
	sc.res.Jobs[0].Tests = 150
	if err := svc.ScanRepo(ctx, "o/r", sha2); err != nil {
		t.Fatal(err)
	}
	if len(st.measured["o/r"]) != 1 || st.rows["o/r"][0].ResetNote != "" {
		t.Fatalf("a 1.5x change reset: %+v", st.rows["o/r"])
	}
	// More than 2x of the last scan resets to the guess and says so.
	sc.res.Jobs[0].Tests = 400
	if err := svc.ScanRepo(ctx, "o/r", sha3); err != nil {
		t.Fatal(err)
	}
	if len(st.measured["o/r"]) != 0 || !strings.Contains(st.rows["o/r"][0].ResetNote, "150 to 400") {
		t.Fatalf("no reset: %+v measured=%v", st.rows["o/r"], st.measured["o/r"])
	}
	views, _, _ := svc.Views(ctx, "o/r")
	if views[0].Size.Source != SourceScan || views[0].Scan.ResetNote == "" {
		t.Fatalf("view = %+v", views[0])
	}
	// The same commit is not scanned again.
	n := sc.n
	_ = svc.ScanRepo(ctx, "o/r", sha3)
	if sc.n != n {
		t.Fatal("a scanned commit was scanned again")
	}
}

// A new job never blocks: a failed scan, a scan that finds nothing and a store
// that fails all leave the answer at the default; no call holds anything.
func TestNewJobNeverBlocks(t *testing.T) {
	ctx := context.Background()
	st := newMem()
	sc := &fakeScanner{err: errors.New("GitHub is down")}
	svc := &Service{Store: st, Scanner: sc}
	if err := svc.ScanRepo(ctx, "o/r", strings.Repeat("a", 40)); err == nil {
		t.Fatal("a failed scan must report its error to the caller, which only logs it")
	}
	views, _, err := svc.Views(ctx, "o/r")
	if err != nil || len(views) != 0 {
		t.Fatalf("views = %v err = %v", views, err)
	}
	if sz, ok := Resolve(nil, nil, nil, "brand-new-job"); ok || sz.Source != SourceDefault || sz.Weight != (Weight{1, 1}) {
		t.Fatalf("new job = %+v ok = %v, want the default 1/1", sz, ok)
	}
	sc.err = nil
	if err := svc.ScanRepo(ctx, "o/r", strings.Repeat("b", 40)); err != nil || len(st.rows["o/r"]) != 0 {
		t.Fatalf("empty scan: err = %v rows = %v", err, st.rows["o/r"])
	}
	st.fail = true
	sc.res = Scan{Jobs: []Guess{{Job: "x", Tests: 1, Weight: Weight{3, 3}, Class: Heavy}}}
	if err := svc.ScanRepo(ctx, "o/r", strings.Repeat("c", 40)); err == nil {
		t.Fatal("store failure must be returned")
	}
	if views, _, _ := svc.Views(ctx, "o/r"); len(views) != 0 {
		t.Fatalf("a failed save left rows: %v", views)
	}
}

func TestViewsShowBothOverridesAndSource(t *testing.T) {
	ctx := context.Background()
	st := newMem()
	st.rows["o/r"] = []ScanRow{{Repository: "o/r", Job: "integration-db", Weight: Weight{1, 1}, Class: Light, Tests: 3}, {Repository: "o/r", Job: "lint", Weight: Weight{1, 1}, Class: Light}}
	st.rules["o/r"] = []Rule{{Repository: "o/r", Pattern: "integration-*", Class: Heavy, Origin: OriginFile}, {Repository: "o/r", Pattern: "ghost-*", Class: Normal, Origin: OriginFile}}
	svc := &Service{Store: st}
	if err := svc.SetOverride(ctx, "o/r", "integration-db", Normal); err != nil {
		t.Fatal(err)
	}
	views, _, err := svc.Views(ctx, "o/r")
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]View{}
	for _, v := range views {
		by[v.Job] = v
	}
	db := by["integration-db"]
	if db.Console != "normal" || db.File != "heavy" || db.Size.Source != SourceOverride || db.Size.Class != Normal || db.Size.Origin != OriginConsole {
		t.Fatalf("integration-db = %+v", db)
	}
	if by["lint"].Size.Source != SourceScan {
		t.Fatalf("lint = %+v", by["lint"])
	}
	if g := by["ghost-*"]; g.File != "normal" || g.Size.Source != SourceOverride {
		t.Fatalf("a rule with no job is not shown: %+v", g)
	}
	// Removing the console override lets the file one show.
	if err := svc.SetOverride(ctx, "o/r", "integration-db", ""); err != nil {
		t.Fatal(err)
	}
	views, _, _ = svc.Views(ctx, "o/r")
	for _, v := range views {
		if v.Job == "integration-db" && (v.Console != "" || v.Size.Class != Heavy) {
			t.Fatalf("after removal: %+v", v)
		}
	}
}

func TestSetOverrideValidates(t *testing.T) {
	svc := &Service{Store: newMem()}
	for _, c := range []struct{ pat, class string }{{"", "heavy"}, {"a[", "heavy"}, {"a", "huge"}, {"a\nb", "light"}} {
		if err := svc.SetOverride(context.Background(), "o/r", c.pat, Class(c.class)); err == nil {
			t.Errorf("SetOverride(%q, %q) = nil, want an error", c.pat, c.class)
		}
	}
}

func TestCostTableEdits(t *testing.T) {
	st := newMem()
	svc := &Service{Store: st}
	if got := svc.CostTable(context.Background()).Frameworks["playwright"]; got.CPU != 1 || got.GiB != 1 || got.SecPerTest != 10 || !got.Assumption {
		t.Fatalf("default playwright = %+v", got)
	}
	st.table = `{"frameworks":{"playwright":{"class":"heavy","cpu":2,"gib":2}},"services":{"kafka":{"class":"bad","cpu":1,"gib":9}}}`
	tbl := svc.CostTable(context.Background())
	if tbl.Frameworks["playwright"].CPU != 2 || tbl.Services["kafka"].GiB != 2.5 {
		t.Fatalf("table = %+v", tbl)
	}
	st.table = "{not json"
	if svc.CostTable(context.Background()).Frameworks["go"].CPU != 1 {
		t.Fatal("bad JSON must leave the defaults")
	}
}

func TestCopyLine(t *testing.T) {
	if got := CopyLine("integration-*", Heavy); got != `sizes: { "integration-*": heavy }` {
		t.Fatalf("got %q", got)
	}
}
