package windows

import (
	"strings"
	"testing"
)

// OPS92: `powershell -Command -` runs a multi-line statement only when a blank
// line follows it, so the stdin must end with one and hold none inside.
func TestPSStdinEndsWithBlankLine(t *testing.T) {
	r := newRig(t)
	if _, err := r.b.ps(t.Context(), scriptLock, nil, nil); err != nil {
		t.Fatal(err)
	}
	in := r.cmd.Calls[0].Stdin
	if !strings.HasSuffix(in, "}\n\n") || strings.HasSuffix(in, "\n\n\n") {
		t.Errorf("stdin must end with one blank line, got tail %q", in[max(0, len(in)-12):])
	}
}

func TestWrapScriptHasNoInnerBlankLine(t *testing.T) {
	for name, s := range map[string]string{"account": scriptAccount, "rights": scriptRights, "logon": scriptCheckLogon, "lock": scriptLock} {
		w := wrapScript(s)
		body := strings.TrimSuffix(w, "\n\n")
		if strings.Contains(body, "\n\n") || strings.Contains(body, "\n \n") {
			t.Errorf("%s: blank line inside the wrapped script", name)
		}
		if !strings.HasSuffix(w, "\n\n") {
			t.Errorf("%s: no closing blank line", name)
		}
	}
}
