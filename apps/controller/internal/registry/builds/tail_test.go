package builds

import (
	"strconv"
	"strings"
	"testing"
)

func TestTailRedactsAndCaps(t *testing.T) {
	tl := NewTail(func(s string) string { return strings.ReplaceAll(s, "s3cret", "***") })
	for i := 0; i < 200; i++ {
		tl.Add("line " + strconv.Itoa(i) + " token s3cret")
	}
	out := tl.String()
	if strings.Contains(out, "s3cret") {
		t.Fatal("secret in the tail")
	}
	lines := strings.Split(out, "\n")
	if len(lines) != tailLines || !strings.HasPrefix(lines[len(lines)-1], "line 199") {
		t.Fatalf("%d lines, last %q", len(lines), lines[len(lines)-1])
	}
	big := NewTail(nil)
	for i := 0; i < 70; i++ {
		big.Add(strings.Repeat("x", 300))
	}
	if len(big.String()) > tailBytes {
		t.Fatalf("tail is %d bytes", len(big.String()))
	}
}

func TestClassify(t *testing.T) {
	for _, c := range []struct{ log, want string }{
		{"write /x: no space left on device", ClassDisk},
		{"E: Failed to fetch http://archive\nTemporary failure in name resolution", ClassNetwork},
		{"Hash Sum mismatch", ClassChecksum},
		{"context deadline exceeded", ClassTimeout},
		{"E: Unable to locate package foo", ClassApt},
		{"RUN apt-get install -y x\nsomething odd", ClassOther},
	} {
		if got, why := Classify(c.log); got != c.want || why == "" {
			t.Errorf("%q: %q (%q), want %q", c.log, got, why, c.want)
		}
	}
}
