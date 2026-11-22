package trial

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"
)

type fakeVars struct {
	repo   Repo
	vars   map[string]string
	want   map[string]string
	writes []string
}

func (f *fakeVars) Repository(_ context.Context, full string) (Repo, error) {
	if f.repo.FullName == "" {
		f.repo.FullName = full
	}
	return f.repo, nil
}
func (f *fakeVars) Variables(context.Context, string) (map[string]string, error) {
	out := map[string]string{}
	for k, v := range f.vars {
		out[k] = v
	}
	return out, nil
}
func (f *fakeVars) Write(_ context.Context, _, n, v string) error {
	f.vars[n] = v
	f.writes = append(f.writes, n)
	return nil
}
func (f *fakeVars) Remove(_ context.Context, _, n string) error { delete(f.vars, n); return nil }
func (f *fakeVars) Desired(context.Context, string) (map[string]string, error) {
	return f.want, nil
}

type fakeDisp struct{ calls []map[string]string }

func (d *fakeDisp) Dispatch(_ context.Context, _, wf, ref string, in map[string]string) error {
	c := map[string]string{"workflow": wf, "ref": ref}
	for k, v := range in {
		c[k] = v
	}
	d.calls = append(d.calls, c)
	return nil
}

func setup(public bool) (*Manager, *fakeVars, *fakeDisp) {
	fv := &fakeVars{repo: Repo{FullName: "o/r", Public: public},
		vars: map[string]string{"CRSP_RUNS_ON_LINUX": `["ubuntu-latest"]`, "CI_RUNS_ON": `["ubuntu-latest"]`},
		want: map[string]string{"CRSP_RUNS_ON_LINUX": `["self-hosted","linux"]`}}
	fd := &fakeDisp{}
	now := time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)
	return New(Config{Vars: fv, Dispatcher: fd, KV: &MemKV{}, Now: func() time.Time { return now }}), fv, fd
}

func ev(job string, run int64, labels ...string) JobEvent {
	t := time.Date(2026, 11, 15, 10, 5, 0, 0, time.UTC)
	return JobEvent{Repo: "o/r", RunID: run, Job: job, Labels: labels, Conclusion: "success",
		CreatedAt: t, StartedAt: t.Add(20 * time.Second), CompletedAt: t.Add(80 * time.Second)}
}

func TestObserveRecordsAndTouchesNothing(t *testing.T) {
	ctx := context.Background()
	m, fv, fd := setup(false)
	before := fmt.Sprint(fv.vars)
	if _, err := m.Set(ctx, "o/r", Observe, Options{}); err != nil {
		t.Fatal(err)
	}
	if err := m.OnJob(ctx, ev("build", 1, "ubuntu-latest")); err != nil {
		t.Fatal(err)
	}
	if err := m.OnJob(ctx, ev("odd", 1, "weird")); err != nil {
		t.Fatal(err)
	}
	rep, err := m.Report(ctx, "o/r")
	if err != nil {
		t.Fatal(err)
	}
	if len(fv.writes) != 0 || fmt.Sprint(fv.vars) != before || len(fd.calls) != 0 {
		t.Fatalf("observe changed things: %v %v", fv.writes, fd.calls)
	}
	if rep.Total != 2 || rep.Hosted.Runs != 2 || rep.Hosted.AvgWaitSec != 20 || rep.Hosted.AvgRunSec != 60 {
		t.Fatalf("report %+v", rep)
	}
	for _, j := range rep.Jobs {
		if want := map[string]int{"build": 1, "odd": 0}[j.Job]; j.WouldTake != want {
			t.Errorf("%s would_take %d", j.Job, j.WouldTake)
		}
	}
}

func TestObserveWouldTakeFollowsServedOS(t *testing.T) {
	ctx := context.Background()
	m, _, _ := setup(false)
	m.serves = func(_ context.Context, os string) bool { return os == "windows" }
	if _, err := m.Set(ctx, "o/r", Observe, Options{}); err != nil {
		t.Fatal(err)
	}
	_ = m.OnJob(ctx, ev("build", 1, "ubuntu-latest"))
	rep, _ := m.Report(ctx, "o/r")
	if rep.Jobs[0].WouldTake != 0 {
		t.Fatalf("%+v", rep.Jobs)
	}
}

func TestShadowDispatchesOnceAndRecordsCopy(t *testing.T) {
	ctx := context.Background()
	m, fv, fd := setup(false)
	before := fmt.Sprint(fv.vars)
	if _, err := m.Set(ctx, "o/r", Shadow, Options{ShadowJobs: []string{"build"}}); err != nil {
		t.Fatal(err)
	}
	e := ev("build", 7, "ubuntu-latest")
	e.Ref = "refs/heads/main"
	for i := 0; i < 2; i++ { // the same run twice: one dispatch
		if err := m.OnJob(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.OnJob(ctx, ev("lint", 7, "ubuntu-latest")); err != nil { // not selected
		t.Fatal(err)
	}
	if len(fd.calls) != 1 || fd.calls[0]["workflow"] != DefaultShadowWorkflow || fd.calls[0]["job"] != "build" || fd.calls[0]["ref"] != "refs/heads/main" {
		t.Fatalf("dispatches %v", fd.calls)
	}
	// The copy finishes on crsp.
	cp := ev("build", 8, "self-hosted", "linux")
	cp.Workflow = DefaultShadowWorkflow
	cp.Conclusion = "failure"
	if err := m.OnJob(ctx, cp); err != nil {
		t.Fatal(err)
	}
	rep, _ := m.Report(ctx, "o/r")
	var row JobRow
	for _, j := range rep.Jobs {
		if j.Job == "build" {
			row = j
		}
	}
	if row.Hosted.Runs != 2 || row.Crsp.Runs != 1 || row.Agree == nil || *row.Agree {
		t.Fatalf("row %+v", row)
	}
	if fmt.Sprint(fv.vars) != before || len(fv.writes) != 0 {
		t.Fatal("shadow touched variables")
	}
	if len(fd.calls) != 1 {
		t.Fatalf("copy dispatched again: %v", fd.calls)
	}
}

func TestCanaryWritesAndOffRestoresExactly(t *testing.T) {
	ctx := context.Background()
	m, fv, _ := setup(false)
	fv.vars["CRSP_RUNS_ON_CANARY_BUILD"] = `["old"]` // a name the canary will overwrite
	before := map[string]string{}
	for k, v := range fv.vars {
		before[k] = v
	}
	_, _ = m.Set(ctx, "o/r", Observe, Options{})
	_ = m.OnJob(ctx, ev("build", 1, "ubuntu-latest"))
	_ = m.OnJob(ctx, ev("test", 1, "ubuntu-latest"))
	st, err := m.Set(ctx, "o/r", Canary, Options{Jobs: []string{"build"}, Percent: 0})
	if err != nil {
		t.Fatal(err)
	}
	if fv.vars["CRSP_RUNS_ON_CANARY_BUILD"] != `["self-hosted","linux"]` || fv.vars["CRSP_RUNS_ON_LINUX"] != before["CRSP_RUNS_ON_LINUX"] {
		t.Fatalf("vars %v", fv.vars)
	}
	if _, ok := fv.vars["CRSP_RUNS_ON_CANARY_TEST"]; ok || len(st.Written) != 1 {
		t.Fatalf("test job selected: %v", fv.vars)
	}
	// Change the canary: still one snapshot, then off.
	if _, err := m.Set(ctx, "o/r", Canary, Options{Percent: 100}); err != nil {
		t.Fatal(err)
	}
	if _, ok := fv.vars["CRSP_RUNS_ON_CANARY_TEST"]; !ok {
		t.Fatalf("100%% left a job out: %v", fv.vars)
	}
	if _, err := m.Set(ctx, "o/r", Off, Options{}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(fv.vars, before) {
		t.Fatalf("not restored:\n got %v\nwant %v", fv.vars, before)
	}
	if g, _ := m.Get(ctx, "o/r"); g.Mode != Off || len(g.Written) != 0 {
		t.Fatalf("state %+v", g)
	}
}

func TestCanaryNeedsJobs(t *testing.T) {
	ctx := context.Background()
	m, fv, _ := setup(false)
	before := fmt.Sprint(fv.vars)
	if _, err := m.Set(ctx, "o/r", Canary, Options{Percent: 50}); !errors.Is(err, ErrNoJobs) {
		t.Fatalf("err %v", err)
	}
	if _, err := m.Set(ctx, "o/r", Canary, Options{}); !errors.Is(err, ErrBadPercent) {
		t.Fatalf("err %v", err)
	}
	if fmt.Sprint(fv.vars) != before {
		t.Fatal("failed canary left variables")
	}
	if g, _ := m.Get(ctx, "o/r"); g.Mode != Off {
		t.Fatalf("mode %s", g.Mode)
	}
}

func TestSelectedStable(t *testing.T) {
	names := []string{"build", "test", "lint", "e2e", "deploy", "Build (linux)", "docs", "release"}
	for _, n := range names {
		if Selected(n, 0, nil) {
			t.Errorf("%s in at 0%%", n)
		}
		if !Selected(n, 100, nil) {
			t.Errorf("%s out at 100%%", n)
		}
		prev := false
		for p := 0; p <= 100; p++ {
			got := Selected(n, p, nil)
			if prev && !got {
				t.Errorf("%s left at %d%%", n, p)
			}
			if got != Selected(n, p, nil) {
				t.Errorf("%s unstable", n)
			}
			prev = got
		}
	}
	if !Selected("BUILD", 0, []string{"build"}) {
		t.Error("named job not selected")
	}
	if Selected("Test", 30, nil) != Selected(" test ", 30, nil) {
		t.Error("case or space changes the choice")
	}
	in := 0
	for i := 0; i < 1000; i++ {
		if Selected(fmt.Sprintf("job-%d", i), 30, nil) {
			in++
		}
	}
	if in < 230 || in > 370 {
		t.Errorf("30%% selected %d of 1000", in)
	}
}

func TestPublicRefused(t *testing.T) {
	ctx := context.Background()
	m, fv, fd := setup(true)
	for _, mode := range []Mode{Observe, Shadow, Canary} {
		if _, err := m.Set(ctx, "o/r", mode, Options{Percent: 10, Jobs: []string{"build"}}); !errors.Is(err, ErrPublic) {
			t.Errorf("%s: err %v", mode, err)
		}
	}
	if len(fv.writes) != 0 || len(fd.calls) != 0 {
		t.Fatal("public repository was touched")
	}
	if _, err := m.Set(ctx, "o/r", Off, Options{}); err != nil {
		t.Fatalf("off on public: %v", err)
	}
}

func TestOnJobWithoutTrialIsQuiet(t *testing.T) {
	ctx := context.Background()
	m, _, fd := setup(false)
	if err := m.OnJob(ctx, ev("build", 1, "ubuntu-latest")); err != nil {
		t.Fatal(err)
	}
	if rep, _ := m.Report(ctx, "o/r"); rep.Total != 0 || len(fd.calls) != 0 {
		t.Fatalf("%+v", rep)
	}
}

func TestParseMode(t *testing.T) {
	for _, s := range []string{"observe", "SHADOW", " canary ", "off"} {
		if _, err := ParseMode(s); err != nil {
			t.Errorf("%q: %v", s, err)
		}
	}
	if _, err := ParseMode("on"); err == nil {
		t.Error("on accepted")
	}
}
