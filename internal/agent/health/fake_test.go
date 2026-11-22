package health

import (
	"context"
	"errors"
	agentv1 "github.com/luis-mc/ci-runners-shared-pool/internal/gen/agent/v1"
	"sort"
	"strings"
	"time"
)

// fakeEnv answers from maps; no clock, no disk, no process.
type fakeEnv struct {
	goos    string
	now     time.Time
	files   map[string]string
	globs   map[string][]string
	runs    map[string]Result // key: argv joined by space
	disks   map[string][2]int64
	tick    time.Duration
	tickErr error
	head    int
	headErr error
	dnsErr  error
	hang    map[string]chan struct{} // a Run on this key blocks until closed
	written map[string]string
}

func newFake(goos string) *fakeEnv {
	return &fakeEnv{goos: goos, now: time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC), files: map[string]string{}, globs: map[string][]string{},
		runs: map[string]Result{}, disks: map[string][2]int64{}, written: map[string]string{}, head: 200, tickErr: errors.New("no tick")}
}

func (f *fakeEnv) GOOS() string   { return f.goos }
func (f *fakeEnv) Now() time.Time { return f.now }
func (f *fakeEnv) ReadFile(p string) (string, error) {
	if v, ok := f.written[p]; ok {
		return v, nil
	}
	if v, ok := f.files[p]; ok {
		return v, nil
	}
	return "", errors.New("not found")
}
func (f *fakeEnv) WriteFile(p string, d []byte) error { f.written[p] = string(d); return nil }
func (f *fakeEnv) Exists(p string) bool {
	_, ok := f.files[p]
	return ok
}
func (f *fakeEnv) Glob(p string) []string {
	g := append([]string(nil), f.globs[p]...)
	sort.Strings(g)
	return g
}
func (f *fakeEnv) Run(argv []string) (Result, bool) {
	k := strings.Join(argv, " ")
	if ch, ok := f.hang[k]; ok {
		<-ch
	}
	r, ok := f.runs[k]
	return r, ok
}
func (f *fakeEnv) RunTimeout(argv []string, _ time.Duration) (Result, bool) { return f.Run(argv) }
func (f *fakeEnv) Disk(p string) (int64, int64, error) {
	d, ok := f.disks[p]
	if !ok {
		return 0, 0, errors.New("no such volume")
	}
	return d[0], d[1], nil
}
func (f *fakeEnv) TickCount() (time.Duration, error) { return f.tick, f.tickErr }
func (f *fakeEnv) Head(context.Context, string) (int, error) {
	return f.head, f.headErr
}
func (f *fakeEnv) LookupHost(context.Context, string) error { return f.dnsErr }

// psKey is the Runs key of a PowerShell script.
func psKey(script string) string {
	return strings.Join([]string{"powershell", "-NoProfile", "-NonInteractive", "-Command", script}, " ")
}

func nil2() *agentv1.LoadAverageHealth { return &agentv1.LoadAverageHealth{} }
