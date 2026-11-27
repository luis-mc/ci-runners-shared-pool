package tray

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The tray-modal guard (dev-standards design-guards lifecycle/tray-modal.mjs, LC-22 and LC-30) for Go.
// Tray, tray server and update window code have no modal, no message box and no Yes/No dialog, and
// no fixed window size. The tray only notifies.

var modalRE = regexp.MustCompile(`\b(MessageBox\w*|MB_YESNO|MB_OK\w*|MB_ICON\w*|DialogResult|YesNo|yes_no|AskDialog|show_dialog|ShowDialog|NSAlert|display dialog|--yesno|--question|--ok-label|--yes-label)\b`)

var fixedSizeRE = regexp.MustCompile(`(ClientSize|CreateWindowEx\w*)[^\n]*\b\d{3,}\b`)

func goFiles(t *testing.T, dirs ...string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, d := range dirs {
		files, _ := filepath.Glob(filepath.Join("..", d, "*.go"))
		for _, f := range files {
			if strings.HasSuffix(f, "_test.go") {
				continue
			}
			b, err := os.ReadFile(f) //nolint:gosec // repository source
			if err != nil {
				t.Fatal(err)
			}
			out[f] = regexp.MustCompile(`(?m)//.*$`).ReplaceAllString(string(b), "")
		}
	}
	return out
}

func TestTrayAsksNothing(t *testing.T) {
	files := goFiles(t, "tray", "trayserver", "updatewindow")
	if len(files) < 20 {
		t.Fatalf("scanned %d files", len(files))
	}
	for f, code := range files {
		if m := modalRE.FindString(code); m != "" {
			t.Errorf("%s: modal or Yes/No dialog from the tray (%s); the tray only notifies (LC-22)", f, m)
		}
	}
}

func TestWindowIsSizedFromItsContent(t *testing.T) {
	for f, code := range goFiles(t, "updatewindow") {
		if m := fixedSizeRE.FindString(code); m != "" {
			t.Errorf("%s: fixed window size (%s); size it from its content (LC-30)", f, m)
		}
	}
}

func TestNoticeWindowIs24Hours(t *testing.T) {
	if NoticeWindow.Hours() != 24 {
		t.Fatalf("NoticeWindow is %v (LC-22)", NoticeWindow)
	}
}
