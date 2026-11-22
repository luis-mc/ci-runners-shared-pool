package agent

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/addresses"
	"github.com/luis-mc/ci-runners-shared-pool/internal/agent/health"
	agentv1 "github.com/luis-mc/ci-runners-shared-pool/internal/gen/agent/v1"
)

// healthBase adapts the agent's Sys to health.Base.
type healthBase struct{ Sys }

func (b healthBase) Run(argv []string) (health.Result, bool) {
	r, ok := b.Sys.Run(argv)
	return health.Result{Code: r.Code, Out: r.Out}, ok
}

// newHealth builds the Monitor of this agent (machines R5.72): the volumes that
// matter, the configured GitHub address and the engine of this machine.
func (c Config) newHealth() *health.Monitor {
	sys := c.sys()
	vols := []health.Volume{{Role: "system", Path: systemVolume(sys.GOOS())}}
	if c.DataDir != "" {
		vols = append(vols, health.Volume{Role: "data_dir", Path: c.DataDir})
	}
	if d := dockerDataPath(sys.GOOS()); d != "" {
		vols = append(vols, health.Volume{Role: "docker", Path: d})
	}
	for _, w := range c.RunnerWorkDirs {
		vols = append(vols, health.Volume{Role: "runner_work", Path: w})
	}
	gh := addresses.DefaultGitHubURL
	if a, err := addresses.Load(os.Getenv); err == nil && a.GitHubURL != "" {
		gh = a.GitHubURL
	}
	cfg := health.Config{
		Env: health.NewEnv(healthBase{sys}), Volumes: vols, GitHubURL: gh, AgentVersion: c.Version,
		StateFile: filepath.Join(c.DataDir, "health-state.json"),
		Docker: func() (string, string) {
			e := DetectEngine(sys)
			if !e.Usable {
				return "", ""
			}
			return string(e.Kind), e.Version
		},
	}
	if c.DataDir == "" {
		cfg.StateFile = ""
	}
	return health.NewMonitor(cfg)
}

func systemVolume(goos string) string {
	if goos == "windows" {
		if d := os.Getenv("SystemDrive"); d != "" {
			return d + `\`
		}
		return `C:\`
	}
	return "/"
}

// dockerDataPath is where Docker keeps its data, or "" when no such folder is known.
func dockerDataPath(goos string) string {
	switch goos {
	case "linux":
		return "/var/lib/docker"
	case "windows":
		if d := os.Getenv("LOCALAPPDATA"); d != "" {
			return filepath.Join(d, "Docker")
		}
	case "darwin":
		if h, err := os.UserHomeDir(); err == nil {
			return filepath.Join(h, "Library", "Containers", "com.docker.docker")
		}
	}
	return ""
}

// healthLoop sends a HealthReport right away and then every HealthEvery.
func (l *link) healthLoop(ctx context.Context) {
	if l.cfg.Health == nil {
		return
	}
	t := time.NewTicker(l.cfg.HealthEvery)
	defer t.Stop()
	for {
		l.reportHealth(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (l *link) reportHealth(ctx context.Context) {
	rep := l.cfg.Health.Collect(ctx)
	_ = l.send(&agentv1.SessionRequest{Message: &agentv1.SessionRequest_HealthReport{HealthReport: rep}})
}
