package dockercompact

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// prune is the Linux and macOS step: remove build cache and dangling images
// that nothing uses. It never removes a volume, a running container's data or
// an image a container uses, and has no disk-file step (caches R5.51 to R5.54).
func (c *run) prune(ctx context.Context) error {
	c.step("df")
	if used, ok := c.dockerUsed(ctx); ok {
		c.r.UsedBefore = used
		c.say("Docker uses %s", FormatBytes(used))
	}
	var failure error
	for _, args := range [][]string{
		{"builder", "prune", "--force"},
		{"image", "prune", "--force"},
	} {
		c.step(args[0] + " " + args[1])
		if _, err := c.exec(ctx, pruneTimeout, "docker", args...); err != nil {
			failure = errors.Join(failure, fmt.Errorf("docker %s %s: %w", args[0], args[1], err))
		}
	}
	if used, ok := c.dockerUsed(context.WithoutCancel(ctx)); ok {
		c.r.UsedAfter = used
		c.say("Docker uses %s", FormatBytes(used))
	}
	if res, err := c.h.Run(context.WithoutCancel(ctx), probeTimeout, "docker", "info"); err == nil && res.ExitCode == 0 {
		c.r.DockerRunning = true
	}
	return failure
}

// dockerUsed adds the Size column of `docker system df`.
func (c *run) dockerUsed(ctx context.Context) (int64, bool) {
	res, err := c.h.Run(ctx, probeTimeout, "docker", "system", "df", "--format", "{{.Size}}")
	if err != nil || res.ExitCode != 0 {
		return 0, false
	}
	var total int64
	n := 0
	for _, ln := range strings.Split(normalize(res.Out), "\n") {
		if ln = strings.TrimSpace(ln); ln == "" {
			continue
		}
		b, ok := ParseDockerSize(ln)
		if !ok {
			return 0, false
		}
		total += b
		n++
	}
	return total, n > 0
}

// ParseDockerSize reads "1.5GB", "300MB", "12kB" or "0B" (decimal units, as
// Docker prints them).
func ParseDockerSize(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	units := []struct {
		suffix string
		mul    float64
	}{{"TB", 1e12}, {"GB", 1e9}, {"MB", 1e6}, {"kB", 1e3}, {"KB", 1e3}, {"B", 1}}
	for _, u := range units {
		if num, ok := strings.CutSuffix(s, u.suffix); ok {
			f, err := strconv.ParseFloat(strings.TrimSpace(num), 64)
			if err != nil || f < 0 {
				return 0, false
			}
			return int64(f * u.mul), true
		}
	}
	return 0, false
}

// FormatBytes writes a size in GiB (or MiB when small) for notices and logs.
func FormatBytes(n int64) string {
	const gib = 1 << 30
	if n >= gib {
		return fmt.Sprintf("%.1f GiB", float64(n)/gib)
	}
	return fmt.Sprintf("%d MiB", n>>20)
}
