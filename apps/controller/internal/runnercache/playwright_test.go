package runnercache

import (
	"path/filepath"
	"testing"
)

// TestBrowsersPathSharedPerMachine: one browsers folder per machine inside the
// tool cache, the same for every runner (caches R5.41); an image with the
// layer keeps its own path.
func TestBrowsersPathSharedPerMachine(t *testing.T) {
	tc := filepath.Join("m", "cache", "tools")
	a := BrowsersEnv(tc, false)[EnvBrowsersPath]
	b := BrowsersEnv(tc, false)[EnvBrowsersPath]
	if a != b || a != filepath.Join(tc, "browsers") {
		t.Fatalf("got %q and %q", a, b)
	}
	if got := BrowsersEnv(tc, true)[EnvBrowsersPath]; got != ImageBrowsersPath {
		t.Fatalf("image layer path: %q", got)
	}
	if rel, err := filepath.Rel(tc, BrowsersDir(tc)); err != nil || rel != "browsers" {
		t.Fatalf("not inside the tool cache: %q %v", rel, err)
	}
}
