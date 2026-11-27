package container

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// The job log words of the registry flow are user words: each is a row of the
// "Registry job-log words" table in docs/design-system/next-version/content.md.
// This test builds every line from the real code, turns each number into {n}
// and a reason into {reason}, and fails when a line and the table differ in
// either direction.

const wordsSection = "## Registry job-log words"

var numberRun = regexp.MustCompile(`\d+(\.\d+)?`)

// contentRows reads the second column of the table under wordsSection.
func contentRows(t *testing.T) []string {
	t.Helper()
	dir, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	for depth := 0; depth < 64; depth++ { // up to the folder that holds go.mod
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			break
		}
		up := filepath.Dir(dir)
		if up == dir {
			t.Fatal("go.mod not found above the test folder")
		}
		dir = up
	}
	raw, err := os.ReadFile(filepath.Join(dir, "docs", "design-system", "next-version", "content.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ReplaceAll(string(raw), "\r\n", "\n")
	i := strings.Index(text, wordsSection)
	if i < 0 {
		t.Fatalf("content.md has no %q section", wordsSection)
	}
	var rows []string
	for _, line := range strings.Split(text[i+len(wordsSection):], "\n")[1:] {
		if strings.HasPrefix(line, "## ") {
			break
		}
		if !strings.HasPrefix(line, "|") {
			continue
		}
		cells := strings.Split(strings.Trim(strings.TrimSpace(line), "|"), "|")
		if len(cells) != 2 {
			t.Fatalf("a row of the table has %d cells, want 2: %q", len(cells), line)
		}
		word := strings.TrimSpace(cells[1])
		if word == "Line" || strings.HasPrefix(word, "---") {
			continue
		}
		rows = append(rows, word)
	}
	return rows
}

func TestRegistryJobLogWordsMatchContent(t *testing.T) {
	const reason = "{reason}"
	lines := []string{
		waitingLine(5 * time.Second),
		waitingLine(2*time.Minute + 5*time.Second),
		busyLine(5 * time.Second),
		busyLine(time.Minute + 30*time.Second),
		pullingText + sizeText(0, 0, 0),
		pullingText + sizeText(0, 3_400_000_000, 0),
		pullingText + sizeText(2_100_000_000, 3_400_000_000, 10*time.Second),
		pullingText + sizeText(100_000_000, 3_400_000_000, 10*time.Minute),
		pullRetryText,
		pullFailOpen + reason + pullFailClose,
		pushFailText + reason,
		pushedText,
	}
	got := map[string]bool{}
	for _, l := range lines {
		got[numberRun.ReplaceAllString(l, "{n}")] = true
	}
	want := map[string]bool{}
	for _, r := range contentRows(t) {
		want[r] = true
	}
	for l := range got {
		if !want[l] {
			t.Errorf("the code writes %q; content.md has no such row", l)
		}
	}
	for r := range want {
		if !got[r] {
			t.Errorf("content.md has the row %q; no code writes it", r)
		}
	}
	if !strings.HasPrefix(waitingLine(0), WaitingText) {
		t.Errorf("waitingLine does not start with WaitingText")
	}
}
