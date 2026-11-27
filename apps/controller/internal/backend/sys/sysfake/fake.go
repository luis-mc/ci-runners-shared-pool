// Package sysfake gives tests a recording command runner, file system and
// downloader. Nothing real is touched.
package sysfake

import (
	"context"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"sync"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend/sys"
)

// Commands records every call and answers from rules.
type Commands struct {
	mu    sync.Mutex
	Calls []sys.Command
	// Rules answer the first call whose full text contains Match.
	Rules []Rule
	// Fallback answers a call no rule matched; ok false means the empty result.
	Fallback func(cmd sys.Command) (sys.Result, bool)
}

// Rule is one canned answer.
type Rule struct {
	Match  string // substring of Name+Args+Stdin
	Result sys.Result
	Err    error
}

// Run records and answers.
func (c *Commands) Run(_ context.Context, cmd sys.Command) (sys.Result, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Calls = append(c.Calls, cmd)
	text := cmd.Name + " " + strings.Join(cmd.Args, " ") + " " + cmd.Stdin
	for _, r := range c.Rules {
		if strings.Contains(text, r.Match) {
			return r.Result, r.Err
		}
	}
	if c.Fallback != nil {
		if res, ok := c.Fallback(cmd); ok {
			return res, nil
		}
	}
	return sys.Result{}, nil
}

// Texts is the call list as "name arg arg" strings.
func (c *Commands) Texts() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, len(c.Calls))
	for i, x := range c.Calls {
		out[i] = x.String()
	}
	return out
}

// FS is an in-memory file system.
type FS struct {
	mu    sync.Mutex
	Files map[string][]byte
	Modes map[string]fs.FileMode
	Dirs  map[string]fs.FileMode
	Order []string // write and remove log
}

// NewFS makes an empty FS.
func NewFS() *FS {
	return &FS{Files: map[string][]byte{}, Modes: map[string]fs.FileMode{}, Dirs: map[string]fs.FileMode{}}
}

// MkdirAll implements sys.FS.
func (f *FS) MkdirAll(p string, perm fs.FileMode) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Dirs[p] = perm
	f.Order = append(f.Order, "mkdir "+p)
	return nil
}

// WriteFile implements sys.FS.
func (f *FS) WriteFile(p string, d []byte, perm fs.FileMode) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Files[p] = append([]byte(nil), d...)
	f.Modes[p] = perm
	f.Order = append(f.Order, "write "+p)
	return nil
}

// ReadFile implements sys.FS.
func (f *FS) ReadFile(p string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.Files[p]
	if !ok {
		return nil, fs.ErrNotExist
	}
	return d, nil
}

// Exists implements sys.FS.
func (f *FS) Exists(p string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.Files[p]; ok {
		return true
	}
	_, ok := f.Dirs[p]
	return ok
}

// RemoveAll implements sys.FS.
func (f *FS) RemoveAll(p string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for k := range f.Files {
		if k == p || strings.HasPrefix(k, p+"/") || strings.HasPrefix(k, p+`\`) {
			delete(f.Files, k)
		}
	}
	delete(f.Dirs, p)
	f.Order = append(f.Order, "remove "+p)
	return nil
}

// Names lists the files, sorted.
func (f *FS) Names() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for k := range f.Files {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Fetcher records downloads and writes Body to the destination of the FS.
type Fetcher struct {
	FS   *FS
	Body []byte
	Err  error
	URLs []string
}

// Fetch implements sys.Fetcher.
func (f *Fetcher) Fetch(_ context.Context, url, dest string) error {
	f.URLs = append(f.URLs, url)
	if f.Err != nil {
		return f.Err
	}
	if f.FS == nil {
		return fmt.Errorf("fake fetcher has no FS")
	}
	return f.FS.WriteFile(dest, f.Body, 0o600)
}
