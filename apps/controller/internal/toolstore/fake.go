package toolstore

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
)

// The fakes below are for tests of this package and of the agent wiring. They never download, never run a
// manager and write only under the folder they are given.

// FakeInstaller writes a file bin/<tool> into the staging folder instead of running a manager, and records
// the recipes it got. Fail names "tool version" strings that fail.
type FakeInstaller struct {
	mu      sync.Mutex
	Recipes []Recipe
	Dests   []string
	Fail    map[string]error
	Calls   int
}

var _ Installer = (*FakeInstaller)(nil)

// Install implements Installer.
func (f *FakeInstaller) Install(_ context.Context, r Recipe, dest string) (Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Calls++
	f.Recipes, f.Dests = append(f.Recipes, r), append(f.Dests, dest)
	key := filepath.Base(dest)
	for k, err := range f.Fail {
		if k == key {
			return Result{}, err
		}
	}
	if err := os.MkdirAll(filepath.Join(dest, "bin"), 0o755); err != nil {
		return Result{}, err
	}
	return Result{Manager: r.Manager}, os.WriteFile(filepath.Join(dest, "bin", "tool"), []byte("x"), 0o755)
}

// FakeFetcher serves downloads and texts from maps.
type FakeFetcher struct {
	Files map[string][]byte // url to body
	Texts map[string]string // url to text
	Got   []string
}

var _ Fetcher = (*FakeFetcher)(nil)

// Download implements Fetcher.
func (f *FakeFetcher) Download(_ context.Context, url, file string) (string, error) {
	f.Got = append(f.Got, url)
	b, ok := f.Files[url]
	if !ok {
		return "", errors.New("fake: no such url " + url)
	}
	return url, os.WriteFile(file, b, 0o644)
}

// Text implements Fetcher.
func (f *FakeFetcher) Text(_ context.Context, url string) (string, error) {
	f.Got = append(f.Got, url)
	t, ok := f.Texts[url]
	if !ok {
		return "", errors.New("fake: no such url " + url)
	}
	return t, nil
}

// FakeRunner records the commands it was asked to run.
type FakeRunner struct {
	Argv [][]string
	Envs []map[string]string
	Err  error
}

var _ Runner = (*FakeRunner)(nil)

// Run implements Runner.
func (f *FakeRunner) Run(_ context.Context, argv []string, env map[string]string, _ string) error {
	f.Argv, f.Envs = append(f.Argv, argv), append(f.Envs, env)
	return f.Err
}

// FakeCloner copies nothing: it makes dst and records the pair. With Real set it links like LinkCloner.
type FakeCloner struct {
	Pairs [][2]string
	Real  bool
	Err   error
}

var _ Cloner = (*FakeCloner)(nil)

// Clone implements Cloner.
func (f *FakeCloner) Clone(ctx context.Context, src, dst string) error {
	f.Pairs = append(f.Pairs, [2]string{src, dst})
	if f.Err != nil {
		return f.Err
	}
	if f.Real {
		return LinkCloner{}.Clone(ctx, src, dst)
	}
	return os.MkdirAll(dst, 0o755)
}

// FakeIdentity and FakeKiller record the calls of Finish in order.
type FakeIdentity struct{ Wiped []string }

var _ Identity = (*FakeIdentity)(nil)

// Wipe implements Identity.
func (f *FakeIdentity) Wipe(_ context.Context, user string) error {
	f.Wiped = append(f.Wiped, user)
	return nil
}

// FakeKiller records the pids killed.
type FakeKiller struct{ Pids []int }

var _ TreeKiller = (*FakeKiller)(nil)

// KillTree implements TreeKiller.
func (f *FakeKiller) KillTree(_ context.Context, pid int) error {
	f.Pids = append(f.Pids, pid)
	return nil
}
