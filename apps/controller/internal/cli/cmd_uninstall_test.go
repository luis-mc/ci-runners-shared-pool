package cli

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/uninstall"
)

// recSteps records the effects of a remove and calls the runner hook like the real host does.
type recSteps struct {
	runners func(ctx context.Context) (int, error)
	calls   []string
	audits  []uninstall.Record
}

func (r *recSteps) UnregisterRunners(ctx context.Context) (int, error) {
	r.calls = append(r.calls, "runners")
	return r.runners(ctx)
}
func (r *recSteps) StopServices(context.Context) error { r.calls = append(r.calls, "stop"); return nil }
func (r *recSteps) RemoveContainers(context.Context) (int, error) {
	r.calls = append(r.calls, "containers")
	return 0, nil
}
func (r *recSteps) RemoveImages(context.Context) (int, error) {
	r.calls = append(r.calls, "images")
	return 0, nil
}
func (r *recSteps) UnregisterWSL(context.Context) error { r.calls = append(r.calls, "wsl"); return nil }
func (r *recSteps) DeleteData(context.Context) error    { r.calls = append(r.calls, "data"); return nil }
func (r *recSteps) Audit(_ context.Context, rec uninstall.Record) error {
	r.audits = append(r.audits, rec)
	return nil
}

type uninstallRun struct {
	steps    *recSteps
	window   int
	windowEr error
	program  []bool
}

func (f *fixture) runUninstall(u *uninstallRun, env map[string]string, args ...string) (int, string) {
	var out, errOut bytes.Buffer
	e := f.env(&out, &errOut)
	if env != nil {
		e.Getenv = func(k string) string { return env[k] }
	}
	u.steps = &recSteps{}
	e.Uninstall = &UninstallHooks{
		Steps: func(runners func(ctx context.Context) (int, error)) uninstall.Steps {
			u.steps.runners = runners
			return u.steps
		},
		Window: func(context.Context) (int, error) { return u.window, u.windowEr },
		RemoveProgram: func(_ context.Context, del bool) error {
			u.program = append(u.program, del)
			return nil
		},
	}
	code := Run(e, args)
	return code, errOut.String()
}

func (f *fixture) oneRunner() {
	f.reply("GET /api/v1/runners", 200, map[string]any{"items": []any{runnerJSON("linux-03", "m_build-01", nil)}})
	f.reply("DELETE /api/v1/runners/r_linux-03", 202, runnerJSON("linux-03", "m_build-01", nil))
	var gets atomic.Int32
	f.handle("GET /api/v1/runners/r_linux-03", func(w http.ResponseWriter, r *http.Request) {
		if gets.Add(1) > 1 {
			writeJSON(w, 404, apiErr("not_found", "No such runner."))
			return
		}
		writeJSON(w, 200, runnerJSON("linux-03", "m_build-01", nil))
	})
}

func TestUninstallSilentKeepsDataAndUnregistersRunners(t *testing.T) {
	f := newFixture(t)
	f.oneRunner()
	u := &uninstallRun{}
	code, e := f.runUninstall(u, nil, "uninstall", "--yes")
	if code != 0 || !strings.Contains(e, "crsp is removed.") {
		t.Fatalf("%d %q", code, e)
	}
	if strings.Contains(strings.Join(u.steps.calls, ","), "data") || !f.called("DELETE /api/v1/runners/r_linux-03?confirm=true") {
		t.Fatalf("calls %v %v", u.steps.calls, f.calls)
	}
	if len(u.program) != 1 || u.program[0] {
		t.Fatalf("the program is removed and the data is kept: %v", u.program)
	}
	if last := u.steps.audits[len(u.steps.audits)-1]; last.Result != uninstall.ResultRemoved || last.DeleteData || last.Actor == "" {
		t.Fatalf("audit %+v", last)
	}
}

func TestUninstallDeleteDataFlagAndPurge(t *testing.T) {
	for name, tc := range map[string]struct {
		env  map[string]string
		args []string
	}{
		"flag":  {nil, []string{"uninstall", "--yes", "--delete-data"}},
		"PURGE": {map[string]string{"PURGE": "1"}, []string{"uninstall", "--yes"}},
		"alias": {nil, []string{"remove", "--yes", "--delete-data"}},
	} {
		f := newFixture(t)
		f.oneRunner()
		u := &uninstallRun{}
		code, e := f.runUninstall(u, tc.env, tc.args...)
		if code != 0 {
			t.Fatalf("%s: %d %q", name, code, e)
		}
		got := strings.Join(u.steps.calls, ",")
		if !strings.Contains(got, "runners") || !strings.Contains(got, "containers,images,wsl") || !strings.HasSuffix(got, "data") {
			t.Fatalf("%s: calls %s", name, got)
		}
		if len(u.program) != 1 || !u.program[0] {
			t.Fatalf("%s: the program step must pass PURGE through: %v", name, u.program)
		}
	}
}

func TestUninstallNoTerminalNoFlagsKeepsDataButNeedsYes(t *testing.T) {
	f := newFixture(t)
	f.oneRunner()
	u := &uninstallRun{}
	code, e := f.runUninstall(u, nil, "uninstall")
	if code != ExitNotConfirmed || !strings.Contains(e, "Nothing changed") || len(u.steps.calls) != 0 {
		t.Fatalf("%d %q %v", code, e, u.steps.calls)
	}
}

func TestUninstallWindowAnswers(t *testing.T) {
	cases := []struct {
		name    string
		code    int
		wantDel bool
		wantErr bool
	}{
		{"keep (10)", uninstall.WindowExitKeep, false, false},
		{"delete (11)", uninstall.WindowExitDelete, true, false},
		{"cancel (1)", uninstall.WindowExitCancel, false, true},
	}
	for _, c := range cases {
		f := newFixture(t)
		f.oneRunner()
		f.terminal = true
		f.stdin = "yes\n"
		u := &uninstallRun{window: c.code}
		code, e := f.runUninstall(u, nil, "uninstall")
		if c.wantErr {
			if code == 0 || len(u.steps.calls) != 0 || !strings.Contains(e, "Cancelled") {
				t.Fatalf("%s: %d %q %v", c.name, code, e, u.steps.calls)
			}
			continue
		}
		if code != 0 {
			t.Fatalf("%s: %d %q", c.name, code, e)
		}
		if deleted := strings.Contains(strings.Join(u.steps.calls, ","), "data"); deleted != c.wantDel {
			t.Fatalf("%s: delete = %v, calls %v", c.name, deleted, u.steps.calls)
		}
	}
}

func TestUninstallWithoutWindowAsksInTheTerminalWithNoDefault(t *testing.T) {
	f := newFixture(t)
	f.oneRunner()
	f.terminal = true
	f.stdin = "\nyes\n" // an empty answer is cancel, never a default
	u := &uninstallRun{windowEr: errors.New("no desktop")}
	code, e := f.runUninstall(u, nil, "uninstall")
	if code == 0 || len(u.steps.calls) != 0 || !strings.Contains(e, "Keep my data or delete my data too?") {
		t.Fatalf("%d %q %v", code, e, u.steps.calls)
	}
	f = newFixture(t)
	f.oneRunner()
	f.terminal = true
	f.stdin = "delete\nyes\n"
	u = &uninstallRun{windowEr: errors.New("no desktop")}
	if code, e := f.runUninstall(u, nil, "uninstall"); code != 0 || !strings.HasSuffix(strings.Join(u.steps.calls, ","), "data") {
		t.Fatalf("%d %q %v", code, e, u.steps.calls)
	}
}

func TestUninstallBothFlagsRefused(t *testing.T) {
	f := newFixture(t)
	u := &uninstallRun{}
	if code, _ := f.runUninstall(u, nil, "uninstall", "--yes", "--delete-data", "--keep-data"); code != ExitUsage {
		t.Fatalf("code %d", code)
	}
}

func TestUninstallNotSignedInWarnsAndGoesOn(t *testing.T) {
	f := newFixture(t)
	if err := os.Remove(filepath.Join(f.dir, "cli.toml")); err != nil {
		t.Skip("the config file name differs: " + err.Error())
	}
	u := &uninstallRun{}
	code, e := f.runUninstall(u, nil, "uninstall", "--yes", "--keep-data")
	if code != 0 || !strings.Contains(e, "stay registered in GitHub") {
		t.Fatalf("%d %q", code, e)
	}
	if last := u.steps.audits[len(u.steps.audits)-1]; last.Result != uninstall.ResultWarnings {
		t.Fatalf("audit %+v", last)
	}
}

func TestUninstallAskToWritesTheAnswerAndChangesNothing(t *testing.T) {
	f := newFixture(t)
	u := &uninstallRun{window: uninstall.WindowExitDelete}
	file := filepath.Join(t.TempDir(), "choice")
	code, e := f.runUninstall(u, nil, "uninstall", "--ask-to", file)
	if code != 0 {
		t.Fatalf("%d %q", code, e)
	}
	b, _ := os.ReadFile(file)
	if strings.TrimSpace(string(b)) != "delete" || len(u.steps.calls) != 0 {
		t.Fatalf("answer %q calls %v", b, u.steps.calls)
	}
	// Cancel in the window: exit non-zero, no answer file.
	u = &uninstallRun{window: uninstall.WindowExitCancel}
	file2 := filepath.Join(t.TempDir(), "choice")
	if code, _ := f.runUninstall(u, nil, "uninstall", "--ask-to", file2); code == 0 {
		t.Fatal("cancel must fail the installer step")
	}
	if _, err := os.Stat(file2); err == nil {
		t.Fatal("no answer on cancel")
	}
}

func TestUninstallFromInstallerReadsTheChoiceFileAndLeavesTheProgram(t *testing.T) {
	dir := t.TempDir()
	del := filepath.Join(dir, "delete")
	keep := filepath.Join(dir, "keep")
	_ = os.WriteFile(del, []byte("delete\n"), 0o644)
	_ = os.WriteFile(keep, []byte("keep\n"), 0o644)
	for name, tc := range map[string]struct {
		file string
		env  map[string]string
		want bool
	}{
		"file delete":        {del, nil, true},
		"file keep":          {keep, nil, false},
		"no file keeps":      {filepath.Join(dir, "none"), nil, false},
		"PURGE beats a keep": {keep, map[string]string{"PURGE": "1"}, true},
	} {
		f := newFixture(t)
		u := &uninstallRun{}
		code, e := f.runUninstall(u, tc.env, "uninstall", "--from-installer", "--yes", "--choice-file", tc.file)
		if code != 0 {
			t.Fatalf("%s: %d %q", name, code, e)
		}
		if deleted := strings.HasSuffix(strings.Join(u.steps.calls, ","), "data"); deleted != tc.want {
			t.Fatalf("%s: delete = %v, calls %v", name, deleted, u.steps.calls)
		}
		if len(u.program) != 0 {
			t.Fatalf("%s: the installer removes the program, not the CLI", name)
		}
	}
}

func TestUninstallRunnersOnly(t *testing.T) {
	f := newFixture(t)
	f.oneRunner()
	u := &uninstallRun{}
	code, e := f.runUninstall(u, nil, "uninstall", "--runners-only")
	if code != 0 || !strings.Contains(e, "Unregistered 1 runner") || !f.called("DELETE /api/v1/runners/r_linux-03") {
		t.Fatalf("%d %q %v", code, e, f.calls)
	}
}
