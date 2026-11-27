package profiles

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry"
)

const digest = "sha256:1111111111111111111111111111111111111111111111111111111111111111"

// memStore is a fake Store.
type memStore struct {
	p      map[string]Profile
	chosen map[string]string
}

func newMem() *memStore { return &memStore{p: map[string]Profile{}, chosen: map[string]string{}} }
func (m *memStore) Get(_ context.Context, id string) (Profile, error) {
	p, ok := m.p[id]
	if !ok {
		return Profile{}, ErrNotFound
	}
	return p, nil
}
func (m *memStore) List(context.Context) ([]Profile, error) {
	var out []Profile
	for _, p := range m.p {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}
func (m *memStore) Put(_ context.Context, p Profile) error { m.p[p.ID] = p; return nil }
func (m *memStore) SeedIfAbsent(_ context.Context, p Profile) error {
	if _, ok := m.p[p.ID]; !ok {
		m.p[p.ID] = p
	}
	return nil
}
func (m *memStore) Delete(_ context.Context, id string) error { delete(m.p, id); return nil }
func (m *memStore) SetRepositoryProfile(_ context.Context, r, id string) error {
	m.chosen[r] = id
	return nil
}
func (m *memStore) RepositoryProfile(_ context.Context, r string) (string, error) {
	return m.chosen[r], nil
}
func (m *memStore) ClearRepositoryProfile(_ context.Context, r string) error {
	delete(m.chosen, r)
	return nil
}
func (m *memStore) RepositoryProfiles(context.Context) (map[string]string, error) {
	out := map[string]string{}
	for k, v := range m.chosen {
		out[k] = v
	}
	return out, nil
}

type stacks map[string][]string

func (s stacks) StacksOf(_ context.Context, r string) ([]string, error) { return s[r], nil }

type bases struct{ calls int }

func (b *bases) Digest(context.Context, string) (string, error) { b.calls++; return digest, nil }

func seeded(t *testing.T) *memStore {
	t.Helper()
	m := newMem()
	if err := Seed(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestDefaultsAreValidAndUnique(t *testing.T) { // R5.77
	seen := map[string]bool{}
	labels := map[string]bool{}
	for _, p := range Defaults() {
		if err := p.Validate(); err != nil {
			t.Errorf("%s: %v", p.ID, err)
		}
		if seen[p.ID] {
			t.Errorf("duplicate id %s", p.ID)
		}
		seen[p.ID] = true
		if p.Kind != registry.KindStandard || !p.BuiltIn {
			t.Errorf("%s: not a built-in standard profile", p.ID)
		}
		for _, l := range p.Labels {
			if labels[l] {
				t.Errorf("label %s on two profiles", l)
			}
			labels[l] = true
		}
	}
	for _, id := range []string{"base", "node-web", "rust-cross", "go", "python"} {
		if !seen[id] {
			t.Errorf("missing default %s", id)
		}
	}
}

func TestSeedKeepsEdits(t *testing.T) {
	ctx := context.Background()
	m := seeded(t)
	p, _ := m.Get(ctx, "go")
	p.Toolchains = []registry.Toolchain{{Tool: "go", Version: "9.9.9"}}
	_ = m.Put(ctx, p)
	if err := Seed(ctx, m); err != nil {
		t.Fatal(err)
	}
	got, _ := m.Get(ctx, "go")
	if got.Toolchains[0].Version != "9.9.9" {
		t.Fatalf("seed overwrote an edit: %v", got.Toolchains)
	}
}

func TestValidate(t *testing.T) {
	cases := map[string]Profile{
		"bad id":         {ID: "Node Web", Kind: registry.KindStandard, BaseName: "b"},
		"std with repo":  {ID: "a", Kind: registry.KindStandard, Repository: "o/r", BaseName: "b"},
		"custom no repo": {ID: "a", Kind: registry.KindCustom, BaseName: "b"},
		"no base":        {ID: "a", Kind: registry.KindStandard},
		"unknown kind":   {ID: "a", Kind: "x", BaseName: "b"},
	}
	for name, p := range cases {
		if err := p.Validate(); !errors.Is(err, ErrBadProfile) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func f(p string) registry.StackFile { return registry.StackFile{Path: p, Content: []byte("x")} }

func TestDetectStacks(t *testing.T) {
	got := DetectStacks([]registry.StackFile{f("apps/web/package.json"), f("Cargo.toml"), f("Dockerfile"), f("go.mod"), f("compose.yaml")})
	if want := []string{"go", "node", "rust"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	if got := DetectStacks([]registry.StackFile{f("Dockerfile")}); len(got) != 0 {
		t.Fatalf("Dockerfile alone is no stack: %v", got)
	}
}

func TestProfileForStack(t *testing.T) {
	all := Defaults()
	cases := []struct {
		stacks []string
		want   string
	}{
		{[]string{"node"}, "node-web"},
		{[]string{"rust"}, "rust-cross"},
		{[]string{"go"}, "go"},
		{[]string{"python"}, "python"},
		{nil, "base"},
		{[]string{"cobol"}, "base"},
		{[]string{"python", "node"}, "node-web"}, // first in profile order
	}
	for _, c := range cases {
		p, ok := ProfileForStack(all, c.stacks)
		if !ok || p.ID != c.want {
			t.Errorf("%v: got %s ok=%v, want %s", c.stacks, p.ID, ok, c.want)
		}
	}
	if _, ok := ProfileForStack(nil, []string{"node"}); ok {
		t.Error("no profiles must give ok=false")
	}
}

func TestProfileForPrecedence(t *testing.T) { // R5.78
	ctx := context.Background()
	m := seeded(t)
	_ = m.Put(ctx, Profile{ID: "acme-api", Kind: registry.KindCustom, Repository: "Acme/API", BaseName: "ubuntu:24.04",
		BaseDigest: digest, Labels: []string{"acme-api"}, Stacks: []string{"go"}})
	r := &Resolver{Store: m, Stacks: stacks{"acme/web": {"node"}, "acme/api": {"go"}, "acme/odd": {"go"}}, Bases: &bases{}}

	pick := func(repo string, labels ...string) string {
		t.Helper()
		ref, err := r.ProfileFor(ctx, repo, labels)
		if err != nil {
			t.Fatal(err)
		}
		return ref.ID
	}
	if got := pick("acme/web", "self-hosted", "linux"); got != "node-web" {
		t.Errorf("stack: %s", got)
	}
	if got := pick("acme/unknown"); got != "base" {
		t.Errorf("fallback: %s", got)
	}
	// Custom profile covers its own repository's stack only (case-insensitive repo).
	if got := pick("ACME/api"); got != "acme-api" {
		t.Errorf("custom by stack: %s", got)
	}
	if got := pick("acme/odd"); got != "go" {
		t.Errorf("another repo must not see a custom profile: %s", got)
	}
	// Label beats the repository choice and the stack.
	_ = m.SetRepositoryProfile(ctx, "acme/web", "python")
	if got := pick("acme/web"); got != "python" {
		t.Errorf("owner choice beats stack: %s", got)
	}
	if got := pick("acme/web", "CRSP-rust-cross"); got != "rust-cross" {
		t.Errorf("label beats choice: %s", got)
	}
	// A choice of another repository's custom profile, or a missing one, is ignored.
	_ = m.SetRepositoryProfile(ctx, "acme/web", "acme-api")
	if got := pick("acme/web"); got != "node-web" {
		t.Errorf("foreign custom choice: %s", got)
	}
	_ = m.SetRepositoryProfile(ctx, "acme/web", "gone")
	if got := pick("acme/web"); got != "node-web" {
		t.Errorf("missing choice: %s", got)
	}
}

func TestProfileForRef(t *testing.T) { // R5.78, R5.79
	ctx := context.Background()
	m := seeded(t)
	b := &bases{}
	r := &Resolver{Store: m, Stacks: stacks{"o/r": {"node"}}, Bases: b}
	ref, err := r.ProfileFor(ctx, "o/r", nil)
	if err != nil {
		t.Fatal(err)
	}
	if ref.Kind != registry.KindStandard || ref.BaseName != DefaultBase || ref.BaseDigest != digest || ref.Pins["runner"] == "" {
		t.Fatalf("ref %+v", ref)
	}
	if !sort.SliceIsSorted(ref.Toolchains, func(i, j int) bool { return ref.Toolchains[i].Tool < ref.Toolchains[j].Tool }) || len(ref.Toolchains) != 2 {
		t.Fatalf("toolchains %v", ref.Toolchains)
	}
	// The same profile gives the same ref for every call: the arch is in the key, not here.
	again, _ := r.ProfileFor(ctx, "o/r", nil)
	if !reflect.DeepEqual(ref, again) {
		t.Fatal("ref differs between calls")
	}
	// Both arches give two keys of one profile id.
	sh, _ := registry.StackHash(nil)
	in := func(arch string) registry.KeyInput {
		return registry.KeyInput{Profile: ref.ID, StackHash: sh, Arch: arch, BaseDigest: ref.BaseDigest, Pins: ref.Pins, Toolchains: ref.Toolchains}
	}
	k1, err1 := registry.ComputeKey(in("amd64"))
	k2, err2 := registry.ComputeKey(in("arm64"))
	if err1 != nil || err2 != nil || k1 == k2 {
		t.Fatalf("per-arch keys: %v %v %v %v", k1, k2, err1, err2)
	}
	// The ref is a copy: changing it does not change the stored profile.
	ref.Pins["runner"] = "0"
	p, _ := m.Get(ctx, "node-web")
	if p.Pins["runner"] == "0" {
		t.Fatal("ref shares the pins map")
	}
}

func TestBaseUnresolved(t *testing.T) {
	m := seeded(t)
	r := &Resolver{Store: m}
	if _, err := r.ProfileFor(context.Background(), "o/r", nil); !errors.Is(err, ErrBaseUnresolved) {
		t.Fatalf("got %v", err)
	}
}

func TestListViews(t *testing.T) {
	ctx := context.Background()
	m := seeded(t)
	_ = m.Put(ctx, Profile{ID: "acme-api", Kind: registry.KindCustom, Repository: "acme/api", BaseName: "b"})
	_ = m.SetRepositoryProfile(ctx, "b/two", "go")
	_ = m.SetRepositoryProfile(ctx, "a/one", "go")
	views, err := ListViews(ctx, m)
	if err != nil {
		t.Fatal(err)
	}
	if views[len(views)-1].ID != "acme-api" {
		t.Fatalf("custom last: %v", views[len(views)-1].ID)
	}
	for _, v := range views {
		if v.ID == "go" && !reflect.DeepEqual(v.Repositories, []string{"a/one", "b/two"}) {
			t.Fatalf("repositories %v", v.Repositories)
		}
	}
}

func TestOlderGeneration(t *testing.T) { // R5.80
	old := "0123456789ab"
	got := OlderGeneration([]string{old, "c57a886d9402"}, func(t string) bool { return t == "c57a886d9402" })
	if !reflect.DeepEqual(got, []string{old}) {
		t.Fatalf("got %v", got)
	}
}
