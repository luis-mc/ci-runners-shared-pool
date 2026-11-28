package poolname

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

type kv struct {
	mu sync.Mutex
	m  map[string]string
}

func (k *kv) RawSetting(_ context.Context, key string) (string, bool, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	v, ok := k.m[key]
	return v, ok, nil
}

func (k *kv) PutRawSetting(_ context.Context, key, v, _ string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.m[key] = v
	return nil
}

// world is the fake GitHub side: runners with labels, repositories with the
// label their variables carry, and the order of every call.
type world struct {
	mu        sync.Mutex
	name      *Name
	labels    map[string]map[string]bool // runner id -> labels
	vars      map[int64]string           // repository -> label in its variables
	calls     []string
	failOn    string // a call key that fails once
	afterCall func(call string)
	scale     bool
	noRepos   bool
	scaleName string
}

func newWorld(n *Name) *world {
	return &world{name: n, labels: map[string]map[string]bool{
		"r1": {"crsp-home": true}, "r2": {"crsp-home": true}, "e1": {"crsp-home": true},
	}, vars: map[int64]string{1: "crsp-home", 2: "crsp-home"}}
}

func (w *world) note(call string) error {
	w.calls = append(w.calls, call)
	if w.failOn == call {
		w.failOn = ""
		return errors.New("GitHub could not be reached")
	}
	if w.afterCall != nil {
		w.afterCall(call)
	}
	return nil
}

func (w *world) WithLabel(_ context.Context, label string) ([]RunnerRef, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	var out []RunnerRef
	for _, id := range []string{"r1", "r2", "e1"} {
		if w.labels[id][label] {
			out = append(out, RunnerRef{ID: id, Name: "runner-" + id, Registered: id != "e1", Ephemeral: id == "e1"})
		}
	}
	return out, nil
}

func (w *world) AddLabel(_ context.Context, id, label, _ string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.note("add:" + id); err != nil {
		return err
	}
	w.labels[id][label] = true
	return nil
}

func (w *world) RemoveLabel(_ context.Context, id, label, _ string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.note("remove:" + id); err != nil {
		return err
	}
	delete(w.labels[id], label)
	return nil
}

func (w *world) PoolOn(context.Context) ([]RepoRef, error) {
	if w.noRepos {
		return nil, nil
	}
	return []RepoRef{{ID: 1, FullName: "luis-mc/a"}, {ID: 2, FullName: "luis-mc/b"}}, nil
}

func (w *world) SyncVariables(_ context.Context, id int64, _ string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.note(fmt.Sprintf("vars:%d", id)); err != nil {
		return err
	}
	w.vars[id] = w.name.Get() // the writer reads the name in force
	return nil
}

func (w *world) On() bool { return w.scale }
func (w *world) Recreate(context.Context) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.scaleName = w.name.Get()
	return w.note("scale")
}

type audits struct{ lines []string }

func (a *audits) Audit(_ context.Context, _, action, subject, _ string) {
	a.lines = append(a.lines, action+" "+subject)
}

func rig(t *testing.T) (*Service, *world, *kv, *audits) {
	t.Helper()
	t.Setenv(EnvName, "")
	db := &kv{m: map[string]string{}}
	n, _ := Resolve(context.Background(), db)
	w := newWorld(n)
	a := &audits{}
	n2 := 0
	s := New(Config{Name: n, KV: db, Runners: w, Repos: w, ScaleSet: scaleOf(w), Audit: a, Background: func(f func()) { f() },
		NewID: func() string { n2++; return fmt.Sprintf("job%d", n2) }})
	return s, w, db, a
}

func scaleOf(w *world) ScaleSet { return w }

func TestValidate(t *testing.T) {
	ok := []string{"crsp-home", "crsp-lab-2", "crsp-a"}
	for _, n := range ok {
		if err := Validate(n); err != nil {
			t.Errorf("%q refused: %v", n, err)
		}
	}
	bad := []string{"", "home", "crsp-", "crsp_home", "CRSP-home", "crsp home", "crsp-a,b", "crsp--x", "self-hosted", "crsp-" + strings.Repeat("a", 40)}
	for _, n := range bad {
		if err := Validate(n); err == nil {
			t.Errorf("%q accepted", n)
		}
	}
}

func TestResolvePrecedence(t *testing.T) {
	db := &kv{m: map[string]string{nameKey: `"crsp-stored"`}}
	t.Setenv(EnvName, "")
	if n, _ := Resolve(context.Background(), db); n.Get() != "crsp-stored" || n.Source() != SourceStored {
		t.Fatalf("stored should win over the default: %s %s", n.Get(), n.Source())
	}
	t.Setenv(EnvName, "crsp-env")
	if n, _ := Resolve(context.Background(), db); n.Get() != "crsp-env" || n.Source() != SourceEnv {
		t.Fatalf("env should win over stored: %s %s", n.Get(), n.Source())
	}
	t.Setenv(EnvName, "Bad Name")
	n, ignored := Resolve(context.Background(), db)
	if n.Get() != "crsp-stored" || ignored == "" {
		t.Fatalf("a bad env value is ignored with a reason: %s %q", n.Get(), ignored)
	}
	t.Setenv(EnvName, "")
	if n, _ := Resolve(context.Background(), &kv{m: map[string]string{}}); n.Get() != Default || n.Source() != SourceDefault {
		t.Fatal("nothing stored takes the default")
	}
}

func TestRenameOrderLabelsThenVariablesThenRemove(t *testing.T) {
	s, w, _, a := rig(t)
	w.scale = true
	ctx := context.Background()
	j, err := s.Plan(ctx, "crsp-lab", "luis")
	if err != nil {
		t.Fatal(err)
	}
	if j.Counts.Runners != 3 || j.Counts.Repositories != 2 || !j.Counts.ScaleSet || j.Counts.Ephemeral != 1 {
		t.Fatalf("counts %+v", j.Counts)
	}
	if got := s.cfg.Name.Get(); got != "crsp-home" {
		t.Fatalf("a plan changes nothing, name is %s", got)
	}
	if _, err := s.Apply(ctx, j.ID, "luis"); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(ctx, j.ID)
	if got.State != JobDone {
		t.Fatalf("state %s: %+v", got.State, got.Steps)
	}
	want := "add:r1 add:r2 add:e1 vars:1 vars:2 remove:r1 remove:r2 remove:e1 scale"
	if have := strings.Join(w.calls, " "); have != want {
		t.Fatalf("order\n have %s\n want %s", have, want)
	}
	for _, id := range []string{"r1", "r2", "e1"} {
		if !w.labels[id]["crsp-lab"] || w.labels[id]["crsp-home"] {
			t.Errorf("runner %s labels %v", id, w.labels[id])
		}
	}
	if w.vars[1] != "crsp-lab" || w.vars[2] != "crsp-lab" || w.scaleName != "crsp-lab" {
		t.Fatalf("vars %v scale %s", w.vars, w.scaleName)
	}
	if s.cfg.Name.Get() != "crsp-lab" || len(s.cfg.Name.Accepted()) != 1 {
		t.Fatalf("name %v", s.cfg.Name.Accepted())
	}
	if !strings.Contains(strings.Join(a.lines, "|"), "pool.renamed crsp-lab") || !strings.Contains(strings.Join(a.lines, "|"), "pool.label_added r1") {
		t.Fatalf("audit %v", a.lines)
	}
}

func TestOldLabelAcceptedUntilTheEnd(t *testing.T) {
	s, w, _, _ := rig(t)
	ctx := context.Background()
	j, _ := s.Plan(ctx, "crsp-lab", "luis")
	var during []string
	w.afterCall = func(call string) {
		if call == "vars:1" {
			during = s.cfg.Name.Accepted()
		}
	}
	if _, err := s.Apply(ctx, j.ID, "luis"); err != nil {
		t.Fatal(err)
	}
	if len(during) != 2 || during[0] != "crsp-lab" || during[1] != "crsp-home" {
		t.Fatalf("both names mean the pool while it runs: %v", during)
	}
}

func TestStopThenResume(t *testing.T) {
	s, w, _, _ := rig(t)
	ctx := context.Background()
	j, _ := s.Plan(ctx, "crsp-lab", "luis")
	w.afterCall = func(call string) {
		if call == "add:r2" {
			_, _ = s.Stop(ctx, j.ID, "luis")
		}
	}
	if _, err := s.Apply(ctx, j.ID, "luis"); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(ctx, j.ID)
	if got.State != JobStopped {
		t.Fatalf("state %s", got.State)
	}
	done := 0
	for _, st := range got.Steps {
		if st.State == StepDone {
			done++
		}
	}
	if done != 2 || got.Steps[2].State != StepSkipped {
		t.Fatalf("the step that ran finished and none after it started: %+v", got.Steps)
	}
	if s.cfg.Name.Get() != "crsp-home" {
		t.Fatal("the name has not been stored yet")
	}
	w.afterCall = nil
	w.calls = nil
	// Run again plans the same name: it resumes the same job.
	again, err := s.Plan(ctx, "crsp-lab", "luis")
	if err != nil || again.ID != j.ID {
		t.Fatalf("resume %v %v", err, again.ID)
	}
	if _, err := s.Apply(ctx, again.ID, "luis"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get(ctx, j.ID); got.State != JobDone {
		t.Fatalf("state %s", got.State)
	}
	if strings.Contains(strings.Join(w.calls, " "), "add:r1") || strings.Contains(strings.Join(w.calls, " "), "add:r2") {
		t.Fatalf("a step done is not repeated: %v", w.calls)
	}
}

func TestFailureMidwayShowsWhatIsLeftAndResumes(t *testing.T) {
	s, w, _, a := rig(t)
	ctx := context.Background()
	j, _ := s.Plan(ctx, "crsp-lab", "luis")
	w.failOn = "vars:2"
	if _, err := s.Apply(ctx, j.ID, "luis"); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(ctx, j.ID)
	if got.State != JobFailed {
		t.Fatalf("state %s", got.State)
	}
	var failed, skipped int
	for _, st := range got.Steps {
		switch st.State {
		case StepFailed:
			failed++
			if st.Kind != KindVariables || st.Error == "" {
				t.Fatalf("failed step %+v", st)
			}
		case StepSkipped:
			skipped++
		}
	}
	if failed != 1 || skipped < 4 {
		t.Fatalf("failed %d skipped %d", failed, skipped)
	}
	// Nothing was stranded: the old label is still on every runner.
	for _, id := range []string{"r1", "r2"} {
		if !w.labels[id]["crsp-home"] || !w.labels[id]["crsp-lab"] {
			t.Fatalf("runner %s should carry both labels: %v", id, w.labels[id])
		}
	}
	if !strings.Contains(strings.Join(a.lines, "|"), "pool.rename_failed") {
		t.Fatalf("audit %v", a.lines)
	}
	if _, err := s.Apply(ctx, j.ID, "luis"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get(ctx, j.ID); got.State != JobDone {
		t.Fatalf("run again finishes it: %s", got.State)
	}
	if w.labels["r1"]["crsp-home"] {
		t.Fatal("the old label is gone at the end")
	}
}

func TestRerunIsIdempotentAndSameNameRefused(t *testing.T) {
	s, w, _, _ := rig(t)
	ctx := context.Background()
	j, _ := s.Plan(ctx, "crsp-lab", "luis")
	if _, err := s.Apply(ctx, j.ID, "luis"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Apply(ctx, j.ID, "luis"); !errors.Is(err, ErrNotPlanned) {
		t.Fatalf("a finished rename is not run twice: %v", err)
	}
	if _, err := s.Plan(ctx, "crsp-lab", "luis"); err == nil {
		t.Fatal("renaming to the name in force is refused")
	}
	n := len(w.calls)
	// A second rename back works and leaves the labels right.
	j2, err := s.Plan(ctx, "crsp-home", "luis")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Apply(ctx, j2.ID, "luis"); err != nil {
		t.Fatal(err)
	}
	if len(w.calls) <= n || !w.labels["r1"]["crsp-home"] || w.labels["r1"]["crsp-lab"] {
		t.Fatalf("calls %v labels %v", w.calls, w.labels["r1"])
	}
}

func TestPlanRefusesBadNameAndEnvOverride(t *testing.T) {
	s, _, _, _ := rig(t)
	if _, err := s.Plan(context.Background(), "Bad", "luis"); err == nil {
		t.Fatal("bad name accepted")
	}
	s.cfg.Name.source = SourceEnv
	if _, err := s.Plan(context.Background(), "crsp-lab", "luis"); err == nil || !strings.Contains(err.Error(), EnvName) {
		t.Fatalf("env override should be named: %v", err)
	}
}

func TestStopWhenNotRunningIsRefused(t *testing.T) {
	s, _, _, _ := rig(t)
	ctx := context.Background()
	j, _ := s.Plan(ctx, "crsp-lab", "luis")
	if _, err := s.Stop(ctx, j.ID, "luis"); !errors.Is(err, ErrNotApplying) {
		t.Fatalf("%v", err)
	}
}

func TestSetFirstOnlyWhenNothingUsesTheLabel(t *testing.T) {
	s, w, db, _ := rig(t)
	ctx := context.Background()
	if _, err := s.SetFirst(ctx, "Bad", "luis"); err == nil {
		t.Fatal("bad name")
	}
	if _, err := s.SetFirst(ctx, "crsp-lab", "luis"); err == nil {
		t.Fatal("runners carry crsp-home: first run must not rename them")
	}
	w.labels = map[string]map[string]bool{}
	w.noRepos = true
	info, err := s.SetFirst(ctx, "crsp-lab", "luis")
	if err != nil || info.Name != "crsp-lab" || info.Source != SourceStored {
		t.Fatalf("%+v %v", info, err)
	}
	if raw, _, _ := db.RawSetting(ctx, nameKey); raw != `"crsp-lab"` {
		t.Fatalf("stored %q", raw)
	}
	if s.cfg.Name.Get() != "crsp-lab" || len(s.cfg.Name.Accepted()) != 1 {
		t.Fatal("name in force")
	}
}

func TestRestartKeepsTheOldNameWhileARenameIsUnfinished(t *testing.T) {
	s, w, db, _ := rig(t)
	ctx := context.Background()
	j, _ := s.Plan(ctx, "crsp-lab", "luis")
	w.afterCall = func(call string) {
		if call == "vars:1" {
			_, _ = s.Stop(ctx, j.ID, "luis")
		}
	}
	_, _ = s.Apply(ctx, j.ID, "luis")
	n, _ := Resolve(ctx, db)
	_ = New(Config{Name: n, KV: db, Runners: w, Repos: w})
	if a := n.Accepted(); len(a) != 2 || a[0] != "crsp-lab" || a[1] != "crsp-home" {
		t.Fatalf("after a restart both names mean the pool: %v", a)
	}
}
