package uninstall

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fake struct {
	calls     []string
	failAudit int // fail the Nth audit row (1-based), 0 never
	audits    []Record
	runnersEr error
	dataErr   error
}

func (f *fake) rec(s string) { f.calls = append(f.calls, s) }
func (f *fake) UnregisterRunners(context.Context) (int, error) {
	f.rec("runners")
	return 3, f.runnersEr
}
func (f *fake) StopServices(context.Context) error            { f.rec("stop"); return nil }
func (f *fake) RemoveContainers(context.Context) (int, error) { f.rec("containers"); return 2, nil }
func (f *fake) RemoveImages(context.Context) (int, error)     { f.rec("images"); return 1, nil }
func (f *fake) UnregisterWSL(context.Context) error           { f.rec("wsl"); return nil }
func (f *fake) DeleteData(context.Context) error              { f.rec("data"); return f.dataErr }
func (f *fake) Audit(_ context.Context, r Record) error {
	f.audits = append(f.audits, r)
	f.rec("audit:" + r.Result)
	if f.failAudit == len(f.audits) {
		return errors.New("disk full")
	}
	return nil
}

func opts(del bool) Options {
	return Options{DeleteData: del, Actor: "owner", Now: func() time.Time { return time.Unix(1000, 0) }}
}

func TestKeepUnregistersRunnersAndKeepsData(t *testing.T) {
	f := &fake{}
	sum, err := Run(context.Background(), opts(false), f, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(f.calls, ","); got != "audit:started,runners,stop,audit:removed" {
		t.Fatalf("calls = %s", got)
	}
	if sum.RunnersRemoved != 3 || sum.DeleteData {
		t.Fatalf("summary = %+v", sum)
	}
}

func TestDeleteRemovesEverythingAndAuditsBeforeTheDataGoes(t *testing.T) {
	f := &fake{}
	if _, err := Run(context.Background(), opts(true), f, nil); err != nil {
		t.Fatal(err)
	}
	want := "audit:started,runners,stop,containers,images,wsl,audit:removed,data"
	if got := strings.Join(f.calls, ","); got != want {
		t.Fatalf("calls = %s\nwant    %s", got, want)
	}
	last := f.audits[len(f.audits)-1]
	if last.Actor != "owner" || last.Action != "remove" || !last.DeleteData || last.Result != ResultRemoved {
		t.Fatalf("audit = %+v", last)
	}
}

func TestAuditFailsClosed(t *testing.T) {
	f := &fake{failAudit: 1}
	if _, err := Run(context.Background(), opts(true), f, nil); err == nil {
		t.Fatal("want an error when the audit log takes nothing")
	}
	if got := strings.Join(f.calls, ","); got != "audit:started" {
		t.Fatalf("nothing may change after a refused audit row: %s", got)
	}
}

func TestRunnerFailureIsAWarningAndTheRemoveGoesOn(t *testing.T) {
	f := &fake{runnersEr: errors.New("GitHub said no")}
	sum, err := Run(context.Background(), opts(true), f, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(sum.Warnings) != 1 || f.audits[len(f.audits)-1].Result != ResultWarnings {
		t.Fatalf("warnings %v audits %+v", sum.Warnings, f.audits)
	}
	if !strings.Contains(strings.Join(f.calls, ","), "data") {
		t.Fatal("data must still be deleted when asked")
	}
}

func TestDataDeleteFailureIsAnErrorAndAudited(t *testing.T) {
	f := &fake{dataErr: errors.New("in use")}
	if _, err := Run(context.Background(), opts(true), f, nil); err == nil {
		t.Fatal("want an error")
	}
	if f.audits[len(f.audits)-1].Result != ResultFailed {
		t.Fatalf("audits %+v", f.audits)
	}
}

func TestChoiceFromWindowExit(t *testing.T) {
	for code, want := range map[int]Choice{10: ChoiceKeep, 11: ChoiceDelete, 1: ChoiceCancel} {
		got, err := ChoiceFromWindowExit(code)
		if err != nil || got != want {
			t.Fatalf("code %d: %v %v", code, got, err)
		}
	}
	if _, err := ChoiceFromWindowExit(0); err == nil {
		t.Fatal("exit 0 is not an answer")
	}
	if _, err := ChoiceFromWindowExit(3); err == nil {
		t.Fatal("exit 3 is not an answer")
	}
}

func TestAsk(t *testing.T) {
	win := func(code int) func() (int, error) { return func() (int, error) { return code, nil } }
	noWin := func() (int, error) { return 0, errors.New("no desktop") }
	term := func(c Choice) func() (Choice, error) { return func() (Choice, error) { return c, nil } }
	cases := []struct {
		name                          string
		del, keep, purge, interactive bool
		w                             func() (int, error)
		tm                            func() (Choice, error)
		want                          Choice
		err                           error
	}{
		{"flag deletes", true, false, false, true, win(10), nil, ChoiceDelete, nil},
		{"PURGE=1 deletes", false, false, true, true, win(10), nil, ChoiceDelete, nil},
		{"keep flag keeps", false, true, false, true, win(11), nil, ChoiceKeep, nil},
		{"silent keeps", false, false, false, false, nil, nil, ChoiceKeep, nil},
		{"window keep", false, false, false, true, win(10), nil, ChoiceKeep, nil},
		{"window delete", false, false, false, true, win(11), nil, ChoiceDelete, nil},
		{"window cancel", false, false, false, true, win(1), nil, ChoiceCancel, ErrCancelled},
		{"no window, terminal delete", false, false, false, true, noWin, term(ChoiceDelete), ChoiceDelete, nil},
		{"no window, terminal cancel", false, false, false, true, noWin, term(ChoiceCancel), ChoiceCancel, ErrCancelled},
	}
	for _, c := range cases {
		got, err := Ask(c.del, c.keep, c.purge, c.interactive, c.w, c.tm)
		if got != c.want || !errors.Is(err, c.err) {
			t.Errorf("%s: got %v %v, want %v %v", c.name, got, err, c.want, c.err)
		}
	}
	if _, err := Ask(false, false, false, true, noWin, nil); err == nil {
		t.Error("nobody to ask must be an error, never a guess")
	}
	if _, err := Ask(false, false, false, true, win(7), nil); err == nil {
		t.Error("a window answer that is not 10, 11 or 1 must be an error")
	}
}

func TestPurgeFromEnv(t *testing.T) {
	env := func(v string) func(string) string { return func(string) string { return v } }
	for v, want := range map[string]bool{"1": true, "true": true, "0": false, "": false, "no": false} {
		if PurgeFromEnv(env(v)) != want {
			t.Errorf("PURGE=%q should be %v", v, want)
		}
	}
}

func TestSafeDataDir(t *testing.T) {
	for _, bad := range []string{"", "/", `C:\`, `C:\ProgramData`, "/var/lib", "/var/lib/other", `C:\ProgramData\Other`} {
		if SafeDataDir(bad) == nil {
			t.Errorf("%q must be refused", bad)
		}
	}
	for _, ok := range []string{`C:\ProgramData\crsp`, `C:\ProgramData\crsp-agent`, "/var/lib/crsp", "/Library/Application Support/crsp"} {
		if err := SafeDataDir(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
}

// recorder is an Exec that records calls and answers from a table.
type recorder struct {
	calls   []string
	answers map[string]struct {
		out string
		err error
	}
}

func (r *recorder) run(_ context.Context, name string, args ...string) ([]byte, error) {
	key := name + " " + strings.Join(args, " ")
	r.calls = append(r.calls, key)
	for prefix, a := range r.answers {
		if strings.HasPrefix(key, prefix) {
			return []byte(a.out), a.err
		}
	}
	return nil, nil
}

func TestHostDockerAndWSL(t *testing.T) {
	r := &recorder{answers: map[string]struct {
		out string
		err error
	}{
		"docker ps -aq --filter label=crsp.image-key": {out: "aaa\nbbb\n"},
		"docker ps -aq --filter name=^crsp-":          {out: "bbb\nccc\n"},
		"docker image ls":                             {out: "img1\n"},
	}}
	h := &Host{GOOS: "windows", Exec: r.run}
	n, err := h.RemoveContainers(context.Background())
	if err != nil || n != 3 {
		t.Fatalf("containers %d %v (a container found by two filters counts once)", n, err)
	}
	if n, err = h.RemoveImages(context.Background()); err != nil || n != 1 {
		t.Fatalf("images %d %v", n, err)
	}
	if err = h.UnregisterWSL(context.Background()); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(r.calls, "\n")
	if !strings.Contains(joined, "wsl.exe --unregister crsp") || !strings.Contains(joined, "docker rm -f -v") || !strings.Contains(joined, "docker image rm -f img1") {
		t.Fatalf("calls:\n%s", joined)
	}
}

func TestHostNoDockerNoWSLIsFine(t *testing.T) {
	notThere := func(context.Context, string, ...string) ([]byte, error) { return nil, ErrNotInstalled }
	h := &Host{GOOS: "windows", Exec: notThere}
	if n, err := h.RemoveContainers(context.Background()); n != 0 || err != nil {
		t.Fatal(n, err)
	}
	if n, err := h.RemoveImages(context.Background()); n != 0 || err != nil {
		t.Fatal(n, err)
	}
	if err := h.UnregisterWSL(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := h.StopServices(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestWSLNoSuchDistroIsFineOtherErrorsAreNot(t *testing.T) {
	// wsl.exe prints UTF-16, so the text arrives with NUL bytes between the letters.
	utf16 := func(s string) string { return strings.Join(strings.Split(s, ""), "\x00") }
	missing := &recorder{answers: map[string]struct {
		out string
		err error
	}{"wsl.exe": {out: utf16("There is no distribution with the supplied name."), err: errors.New("exit 1")}}}
	if err := (&Host{GOOS: "windows", Exec: missing.run}).UnregisterWSL(context.Background()); err != nil {
		t.Fatal(err)
	}
	broken := &recorder{answers: map[string]struct {
		out string
		err error
	}{"wsl.exe": {out: "access denied", err: errors.New("exit 1")}}}
	if err := (&Host{GOOS: "windows", Exec: broken.run}).UnregisterWSL(context.Background()); err == nil {
		t.Fatal("a real failure must be reported")
	}
	before := len(broken.calls)
	if err := (&Host{GOOS: "linux", Exec: broken.run}).UnregisterWSL(context.Background()); err != nil || len(broken.calls) != before {
		t.Fatal("WSL is a Windows step only")
	}
}

func TestHostDeleteDataAndAuditFile(t *testing.T) {
	root := t.TempDir()
	data := filepath.Join(root, "crsp")
	agent := filepath.Join(root, "crsp-agent")
	other := filepath.Join(root, "keepme")
	for _, d := range []string{data, agent, other} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "f"), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var logged []string
	h := &Host{GOOS: "linux", DataDirs: []string{data, agent}, SystemLog: func(_ context.Context, l string) error { logged = append(logged, l); return nil }}
	if err := h.Audit(context.Background(), Record{Actor: "owner", Action: "remove", Result: ResultRemoved}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(data, "logs", "remove-audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var rec Record
	if err := json.Unmarshal(b, &rec); err != nil || rec.Actor != "owner" || rec.Result != ResultRemoved {
		t.Fatalf("audit file %q %v", b, err)
	}
	if len(logged) != 1 || !strings.Contains(logged[0], `"actor":"owner"`) {
		t.Fatalf("system log %v", logged)
	}
	if err := h.DeleteData(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, gone := range []string{data, agent} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Errorf("%s must be gone", gone)
		}
	}
	if _, err := os.Stat(other); err != nil {
		t.Error("a folder that is not crsp's must stay")
	}
	// A folder that is not crsp's is refused, not deleted.
	if err := (&Host{DataDirs: []string{other}}).DeleteData(context.Background()); err == nil {
		t.Error("want a refusal")
	}
}

func TestAuditFailsWhenNoSinkTakesTheRow(t *testing.T) {
	h := &Host{DataDirs: []string{"/"}, SystemLog: func(context.Context, string) error { return errors.New("no log") }}
	if err := h.Audit(context.Background(), Record{Action: "remove"}); err == nil {
		t.Fatal("want an error")
	}
}
