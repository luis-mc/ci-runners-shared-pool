package uninstall

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

const code = "{11111111-2222-3333-4444-555555555555}"

type fakeStart struct {
	name string
	args []string
	n    int
	err  error
}

func (f *fakeStart) start(_ context.Context, name string, args ...string) error {
	f.name, f.args, f.n = name, args, f.n+1
	return f.err
}

func TestMSIExecArgsPurge(t *testing.T) {
	got := MSIExecArgs(code, true, "")
	want := []string{"/x", code, "/qn", "/norestart", "PURGE=1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%v", got)
	}
	if k := MSIExecArgs(code, false, `C:\l.log`); !reflect.DeepEqual(k, []string{"/x", code, "/qn", "/norestart", "/l*v", `C:\l.log`, "PURGE=0"}) {
		t.Fatalf("%v", k)
	}
}

func TestHandoffStartsMsiexecOnce(t *testing.T) {
	f := &fakeStart{}
	h := MSIHandoff{SystemRoot: `C:\Windows`, ProductCode: func(context.Context) (string, error) { return code + "\r\n", nil }, Start: f.start}
	if err := h.Remove(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if f.n != 1 || !strings.HasSuffix(f.name, "msiexec.exe") || !strings.Contains(f.name, "System32") || f.args[0] != "/x" || f.args[1] != code || f.args[len(f.args)-1] != "PURGE=1" {
		t.Fatalf("%q %v", f.name, f.args)
	}
}

func TestHandoffKeepIsPurge0(t *testing.T) {
	f := &fakeStart{}
	h := MSIHandoff{ProductCode: func(context.Context) (string, error) { return code, nil }, Start: f.start}
	if err := h.Remove(context.Background(), false); err != nil || f.args[len(f.args)-1] != "PURGE=0" {
		t.Fatalf("%v %v", err, f.args)
	}
}

func TestHandoffRefusesWhatIsNotAGUID(t *testing.T) {
	for _, bad := range []string{"", "crsp", "{1}", code + " & calc", "{1111111g-2222-3333-4444-555555555555}"} {
		f := &fakeStart{}
		h := MSIHandoff{ProductCode: func(context.Context) (string, error) { return bad, nil }, Start: f.start}
		if err := h.Remove(context.Background(), true); err == nil || f.n != 0 {
			t.Fatalf("%q accepted", bad)
		}
	}
}

func TestHandoffErrorsPass(t *testing.T) {
	h := MSIHandoff{ProductCode: func(context.Context) (string, error) { return "", errors.New("nothing to remove") }, Start: (&fakeStart{}).start}
	if err := h.Remove(context.Background(), true); err == nil || !strings.Contains(err.Error(), "nothing to remove") {
		t.Fatal(err)
	}
	h = MSIHandoff{ProductCode: func(context.Context) (string, error) { return code, nil }, Start: (&fakeStart{err: errors.New("boom")}).start}
	if err := h.Remove(context.Background(), true); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatal(err)
	}
}
