package registry

import (
	"errors"
	"strings"
	"testing"
)

// The three vectors of docs/features/registry/spec.md section 7.2. A change to
// any expected value is a change of the key format and needs the owner's yes.
const vecStack = "3ce86298410301e478baa185c81f75e9d8e02dcf22633e16d02d5ee627d67eb0"

var vecFiles = []StackFile{
	{Path: "package.json", Content: []byte("{\"name\":\"docs-site\"}\r\n")},
	{Path: ".nvmrc", Content: []byte("22\n")},
	{Path: "pnpm-lock.yaml", Content: []byte("lockfileVersion: '9.0'\n")},
}

var keyVectors = []struct {
	name string
	in   KeyInput
	json string
	key  Key
}{
	{
		name: "empty stack, no pins",
		in: KeyInput{Profile: "base", StackHash: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", Arch: "amd64",
			BaseDigest: "sha256:0000000000000000000000000000000000000000000000000000000000000000"},
		json: `{"arch":"amd64","base_digest":"sha256:0000000000000000000000000000000000000000000000000000000000000000","pins":{},"profile":"base","stack_hash":"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855","toolchains":[]}`,
		key:  "37fef5dbb41b",
	},
	{
		name: "node web, x64, unsorted pins and toolchains",
		in: KeyInput{Profile: "node-web", StackHash: vecStack, Arch: "x64",
			BaseDigest: "sha256:1111111111111111111111111111111111111111111111111111111111111111",
			Pins:       map[string]string{"runner": "2.330.0", "gh": "2.62.0"},
			Toolchains: []Toolchain{{"pnpm", "10.34.5"}, {"node", "22.11.0"}, {"node", "20.18.0"}}},
		json: `{"arch":"amd64","base_digest":"sha256:1111111111111111111111111111111111111111111111111111111111111111","pins":{"gh":"2.62.0","runner":"2.330.0"},"profile":"node-web","stack_hash":"3ce86298410301e478baa185c81f75e9d8e02dcf22633e16d02d5ee627d67eb0","toolchains":[{"tool":"node","version":"20.18.0"},{"tool":"node","version":"22.11.0"},{"tool":"pnpm","version":"10.34.5"}]}`,
		key:  "c57a886d9402",
	},
	{
		name: "rust cross, arm64",
		in: KeyInput{Profile: "rust-cross", StackHash: vecStack, Arch: "arm64",
			BaseDigest: "sha256:abababababababababababababababababababababababababababababababab",
			Pins:       map[string]string{"llvm": "19.1.0", "cargo-xwin": "0.18.4"},
			Toolchains: []Toolchain{{"rust", "1.84.0"}}},
		json: `{"arch":"arm64","base_digest":"sha256:abababababababababababababababababababababababababababababababab","pins":{"cargo-xwin":"0.18.4","llvm":"19.1.0"},"profile":"rust-cross","stack_hash":"3ce86298410301e478baa185c81f75e9d8e02dcf22633e16d02d5ee627d67eb0","toolchains":[{"tool":"rust","version":"1.84.0"}]}`,
		key:  "b82203cda3a4",
	},
}

func TestKeyVectors(t *testing.T) {
	for _, v := range keyVectors {
		t.Run(v.name, func(t *testing.T) {
			b, err := CanonicalJSON(v.in)
			if err != nil {
				t.Fatal(err)
			}
			if string(b) != v.json {
				t.Errorf("canonical JSON\n got %s\nwant %s", b, v.json)
			}
			k, err := ComputeKey(v.in)
			if err != nil {
				t.Fatal(err)
			}
			if k != v.key || !k.Valid() {
				t.Errorf("key %q, want %q", k, v.key)
			}
		})
	}
}

func TestStackHashVector(t *testing.T) {
	got, err := StackHash(vecFiles)
	if err != nil {
		t.Fatal(err)
	}
	if got != vecStack {
		t.Fatalf("stack hash %s, want %s", got, vecStack)
	}
	empty, _ := StackHash(nil)
	if empty != "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" {
		t.Fatalf("empty stack hash %s", empty)
	}
}

func TestStackHashIgnoresOrderAndCRLFOnly(t *testing.T) {
	rev := []StackFile{vecFiles[2], vecFiles[0], vecFiles[1]}
	lf := []StackFile{{Path: "package.json", Content: []byte("{\"name\":\"docs-site\"}\n")}, vecFiles[1], vecFiles[2]}
	for _, fs := range [][]StackFile{rev, lf} {
		if got, _ := StackHash(fs); got != vecStack {
			t.Errorf("stack hash %s, want %s", got, vecStack)
		}
	}
	cr := []StackFile{{Path: "package.json", Content: []byte("{\"name\":\"docs-site\"}\r")}, vecFiles[1], vecFiles[2]}
	if got, _ := StackHash(cr); got == vecStack {
		t.Error("a lone CR must change the hash")
	}
}

func TestStackHashRefusesBadPaths(t *testing.T) {
	for _, p := range []string{"", "/a", "./a", `a\b`} {
		if _, err := StackHash([]StackFile{{Path: p}}); !errors.Is(err, ErrBadKeyInput) {
			t.Errorf("path %q: err %v", p, err)
		}
	}
	if _, err := StackHash([]StackFile{{Path: "a"}, {Path: "a"}}); !errors.Is(err, ErrBadKeyInput) {
		t.Errorf("duplicate path: err %v", err)
	}
}

func TestKeyChangesWithEveryPart(t *testing.T) {
	base := keyVectors[1].in
	k0, _ := ComputeKey(base)
	mut := []func(*KeyInput){
		func(i *KeyInput) { i.Profile = "node-web-2" },
		func(i *KeyInput) { i.StackHash = strings.Repeat("0", 64) },
		func(i *KeyInput) { i.Arch = "arm64" },
		func(i *KeyInput) { i.BaseDigest = "sha256:" + strings.Repeat("2", 64) },
		func(i *KeyInput) { i.Pins = map[string]string{"gh": "2.63.0", "runner": "2.330.0"} },
		func(i *KeyInput) { i.Toolchains = []Toolchain{{"node", "22.11.0"}} },
	}
	for n, m := range mut {
		in := base
		m(&in)
		if k, _ := ComputeKey(in); k == k0 {
			t.Errorf("mutation %d kept key %s", n, k)
		}
	}
	dup := base
	dup.Toolchains = append(append([]Toolchain{}, base.Toolchains...), Toolchain{"node", "22.11.0"})
	if k, _ := ComputeKey(dup); k != k0 {
		t.Errorf("a repeated toolchain changed the key")
	}
}

func TestKeyRefusesBadInput(t *testing.T) {
	good := keyVectors[0].in
	bad := []func(*KeyInput){
		func(i *KeyInput) { i.Profile = " " },
		func(i *KeyInput) { i.Arch = "riscv64" },
		func(i *KeyInput) { i.StackHash = "ABC" },
		func(i *KeyInput) { i.BaseDigest = "ubuntu:24.04" },
		func(i *KeyInput) { i.Pins = map[string]string{"": "1"} },
		func(i *KeyInput) { i.Toolchains = []Toolchain{{"", "1"}} },
	}
	for n, m := range bad {
		in := good
		m(&in)
		if _, err := ComputeKey(in); !errors.Is(err, ErrBadKeyInput) {
			t.Errorf("case %d: err %v", n, err)
		}
	}
}

func TestOCIArchAndShort(t *testing.T) {
	for in, want := range map[string]string{"x64": "amd64", "AMD64": "amd64", "aarch64": "arm64", "arm64": "arm64", "s390x": "s390x"} {
		if got := OCIArch(in); got != want {
			t.Errorf("OCIArch(%q) = %q", in, got)
		}
	}
	if Key("c57a886d9402").Short() != "c57a88" || Key("ab").Short() != "ab" {
		t.Error("Short")
	}
}
