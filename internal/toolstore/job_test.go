package toolstore

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func envMap(env []string) map[string]string {
	m := map[string]string{}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		m[k] = v
	}
	return m
}

// R5.114: a private home with its own HOME, CARGO_HOME and caches; PATH from the plan; no manager variable.
func TestBuildJobHome(t *testing.T) {
	s, in := newStore(t), &FakeInstaller{}
	plan := Plan{rust185, {Tool: ToolXcode, Version: "16.2"}}
	if _, err := s.BuildJobHome(t.TempDir(), "j1", plan, nil); !errors.Is(err, ErrNeedsInstall) {
		t.Fatalf("job home before install: %v", err)
	}
	if err := s.EnsureInstalled(context.Background(), in, plan); err != nil {
		t.Fatal(err)
	}
	jobs := t.TempDir()
	jh, err := s.BuildJobHome(jobs, "j1", plan, map[string]string{"PATH": "/usr/bin:/bin", "NVM_DIR": "/x", "PYENV_ROOT": "/y", "CI": "true"})
	if err != nil {
		t.Fatal(err)
	}
	e := envMap(jh.Env)
	if e["HOME"] != filepath.Join(jobs, "j1", "home") || e["CARGO_HOME"] != jh.CargoHome || e["XDG_CACHE_HOME"] != jh.Caches || e["TMPDIR"] != jh.Temp {
		t.Fatalf("env = %v", e)
	}
	if !strings.HasPrefix(e["PATH"], s.Dir(rust185)) || !strings.HasSuffix(e["PATH"], "/usr/bin:/bin") {
		t.Fatalf("PATH = %q", e["PATH"])
	}
	if _, ok := e["NVM_DIR"]; ok || e["PYENV_ROOT"] != "" || e["CI"] != "true" {
		t.Fatalf("manager variable leaked: %v", e)
	}
	if !strings.HasSuffix(e["DEVELOPER_DIR"], "Xcode-16.2.app/Contents/Developer") || e["RUSTUP_TOOLCHAIN"] != "1.85.0" {
		t.Fatalf("tool variables = %v", e)
	}
	for _, d := range []string{jh.Home, jh.CargoHome, jh.Caches, jh.Temp} {
		if fi, err := os.Stat(d); err != nil || fi.Mode().Perm() != 0o700 {
			t.Errorf("%s: %v %v", d, fi, err)
		}
	}
	for _, bad := range []string{"../x", "", "a/b"} {
		if _, err := s.BuildJobHome(jobs, bad, plan, nil); err == nil {
			t.Errorf("job id %q accepted", bad)
		}
	}
}

func TestBuildJobHomeWindowsNames(t *testing.T) {
	s, in := newStore(t), &FakeInstaller{}
	s.GOOS = Windows
	plan := Plan{{Tool: ToolGo, Version: "1.24.0"}}
	if err := s.EnsureInstalled(context.Background(), in, plan); err != nil {
		t.Fatal(err)
	}
	jh, err := s.BuildJobHome(t.TempDir(), "j2", plan, map[string]string{"PATH": `C:\Windows`})
	if err != nil {
		t.Fatal(err)
	}
	e := envMap(jh.Env)
	for _, k := range []string{"USERPROFILE", "APPDATA", "LOCALAPPDATA", "TEMP", "TMP", "HOME", "CARGO_HOME"} {
		if !strings.HasPrefix(e[k], jh.Root) {
			t.Errorf("%s = %q is outside the job home", k, e[k])
		}
	}
	if !strings.Contains(e["PATH"], ";") || e["GOTOOLCHAIN"] != "local" {
		t.Errorf("env = %v", e)
	}
}

// R5.114: step 8 kills the tree first, removes the files, then wipes the job user.
func TestFinishWipes(t *testing.T) {
	s, in := newStore(t), &FakeInstaller{}
	_ = s.EnsureInstalled(context.Background(), in, Plan{rust185})
	jh, err := s.BuildJobHome(t.TempDir(), "j3", Plan{rust185}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(jh.Home, "secret"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	k, id := &FakeKiller{}, &FakeIdentity{}
	if err := jh.Finish(context.Background(), k, 4242, id, "crsp-job1"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(jh.Root); err == nil {
		t.Fatal("job home left")
	}
	if len(k.Pids) != 1 || k.Pids[0] != 4242 || len(id.Wiped) != 1 || id.Wiped[0] != "crsp-job1" {
		t.Fatalf("killer %v identity %v", k.Pids, id.Wiped)
	}
	if !s.Has(rust185) {
		t.Fatal("finish touched the store")
	}
}

// R5.115: key is tool versions plus lockfile hash; trusted green default-branch runs save under a new key only.
func TestCacheKeyAndTrust(t *testing.T) {
	a := CacheKey(Darwin, Plan{rust185, {Tool: ToolGo, Version: "1.24.0"}}, "abc")
	b := CacheKey(Darwin, Plan{{Tool: ToolGo, Version: "1.24.0"}, rust185, rust185}, "abc")
	if a != b || !keyShape.MatchString(a) {
		t.Fatalf("order must not matter: %s %s", a, b)
	}
	for _, other := range []string{CacheKey(Windows, Plan{rust185, {Tool: ToolGo, Version: "1.24.0"}}, "abc"), CacheKey(Darwin, Plan{rust185}, "abc"),
		CacheKey(Darwin, Plan{rust185, {Tool: ToolGo, Version: "1.24.0"}}, "abd")} {
		if other == a {
			t.Fatal("different input, same key")
		}
	}
	ok := Trust{Repository: "o/r", HeadRepository: "O/R", Ref: "refs/heads/main", DefaultBranch: "main", Green: true}
	if !ok.MaySave() {
		t.Fatal("trusted run refused")
	}
	for name, mut := range map[string]func(*Trust){
		"red": func(t *Trust) { t.Green = false }, "fork": func(t *Trust) { t.HeadRepository = "evil/r" },
		"branch": func(t *Trust) { t.Ref = "refs/heads/feature" }, "pull request": func(t *Trust) { t.Ref = "refs/pull/3/merge" },
		"no default": func(t *Trust) { t.DefaultBranch = "" },
	} {
		x := ok
		mut(&x)
		if x.MaySave() {
			t.Errorf("%s may save", name)
		}
	}
}

func TestCacheSaveRestore(t *testing.T) {
	root, src := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "dep"), []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := Cache{Root: filepath.Join(root, "cache"), Clone: &FakeCloner{Real: true}, Seal: sealDir}
	t.Cleanup(func() { _ = unsealDir(c.Root) })
	key := CacheKey(Darwin, Plan{rust185}, "h1")
	trusted := Trust{Repository: "o/r", HeadRepository: "o/r", Ref: "main", DefaultBranch: "main", Green: true}
	dst := filepath.Join(t.TempDir(), "caches")
	if hit, err := c.Restore(context.Background(), key, dst); hit || err != nil {
		t.Fatalf("miss = %v %v", hit, err)
	}
	if saved, err := c.Save(context.Background(), key, src, Trust{Repository: "o/r", HeadRepository: "x/r", Ref: "main", DefaultBranch: "main", Green: true}); saved || err != nil || c.Has(key) {
		t.Fatalf("fork saved: %v %v", saved, err)
	}
	if saved, err := c.Save(context.Background(), key, src, trusted); !saved || err != nil || !c.Has(key) {
		t.Fatalf("trusted save: %v %v", saved, err)
	}
	if err := os.WriteFile(filepath.Join(src, "dep"), []byte("v2"), 0o644); err != nil {
		t.Fatal(err)
	}
	if saved, err := c.Save(context.Background(), key, src, trusted); saved || err != nil {
		t.Fatalf("overwrite: %v %v", saved, err)
	}
	if hit, err := c.Restore(context.Background(), key, dst); !hit || err != nil {
		t.Fatalf("restore = %v %v", hit, err)
	}
	if b, _ := os.ReadFile(filepath.Join(dst, "dep")); string(b) != "v1" {
		t.Fatalf("restored %q, want the first save", b)
	}
	if fi, _ := os.Stat(filepath.Join(c.Root, key, "dep")); fi.Mode().Perm()&0o222 != 0 {
		t.Fatal("saved entry is writable")
	}
	if _, err := c.Restore(context.Background(), "../x", dst); !errors.Is(err, ErrBadKey) {
		t.Fatalf("bad key: %v", err)
	}
	if _, err := os.Stat(filepath.Join(c.Root, key+".tmp")); err == nil {
		t.Fatal("temporary folder left")
	}
}

func TestLinkClonerSharesFiles(t *testing.T) {
	src, dst := t.TempDir(), filepath.Join(t.TempDir(), "d")
	_ = os.MkdirAll(filepath.Join(src, "a", "b"), 0o755)
	_ = os.WriteFile(filepath.Join(src, "a", "b", "f"), []byte("data"), 0o644)
	if err := (LinkCloner{}).Clone(context.Background(), src, dst); err != nil {
		t.Fatal(err)
	}
	a, _ := os.Stat(filepath.Join(src, "a", "b", "f"))
	b, err := os.Stat(filepath.Join(dst, "a", "b", "f"))
	if err != nil || !os.SameFile(a, b) {
		t.Fatalf("not a link: %v", err)
	}
}

func sum(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

func zipOf(t *testing.T, files map[string]string) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for n, c := range files {
		w, _ := zw.Create(n)
		_, _ = w.Write([]byte(c))
	}
	_ = zw.Close()
	return buf.Bytes()
}

// R5.113: the official archive is checked against the published SHA-256, and unpacked without links or "..".
func TestExecInstallerArchive(t *testing.T) {
	archive := zipOf(t, map[string]string{"go/bin/go": "bin", "go/VERSION": "go1.24.0"})
	url := "https://go.dev/dl/go1.24.0.windows-amd64.zip"
	mk := func(sha string) (*ExecInstaller, *FakeFetcher) {
		f := &FakeFetcher{Files: map[string][]byte{url: archive}, Texts: map[string]string{url + ".sha256": sha + "  go1.24.0.zip\n"}}
		return &ExecInstaller{Fetch: f, Run: &FakeRunner{}}, f
	}
	rec, _ := RecipeFor(Windows, "amd64", DefaultSources(), Tool{Tool: ToolGo, Version: "1.24.0"}, "x")
	dest := t.TempDir()
	in, _ := mk(sum(archive))
	res, err := in.Install(context.Background(), rec, dest)
	if err != nil || len(res.Unchecked) != 0 {
		t.Fatalf("install: %+v %v", res, err)
	}
	if b, err := os.ReadFile(filepath.Join(dest, "bin", "go")); err != nil || string(b) != "bin" {
		t.Fatalf("unpacked: %q %v", b, err)
	}
	bad, _ := mk(strings.Repeat("0", 64))
	dest2 := t.TempDir()
	if _, err := bad.Install(context.Background(), rec, dest2); err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Fatalf("mismatch accepted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest2, "bin")); err == nil {
		t.Fatal("unpacked after a bad checksum")
	}
}

func TestExecInstallerRunsManagerAndFlagsUnchecked(t *testing.T) {
	f := &FakeFetcher{Files: map[string][]byte{"https://dot.net/v1/dotnet-install.sh": []byte("#!/bin/sh")}}
	r := &FakeRunner{}
	rec, _ := RecipeFor(Darwin, "arm64", DefaultSources(), Tool{Tool: ToolDotnet, Version: "8.0.100"}, "/s/x")
	res, err := (&ExecInstaller{Fetch: f, Run: r}).Install(context.Background(), rec, t.TempDir())
	if err != nil || len(res.Unchecked) != 1 || res.Unchecked[0] != "dotnet-install.sh" {
		t.Fatalf("res %+v %v", res, err)
	}
	if len(r.Argv) != 1 || r.Argv[0][len(r.Argv[0])-2] != "--install-dir" && !strings.Contains(strings.Join(r.Argv[0], " "), "--version 8.0.100") {
		t.Fatalf("command = %v", r.Argv)
	}
	r.Err = errors.New("exit 1")
	if _, err := (&ExecInstaller{Fetch: f, Run: r}).Install(context.Background(), rec, t.TempDir()); err == nil {
		t.Fatal("manager failure not reported")
	}
}

func TestUnpackRefusesUnsafe(t *testing.T) {
	dir := t.TempDir()
	zp := filepath.Join(dir, "a.zip")
	_ = os.WriteFile(zp, zipOf(t, map[string]string{"../evil": "x"}), 0o644)
	if err := Unpack(zp, filepath.Join(dir, "o1"), 0); !errors.Is(err, ErrUnsafeArchive) {
		t.Errorf("zip: %v", err)
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	_ = tw.WriteHeader(&tar.Header{Name: "go/bin/link", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"})
	_ = tw.Close()
	_ = gz.Close()
	tp := filepath.Join(dir, "a.tar.gz")
	_ = os.WriteFile(tp, buf.Bytes(), 0o644)
	if err := Unpack(tp, filepath.Join(dir, "o2"), 1); !errors.Is(err, ErrUnsafeArchive) {
		t.Errorf("tar link: %v", err)
	}
}
