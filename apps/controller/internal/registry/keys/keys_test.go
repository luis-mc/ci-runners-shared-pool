package keys

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry"
)

var baseRef = registry.ProfileRef{
	ID: "node-web", Kind: registry.KindCustom, BaseName: "ubuntu:24.04",
	BaseDigest: "sha256:" + strings.Repeat("1", 64),
	Pins:       map[string]string{"runner": "2.330.0"},
	Toolchains: []registry.Toolchain{{Tool: "node", Version: "22.11.0"}},
}

func stackFiles(lock string) []registry.StackFile {
	return []registry.StackFile{
		{Path: "package.json", Content: []byte("{\"name\":\"docs-site\"}\r\n")},
		{Path: ".nvmrc", Content: []byte("22\n")},
		{Path: "pnpm-lock.yaml", Content: []byte(lock)},
	}
}

func keyer(commits map[string][]registry.StackFile) *Keyer {
	return &Keyer{Profiles: fakeProfiles{baseRef}, Stacks: fakeStacks{commits}}
}

// R5.23, D19: the same stack files on a branch give the same key.
func TestSameStackFilesSameKey(t *testing.T) {
	k := keyer(map[string][]registry.StackFile{
		"main-sha":   stackFiles("lockfileVersion: '9.0'\n"),
		"branch-sha": stackFiles("lockfileVersion: '9.0'\n"),
	})
	ctx := context.Background()
	a, in, ref, err := k.KeyFor(ctx, "o/r", "main-sha", "x64", []string{"self-hosted"})
	if err != nil {
		t.Fatal(err)
	}
	b, _, _, err := k.KeyFor(ctx, "o/r", "branch-sha", "amd64", nil)
	if err != nil {
		t.Fatal(err)
	}
	if a != b || !a.Valid() {
		t.Fatalf("keys differ for the same stack files: %s vs %s", a, b)
	}
	if in.Arch != "amd64" || ref.ID != "node-web" || in.BaseDigest != baseRef.BaseDigest {
		t.Fatalf("input = %+v", in)
	}
	// The key is exactly ComputeKey of what KeyFor returns.
	if want, _ := registry.ComputeKey(in); want != a {
		t.Fatalf("key %s != ComputeKey %s", a, want)
	}
}

// A lock file change gives a variant (a different key); each other part does too.
func TestLockFileChangeGivesNewKey(t *testing.T) {
	k := keyer(map[string][]registry.StackFile{
		"main":   stackFiles("lockfileVersion: '9.0'\n"),
		"branch": stackFiles("lockfileVersion: '9.0'\nnew-dep: 1\n"),
	})
	ctx := context.Background()
	a, _, _, _ := k.KeyFor(ctx, "o/r", "main", "amd64", nil)
	b, _, _, _ := k.KeyFor(ctx, "o/r", "branch", "amd64", nil)
	if a == b {
		t.Fatal("a lock file change kept the key")
	}
	c, _, _, _ := k.KeyFor(ctx, "o/r", "main", "arm64", nil)
	if a == c {
		t.Fatal("arch did not change the key")
	}
}

func TestKeyForErrors(t *testing.T) {
	if _, _, _, err := (&Keyer{}).KeyFor(context.Background(), "o/r", "c", "amd64", nil); !errors.Is(err, ErrNoSource) {
		t.Fatalf("err = %v, want ErrNoSource", err)
	}
	bad := &Keyer{Profiles: fakeProfiles{baseRef}, Stacks: fakeStacks{map[string][]registry.StackFile{"c": {{Path: "/abs", Content: nil}}}}}
	if _, _, _, err := bad.KeyFor(context.Background(), "o/r", "c", "amd64", nil); !errors.Is(err, registry.ErrBadKeyInput) {
		t.Fatalf("err = %v, want ErrBadKeyInput", err)
	}
	if _, _, _, err := keyer(nil).KeyFor(context.Background(), "o/r", "c", "riscv", nil); !errors.Is(err, registry.ErrBadKeyInput) {
		t.Fatalf("bad arch: err = %v", err)
	}
}

// R5.7: every label is there and agrees with the input.
func TestLabels(t *testing.T) {
	k := keyer(map[string][]registry.StackFile{"c": stackFiles("x")})
	key, in, ref, err := k.KeyFor(context.Background(), "o/r", "c", "arm64", nil)
	if err != nil {
		t.Fatal(err)
	}
	l := LabelsFor(key, in, ref, "o/r", "c")
	want := map[string]string{
		LabelRevision: "c", LabelSource: "https://github.com/o/r", LabelBaseName: "ubuntu:24.04", LabelBaseDigest: baseRef.BaseDigest,
		LabelProfile: "node-web", LabelLineage: "o/r|node-web|arm64", LabelStackHash: in.StackHash, LabelKey: key.String(),
	}
	if len(l) != len(want) {
		t.Fatalf("labels = %v", l)
	}
	for n, v := range want {
		if l[n] != v {
			t.Errorf("%s = %q, want %q", n, l[n], v)
		}
	}
	repo, prof, arch, ok := ParseLineage(l[LabelLineage])
	if !ok || repo != "o/r" || prof != "node-web" || arch != "arm64" {
		t.Fatalf("ParseLineage = %q %q %q %v", repo, prof, arch, ok)
	}
	if r, _, _, ok := ParseLineage(Lineage("", "node-web", "x64")); !ok || r != "" {
		t.Fatalf("standard lineage: %q %v", r, ok)
	}
	if _, _, _, ok := ParseLineage("nope"); ok {
		t.Fatal("bad lineage parsed")
	}
}

func TestSimilar(t *testing.T) {
	a := registry.Image{ID: "a", Profile: "p", Arch: "amd64"}
	b := registry.Image{ID: "b", Profile: "p", Arch: "amd64"}
	la := []registry.Layer{{Digest: "1", SizeBytes: 70}, {Digest: "2", SizeBytes: 30}}
	lb := []registry.Layer{{Digest: "1", SizeBytes: 70}, {Digest: "3", SizeBytes: 30}}
	if !Similar(a, b, la, lb) {
		t.Fatal("70% shared is not similar")
	}
	l60a := []registry.Layer{{Digest: "1", SizeBytes: 60}, {Digest: "2", SizeBytes: 40}}
	l60b := []registry.Layer{{Digest: "1", SizeBytes: 60}, {Digest: "9", SizeBytes: 40}}
	if Similar(a, b, l60a, l60b) {
		t.Fatal("60% is not over 60%")
	}
	b.Arch = "arm64"
	if Similar(a, b, la, lb) || Similar(a, a, la, la) {
		t.Fatal("other arch or same image counted")
	}
	if SharedShare(nil, nil) != 0 {
		t.Fatal("empty share")
	}
}
