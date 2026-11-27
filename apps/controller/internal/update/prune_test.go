package update

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"
)

func listDir(t *testing.T, dir string) []string {
	t.Helper()
	es, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range es {
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out
}

// TRAY8: only the current and the previous version folders stay; other entries are never touched.
func TestPruneVersionsKeepsCurrentAndPrevious(t *testing.T) {
	dir := t.TempDir()
	for _, v := range []string{"0.1.1", "0.1.2", "0.1.9", "0.1.10", "0.1.12", "0.1.13", "notes"} {
		if err := os.MkdirAll(filepath.Join(dir, v), 0o750); err != nil {
			t.Fatal(err)
		}
		_ = os.WriteFile(filepath.Join(dir, v, "crsp.msi"), []byte("x"), 0o600)
	}
	_ = os.WriteFile(filepath.Join(dir, "result.json"), []byte("{}"), 0o600)
	removed := PruneVersions(dir, "0.1.13", "0.1.12")
	sort.Strings(removed)
	if !reflect.DeepEqual(removed, []string{"0.1.1", "0.1.10", "0.1.2", "0.1.9"}) {
		t.Fatalf("removed %v", removed)
	}
	want := []string{"0.1.12", "0.1.13", "notes", "result.json"}
	if got := listDir(t, dir); !reflect.DeepEqual(got, want) {
		t.Fatalf("left %v, want %v", got, want)
	}
	if again := PruneVersions(dir, "0.1.13", "0.1.12"); len(again) != 0 {
		t.Fatalf("second run removed %v", again)
	}
}

// TRAY8: a good update (a result that says the running version arrived) prunes the old folders at start;
// a failed one prunes nothing.
func TestManagerPrunesAfterASuccessfulUpdate(t *testing.T) {
	for _, ok := range []bool{true, false} {
		dir := t.TempDir()
		for _, v := range []string{"0.1.1", "0.1.11", "0.1.12", "0.1.13"} {
			_ = os.MkdirAll(filepath.Join(dir, v), 0o750)
		}
		if err := WriteResult(dir, Result{OK: ok, Kind: "update", From: "0.1.12", To: "0.1.13", At: time.Now()}); err != nil {
			t.Fatal(err)
		}
		New(Options{Current: "0.1.13", Dir: dir})
		got := listDir(t, dir)
		want := []string{"0.1.1", "0.1.11", "0.1.12", "0.1.13", "result.json"}
		if ok {
			want = []string{"0.1.12", "0.1.13", "result.json"}
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("ok=%v: %v, want %v", ok, got, want)
		}
	}
}
