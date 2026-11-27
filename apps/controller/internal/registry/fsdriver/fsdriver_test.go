package fsdriver

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	storagedriver "github.com/distribution/distribution/v3/registry/storage/driver"
)

// PutContent replaces an existing file and leaves no temporary file behind
// (on Windows the rename failed while the temporary file was open).
func TestPutContentReplaces(t *testing.T) {
	dir := t.TempDir()
	d, err := New(map[string]any{"rootdirectory": dir})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, v := range []string{"one", "two"} {
		if err := d.PutContent(ctx, "/a/b/startedat", []byte(v)); err != nil {
			t.Fatal(err)
		}
	}
	got, err := d.GetContent(ctx, "/a/b/startedat")
	if err != nil || string(got) != "two" {
		t.Fatalf("%q %v", got, err)
	}
	ents, _ := os.ReadDir(filepath.Join(dir, "a", "b"))
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("temporary file left: %s", e.Name())
		}
	}
}

// List and Walk give slash paths on every system (on Windows the inner driver
// answered with backslashes).
func TestListAndWalkUseSlashes(t *testing.T) {
	d, err := New(map[string]any{"rootdirectory": t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := d.PutContent(ctx, "/a/b/c", []byte("x")); err != nil {
		t.Fatal(err)
	}
	got, err := d.List(ctx, "/a/b")
	if err != nil || len(got) != 1 || got[0] != "/a/b/c" {
		t.Fatalf("list %v %v", got, err)
	}
	var seen []string
	if err := d.Walk(ctx, "/a", func(fi storagedriver.FileInfo) error {
		seen = append(seen, fi.Path())
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, p := range seen {
		if strings.Contains(p, `\`) {
			t.Fatalf("backslash in %q", p)
		}
	}
	if len(seen) != 2 {
		t.Fatalf("walk %v", seen)
	}
}
