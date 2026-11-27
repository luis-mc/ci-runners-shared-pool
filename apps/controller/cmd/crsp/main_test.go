package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunVersion(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"version"}, &out, &errOut); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	if !strings.HasPrefix(out.String(), "crsp ") {
		t.Fatalf("output %q", out.String())
	}
}

func TestRunUsageErrors(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run(nil, &out, &errOut); code != 2 {
		t.Fatalf("no args: exit %d", code)
	}
	errOut.Reset()
	if code := run([]string{"bogus"}, &out, &errOut); code != 2 || !strings.Contains(errOut.String(), "unknown command") {
		t.Fatalf("bogus: exit %d, %q", code, errOut.String())
	}
}

func TestRunAgentNeedsACommand(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"agent"}, &out, &errOut); code != 1 {
		t.Fatalf("agent: exit %d", code)
	}
}

func TestRunVersionJSON(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"version", "--json"}, &out, &errOut); code != 0 || !strings.Contains(out.String(), `"version"`) || !strings.Contains(out.String(), `"go"`) {
		t.Fatalf("exit %d: %q %s", code, out.String(), errOut.String())
	}
}
