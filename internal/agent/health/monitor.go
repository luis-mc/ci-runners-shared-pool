package health

import (
	"context"
	"net/url"
	"strings"
	"sync"
	"time"

	agentv1 "github.com/luis-mc/ci-runners-shared-pool/internal/gen/agent/v1"
)

// Volume names a volume to measure; Role is system, data_dir, docker or runner_work.
type Volume struct{ Role, Path string }

// Config is what a Monitor needs. Env is required.
type Config struct {
	Env     Env
	Volumes []Volume
	// GitHubURL is the configured GitHub address (internal/addresses); the
	// reachability check sends a HEAD to it and resolves its host.
	GitHubURL    string
	AgentVersion string
	// RunnerVersion is the oldest installed runner app version; nil or "" is none.
	RunnerVersion func() string
	// Docker is the container engine kind and version; nil is none.
	Docker func() (kind, version string)
	// StateFile keeps the boot state; empty turns restart tracking off.
	StateFile string
	Beats     *Beats
	// Budget limits one cheap collector; 0 takes DefaultBudget.
	Budget time.Duration
	// Sync runs the slow collectors inside Collect (tests); otherwise they run in the background.
	Sync bool
	// Intervals of the slow collectors; 0 takes the defaults (GitHub 5 min,
	// Docker use 10 min, power 10 min, updates 10 min and, on macOS, 1 hour).
	GitHubEvery, DockerEvery, PowerEvery, UpdatesEvery time.Duration
}

// Monitor builds HealthReports.
type Monitor struct {
	cfg Config

	mu        sync.Mutex
	oomPrev   map[OOMTarget]int64
	oomLast   time.Time
	cpuPrev   cpuTimes
	havePrev  bool
	thermPrev int64
	haveTherm bool
	slow      map[string]*slowTask
}

// NewMonitor makes a Monitor.
func NewMonitor(cfg Config) *Monitor {
	if cfg.Budget == 0 {
		cfg.Budget = DefaultBudget
	}
	if cfg.Beats == nil {
		cfg.Beats = NewBeats(cfg.Env.Now)
	}
	def := func(p *time.Duration, d time.Duration) {
		if *p == 0 {
			*p = d
		}
	}
	def(&cfg.GitHubEvery, 5*time.Minute)
	def(&cfg.DockerEvery, 10*time.Minute)
	def(&cfg.PowerEvery, 10*time.Minute)
	upd := 10 * time.Minute
	if cfg.Env.GOOS() == "darwin" {
		upd = time.Hour
	}
	def(&cfg.UpdatesEvery, upd)
	return &Monitor{cfg: cfg, slow: map[string]*slowTask{}, oomPrev: map[OOMTarget]int64{}}
}

// Beats is the link figures the agent feeds.
func (m *Monitor) Beats() *Beats { return m.cfg.Beats }

type slowTask struct {
	mu      sync.Mutex
	last    time.Time
	running bool
	val     any
}

// slowRead returns the last value of a slow collector and starts a new read when
// it is due and none runs. It never waits for the read, except in Sync mode.
func (m *Monitor) slowRead(name string, every time.Duration, fn func() any) any {
	m.mu.Lock()
	t := m.slow[name]
	if t == nil {
		t = &slowTask{}
		m.slow[name] = t
	}
	m.mu.Unlock()
	now := m.cfg.Env.Now()
	t.mu.Lock()
	due := !t.running && (t.last.IsZero() || now.Sub(t.last) >= every)
	if due {
		t.running = true
	}
	t.mu.Unlock()
	if due {
		do := func() {
			v := fn()
			t.mu.Lock()
			t.val, t.last, t.running = v, now, false
			t.mu.Unlock()
		}
		if m.cfg.Sync {
			do()
		} else {
			go do()
		}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.val
}

const notYet = "not read yet; the first read of this slow item is still running"

// Collect builds one report.
func (m *Monitor) Collect(ctx context.Context) *agentv1.HealthReport {
	env, b := m.cfg.Env, m.cfg.Budget
	now := env.Now()
	r := &agentv1.HealthReport{SampledAtUnixMs: now.UnixMilli()}

	r.Boot = budgeted(ctx, b, func() *agentv1.BootHealth {
		var h *agentv1.BootHealth
		switch env.GOOS() {
		case "linux":
			h = linuxBoot(env)
		case "windows":
			h = windowsBoot(env)
		case "darwin":
			h = darwinBoot(env)
		default:
			h = &agentv1.BootHealth{UnknownReason: "uptime is not read on " + env.GOOS()}
		}
		if h.Known {
			h.UnexpectedRestart = trackBoot(env, m.cfg.StateFile, h.LastBootUnixMs)
		}
		return h
	}, func(why string) *agentv1.BootHealth { return &agentv1.BootHealth{UnknownReason: why} })

	for _, v := range m.cfg.Volumes {
		r.Volumes = append(r.Volumes, budgeted(ctx, b, func() *agentv1.VolumeHealth { return m.volume(v) },
			func(why string) *agentv1.VolumeHealth {
				return &agentv1.VolumeHealth{Role: v.Role, Path: v.Path, UnknownReason: why}
			}))
	}

	r.Swap = budgeted(ctx, b, func() *agentv1.SwapHealth {
		switch env.GOOS() {
		case "linux":
			return linuxSwap(env)
		case "windows":
			return windowsSwap(env)
		case "darwin":
			return darwinSwap(env)
		}
		return &agentv1.SwapHealth{UnknownReason: "swap is not read on " + env.GOOS()}
	}, func(why string) *agentv1.SwapHealth { return &agentv1.SwapHealth{UnknownReason: why} })

	r.Oom = budgeted(ctx, b, func() *agentv1.OomHealth { return m.oom(now) },
		func(why string) *agentv1.OomHealth { return &agentv1.OomHealth{UnknownReason: why} })

	r.Connectivity = m.connectivity()
	r.Versions = m.versions()

	r.Thermal = budgeted(ctx, b, func() *agentv1.ThermalHealth { return m.thermal() },
		func(why string) *agentv1.ThermalHealth { return &agentv1.ThermalHealth{UnknownReason: why} })
	r.LoadAverage = budgeted(ctx, b, func() *agentv1.LoadAverageHealth { return m.loadAverage() },
		func(why string) *agentv1.LoadAverageHealth {
			return &agentv1.LoadAverageHealth{LoadUnknownReason: why, StealUnknownReason: why}
		})

	// Slow items.
	if g, ok := m.slowRead("github", m.cfg.GitHubEvery, func() any { return m.githubProbe(ctx) }).(githubProbe); ok {
		c := r.Connectivity
		c.GithubKnown, c.GithubReachable, c.GithubLatencyMs, c.GithubCheckedUnixMs, c.GithubError = true, g.ok, g.latency.Milliseconds(), g.at.UnixMilli(), g.err
		c.DnsKnown, c.DnsOk, c.DnsError = true, g.dnsOK, g.dnsErr
	}
	if u, ok := m.slowRead("updates", m.cfg.UpdatesEvery, func() any { return m.updates() }).(*agentv1.UpdateHealth); ok {
		r.Updates = u
	} else {
		r.Updates = &agentv1.UpdateHealth{UnknownReason: notYet}
	}
	if p, ok := m.slowRead("power", m.cfg.PowerEvery, func() any { return m.power() }).(*agentv1.PowerHealth); ok {
		r.Power = p
	} else {
		r.Power = &agentv1.PowerHealth{SourceUnknownReason: notYet, SleepUnknownReason: notYet}
	}
	if d, ok := m.slowRead("docker", m.cfg.DockerEvery, func() any { return m.dockerUse() }).(*agentv1.DockerUseHealth); ok {
		r.DockerUse = d
	} else {
		r.DockerUse = &agentv1.DockerUseHealth{UnknownReason: notYet}
	}
	return r
}

func (m *Monitor) volume(v Volume) *agentv1.VolumeHealth {
	total, free, err := m.cfg.Env.Disk(v.Path)
	if err != nil {
		return &agentv1.VolumeHealth{Role: v.Role, Path: v.Path, UnknownReason: "the volume cannot be read: " + err.Error()}
	}
	return &agentv1.VolumeHealth{Role: v.Role, Path: v.Path, Known: true, TotalBytes: total, FreeBytes: free}
}

func (m *Monitor) oom(now time.Time) *agentv1.OomHealth {
	env := m.cfg.Env
	switch env.GOOS() {
	case "linux":
		counts, ok := linuxOOMCounts(env, DiscoverOOMTargets(env))
		if !ok {
			return &agentv1.OomHealth{UnknownReason: "no crsp cgroup with memory.events was found (cgroup v2 is needed)"}
		}
		m.mu.Lock()
		defer m.mu.Unlock()
		h := &agentv1.OomHealth{Known: true}
		for t, n := range counts {
			h.TotalKills += n
			h.Scopes = append(h.Scopes, &agentv1.OomScope{Scope: t.Scope, Name: t.Name, Kills: n})
			if prev, seen := m.oomPrev[t]; seen && n > prev {
				m.oomLast = now
			}
			m.oomPrev[t] = n
		}
		sortScopes(h.Scopes)
		h.LastKillUnixMs = m.oomLast.UnixMilli()
		if m.oomLast.IsZero() {
			h.LastKillUnixMs = 0
		}
		return h
	case "windows":
		return windowsOOM(env)
	}
	return &agentv1.OomHealth{UnknownReason: "macOS does not keep a count of jobs killed for memory that crsp can read"}
}

func (m *Monitor) connectivity() *agentv1.ConnectivityHealth {
	v := m.cfg.Beats.View()
	c := &agentv1.ConnectivityHealth{Reconnects_24H: int32(v.Reconnects24)} //nolint:gosec // a few reconnects
	if v.RTTKnown {
		c.RttKnown, c.RttMs = true, v.RTT.Milliseconds()
	}
	if !v.LastBeat.IsZero() {
		c.LastHeartbeatUnixMs = v.LastBeat.UnixMilli()
	}
	if v.SkewKnown {
		c.SkewKnown, c.ClockSkewSeconds = true, v.Skew.Seconds()
	}
	return c
}

func (m *Monitor) versions() *agentv1.VersionHealth {
	v := &agentv1.VersionHealth{AgentVersion: m.cfg.AgentVersion}
	if m.cfg.RunnerVersion != nil {
		v.RunnerAppVersion = m.cfg.RunnerVersion()
	}
	if m.cfg.Docker != nil {
		v.DockerKind, v.DockerVersion = m.cfg.Docker()
	}
	return v
}

func (m *Monitor) thermal() *agentv1.ThermalHealth {
	env := m.cfg.Env
	switch env.GOOS() {
	case "linux":
		m.mu.Lock()
		defer m.mu.Unlock()
		h, cur := linuxThermal(env, m.thermPrev, m.haveTherm)
		if h.Known {
			m.thermPrev, m.haveTherm = cur, true
		}
		return h
	case "windows":
		return &agentv1.ThermalHealth{UnknownReason: "Windows gives the CPU temperature only to an administrator and few PCs expose it"}
	}
	return &agentv1.ThermalHealth{UnknownReason: "macOS gives the CPU temperature and throttling only to an administrator (powermetrics) and not reliably"}
}

func (m *Monitor) loadAverage() *agentv1.LoadAverageHealth {
	env := m.cfg.Env
	switch env.GOOS() {
	case "linux":
		m.mu.Lock()
		defer m.mu.Unlock()
		cur, ok := linuxCPUTimes(env)
		pct, pok, reason := 0.0, false, "/proc/stat cannot be read"
		if ok {
			if m.havePrev && cur.total > m.cpuPrev.total {
				pct, pok, reason = 100*float64(cur.steal-m.cpuPrev.steal)/float64(cur.total-m.cpuPrev.total), true, ""
			} else {
				reason = "the first read has no earlier sample to compare with"
			}
			m.cpuPrev, m.havePrev = cur, true
		}
		return linuxLoad(env, &agentv1.LoadAverageHealth{}, pct, pok, reason)
	case "darwin":
		return darwinLoad(env)
	}
	return &agentv1.LoadAverageHealth{LoadUnknownReason: "Windows has no load average", StealUnknownReason: "Windows has no CPU steal figure"}
}

func (m *Monitor) updates() *agentv1.UpdateHealth {
	env := m.cfg.Env
	var u *agentv1.UpdateHealth
	switch env.GOOS() {
	case "linux":
		u = linuxUpdates(env)
	case "windows":
		u = windowsUpdates(env, 90*time.Second)
	case "darwin":
		u = darwinUpdates(env, 90*time.Second)
	default:
		u = &agentv1.UpdateHealth{UnknownReason: "updates are not read on " + env.GOOS()}
	}
	return u
}

func (m *Monitor) power() *agentv1.PowerHealth {
	switch m.cfg.Env.GOOS() {
	case "linux":
		return linuxPower(m.cfg.Env)
	case "windows":
		return windowsPower(m.cfg.Env)
	case "darwin":
		return darwinPower(m.cfg.Env)
	}
	return &agentv1.PowerHealth{SourceUnknownReason: "power is not read here", SleepUnknownReason: "power is not read here"}
}

type githubProbe struct {
	ok      bool
	latency time.Duration
	at      time.Time
	err     string
	dnsOK   bool
	dnsErr  string
}

func (m *Monitor) githubProbe(ctx context.Context) githubProbe {
	env := m.cfg.Env
	g := githubProbe{at: env.Now()}
	if u, err := url.Parse(m.cfg.GitHubURL); err == nil && u.Hostname() != "" {
		dctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		if derr := env.LookupHost(dctx, u.Hostname()); derr != nil {
			g.dnsErr = derr.Error()
		} else {
			g.dnsOK = true
		}
		cancel()
	} else {
		g.dnsErr = "no GitHub address is set"
	}
	hctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	start := env.Now()
	code, err := env.Head(hctx, m.cfg.GitHubURL)
	g.latency = env.Now().Sub(start)
	switch {
	case err != nil:
		g.err = err.Error()
	case code >= 500:
		g.err = "GitHub answered " + itoa(code)
	default:
		g.ok = true
	}
	return g
}

// dockerUse reads `docker system df`: images, build cache and volumes.
func (m *Monitor) dockerUse() *agentv1.DockerUseHealth {
	env := m.cfg.Env
	if m.cfg.Docker != nil {
		if kind, _ := m.cfg.Docker(); kind == "" {
			return &agentv1.DockerUseHealth{UnknownReason: "this machine has no container engine"}
		}
	}
	r, ok := env.RunTimeout([]string{"docker", "system", "df", "--format", "{{.Type}}|{{.Size}}"}, 60*time.Second)
	if !ok || r.Code != 0 {
		return &agentv1.DockerUseHealth{UnknownReason: "docker system df did not answer"}
	}
	h := parseDockerDF(r.Out)
	if h.Known {
		h.ReadUnixMs = env.Now().UnixMilli()
	}
	return h
}

func parseDockerDF(out string) *agentv1.DockerUseHealth {
	h := &agentv1.DockerUseHealth{}
	n := 0
	for _, l := range strings.Split(out, "\n") {
		typ, size, ok := strings.Cut(strings.TrimSpace(l), "|")
		if !ok {
			continue
		}
		b, bok := parseSize(size)
		if !bok {
			continue
		}
		switch strings.ToLower(typ) {
		case "images":
			h.ImagesBytes = b
		case "build cache":
			h.BuildCacheBytes = b
		case "local volumes":
			h.VolumesBytes = b
		case "containers":
			h.ContainersBytes = b
		default:
			continue
		}
		n++
	}
	if n == 0 {
		return &agentv1.DockerUseHealth{UnknownReason: "docker system df gave no sizes"}
	}
	h.Known = true
	return h
}
