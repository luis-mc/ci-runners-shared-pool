package agent

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/machines"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runnercache"
)

// DefaultBudgetEvery is how often the pool-wide cache budget is checked.
const DefaultBudgetEvery = 10 * time.Minute

// alertSet remembers which paths were reported, so one big file is one alert
// until it falls under the threshold again.
type alertSet struct {
	mu   sync.Mutex
	seen map[string]bool
}

// fresh returns the paths in current that were not reported yet and remembers
// current as the reported set.
func (a *alertSet) fresh(current map[string]bool) []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []string
	for p := range current {
		if !a.seen[p] {
			out = append(out, p)
		}
	}
	a.seen = current
	return out
}

// budgetEnv reads the pool-wide values from the runners' own .env (all runners
// of a pool carry the same ones). Zero means no runner was set up by the
// product with them, so nothing is enforced.
func budgetEnv(runners []CacheRunner) (budget, alert int64) {
	for _, r := range runners {
		kv := readEnvFile(r.Folder)
		if b, err := strconv.Atoi(kv[runnercache.EnvBudget]); err == nil && b > 0 && budget == 0 {
			budget = int64(b) * runnercache.GiB
		}
		if a, err := strconv.Atoi(kv[runnercache.EnvAlert]); err == nil && a > 0 && alert == 0 {
			alert = int64(a) * runnercache.GiB
		}
	}
	return budget, alert
}

func readEnvFile(folder string) map[string]string {
	kv := map[string]string{}
	b, err := os.ReadFile(filepath.Join(folder, ".env")) //nolint:gosec // the runner folder the agent made
	if err != nil {
		return kv
	}
	for _, ln := range strings.Split(string(b), "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(ln), "="); ok {
			kv[k] = strings.TrimSpace(v)
		}
	}
	return kv
}

// Budgeter runs the budget passes of one agent and remembers its alerts.
type Budgeter struct {
	cfg    Config
	holder RunnerHolder
	alerts alertSet
}

// RunnerHolder is the part of the gate a budget pass uses: keep new jobs out of
// one runner and say whether it is idle, in one step (caches R5.32).
type RunnerHolder interface {
	HoldRunner(name string) (idle bool, waitingFor []machines.JobRef)
	ReleaseRunner(name string)
}

// NewBudgeter makes a Budgeter that reads the runners, the gate and the file
// access from cfg.
func NewBudgeter(cfg Config) *Budgeter {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	b := &Budgeter{cfg: cfg}
	if cfg.Gate != nil {
		b.holder = cfg.Gate
	}
	return b
}

// WithHolder replaces the gate, for tests.
func (b *Budgeter) WithHolder(h RunnerHolder) *Budgeter { b.holder = h; return b }

// Pass is one pass of the pool-wide cache disk budget (caches R5.48).
// It raises the size alerts first; then, when all cache folders of this machine
// pass the budget, it evicts the oldest entries across runners. A runner with
// a job running is never touched: each runner is held (new jobs wait) and
// checked for idle in one step, and a busy one is skipped. It returns the log
// lines it wrote, for tests.
func (b *Budgeter) Pass(ctx context.Context) []string {
	var lines []string
	say := func(warn bool, msg string, kv ...any) {
		lines = append(lines, msg)
		if warn {
			b.cfg.Logger.Warn(msg, kv...)
		} else {
			b.cfg.Logger.Info(msg, kv...)
		}
	}
	if b.cfg.Caches == nil || b.holder == nil {
		return nil
	}
	runners, err := b.cfg.Caches.Runners(ctx)
	if err != nil || len(runners) == 0 {
		return nil
	}
	fsys := b.cfg.cacheFS()
	budget, alert := budgetEnv(runners)

	// Size alert: a single cache file or folder past the threshold.
	current := map[string]bool{}
	sizes := map[string]int64{}
	if alert > 0 {
		for _, r := range runners {
			d := runnercache.DirsFor(r.Folder)
			for _, dir := range []string{d.Tools, d.Rust} {
				over, err := runnercache.FindOversize(fsys, dir, alert)
				if err != nil {
					continue
				}
				for _, o := range over {
					current[o.Path] = true
					sizes[o.Path] = o.Bytes
				}
			}
		}
	}
	for _, p := range b.alerts.fresh(current) {
		say(true, "cache size alert: one cache item is past the size limit", "path", p, "mib", sizes[p]>>20, "limit_mib", alert>>20)
	}

	if budget <= 0 {
		return lines
	}
	br := make([]runnercache.BudgetRunner, len(runners))
	for i, r := range runners {
		br[i] = runnercache.BudgetRunner{Name: r.Name, Folder: r.Folder}
	}
	total, err := runnercache.BudgetTotal(fsys, br)
	if err != nil || total <= budget {
		return lines
	}
	// Over budget: hold each runner and check it is idle in one step.
	for i := range br {
		idle, _ := b.holder.HoldRunner(br[i].Name)
		br[i].Busy = !idle
	}
	defer func() {
		for _, r := range br {
			b.holder.ReleaseRunner(r.Name)
		}
	}()
	res, err := runnercache.EnforceBudget(fsys, br, budget)
	for _, ln := range res.Lines() {
		say(res.Over, ln)
	}
	if err != nil {
		say(true, "cache budget: eviction stopped", "err", err.Error())
	}
	return lines
}

// budgetLoop checks the budget every DefaultBudgetEvery (or Config.BudgetEvery)
// until the session ends. The first check waits one period, so a machine that
// just started does not evict before the runners say they are up.
func (l *link) budgetLoop(ctx context.Context) {
	every := l.cfg.BudgetEvery
	if every <= 0 {
		every = DefaultBudgetEvery
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			l.budget.Pass(ctx)
		}
	}
}
