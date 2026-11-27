package output_test

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/cli/output"
)

func TestTableAndFormats(t *testing.T) {
	var b bytes.Buffer
	output.Table(&b, 80, []string{"NAME", "N"}, [][]string{{"alpha", "1"}, {"b", "22"}})
	want := "NAME   N\nalpha  1\nb      22\n"
	if b.String() != want {
		t.Fatalf("%q", b.String())
	}
	if output.Number(12345) != "12 345" || output.Number(-1234567) != "-1 234 567" || output.Number(7) != "7" {
		t.Error("number format")
	}
	p := float32(44.6)
	if output.Percent(&p) != "45%" || output.Percent(nil) != "-" {
		t.Error("percent")
	}
	ts := time.Date(2026, 11, 2, 14, 30, 59, 0, time.UTC)
	if output.Time(&ts) != "2026-11-02 14:30" || output.Time(nil) != "-" {
		t.Error("time")
	}
}

func TestFits80Columns(t *testing.T) {
	var b bytes.Buffer
	long := strings.Repeat("x", 120)
	output.Table(&b, 80, []string{"REPOSITORY", "POOL", "LABELS", "MAX"}, [][]string{{"luis-mc/" + long, "on", "self-hosted,linux,x64", "2"}})
	for _, l := range strings.Split(strings.TrimRight(b.String(), "\n"), "\n") {
		if n := len([]rune(l)); n > 80 {
			t.Errorf("%d columns: %q", n, l)
		}
	}
	if !strings.Contains(b.String(), "…") {
		t.Error("no ellipsis")
	}
}

func TestNoColor(t *testing.T) {
	var b bytes.Buffer
	output.Table(&b, 80, []string{"A"}, [][]string{{"x"}})
	if strings.Contains(b.String(), "\x1b[") {
		t.Fatal("colour in table output")
	}
}

func TestJSONGolden(t *testing.T) {
	var b bytes.Buffer
	if err := output.JSON(&b, map[string]any{"items": []int{1}}); err != nil {
		t.Fatal(err)
	}
	if b.String() != "{\n  \"items\": [\n    1\n  ]\n}\n" {
		t.Fatalf("%q", b.String())
	}
	b.Reset()
	_ = output.Line(&b, map[string]int{"a": 1})
	if b.String() != "{\"a\":1}\n" {
		t.Fatalf("%q", b.String())
	}
}
