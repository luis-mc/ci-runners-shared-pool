package main

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/github"
)

// sizeTrigger starts a job size scan when a job event brings a commit not seen
// yet (scheduling R5.84). It never blocks the webhook and never holds a job:
// one scan per repository runs at a time, the newest commit waits for it, and
// a scan that fails leaves the sizes as they were.
type sizeTrigger struct {
	mu      sync.Mutex
	scan    func(ctx context.Context, repository, commit string) error
	ctx     context.Context
	log     *slog.Logger
	seen    map[string]bool
	order   []string
	pending map[string]string
	running map[string]bool
}

const sizeTriggerSeen = 2048

func newSizeTrigger(ctx context.Context, log *slog.Logger) *sizeTrigger {
	return &sizeTrigger{ctx: ctx, log: log, seen: map[string]bool{}, pending: map[string]string{}, running: map[string]bool{}}
}

// set gives the trigger its scan; before that every Job call does nothing.
func (t *sizeTrigger) set(scan func(ctx context.Context, repository, commit string) error) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.scan = scan
	t.mu.Unlock()
}

// Job sees one workflow_job event.
func (t *sizeTrigger) Job(j github.Job) {
	if t == nil || j.Repository == "" || len(j.HeadSHA) < 40 {
		return
	}
	key := j.Repository + "@" + j.HeadSHA
	t.mu.Lock()
	if t.scan == nil || t.seen[key] {
		t.mu.Unlock()
		return
	}
	t.seen[key] = true
	t.order = append(t.order, key)
	if len(t.order) > sizeTriggerSeen {
		delete(t.seen, t.order[0])
		t.order = t.order[1:]
	}
	t.pending[j.Repository] = j.HeadSHA
	start := !t.running[j.Repository]
	t.running[j.Repository] = true
	t.mu.Unlock()
	if start {
		go t.run(j.Repository)
	}
}

func (t *sizeTrigger) run(repository string) {
	for {
		t.mu.Lock()
		sha, ok := t.pending[repository]
		delete(t.pending, repository)
		if !ok {
			delete(t.running, repository)
			t.mu.Unlock()
			return
		}
		scan := t.scan
		t.mu.Unlock()
		ctx, cancel := context.WithTimeout(github.Background(t.ctx), 2*time.Minute)
		if err := scan(ctx, repository, sha); err != nil && t.ctx.Err() == nil {
			t.log.Debug("job size scan skipped", "repository", repository, "err", err)
		}
		cancel()
	}
}
