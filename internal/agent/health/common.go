package health

import (
	"sort"

	"context"
	"fmt"
	agentv1 "github.com/luis-mc/ci-runners-shared-pool/internal/gen/agent/v1"
	"strconv"
	"strings"
	"time"
)

// DefaultBudget is the time one cheap collector may take.
const DefaultBudget = 5 * time.Second

// budgeted runs fn and gives up after d, so one stuck command never holds the
// report; onTimeout builds the unknown value.
func budgeted[T any](ctx context.Context, d time.Duration, fn func() T, onTimeout func(reason string) T) T {
	ctx, cancel := context.WithTimeout(ctx, d)
	defer cancel()
	ch := make(chan T, 1)
	go func() { ch <- fn() }()
	select {
	case v := <-ch:
		return v
	case <-ctx.Done():
		return onTimeout(fmt.Sprintf("the read took longer than %s", d))
	}
}

func run(env Env, argv ...string) (string, bool) {
	r, ok := env.Run(argv)
	if !ok || r.Code != 0 {
		return r.Out, false
	}
	return r.Out, true
}

func atoi64(s string) (int64, bool) {
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	return n, err == nil
}

func atof(s string) (float64, bool) {
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return f, err == nil
}

// kv reads "Key: value" lines (/proc/meminfo style) into a map.
func kv(txt, sep string) map[string]string {
	m := map[string]string{}
	for _, l := range strings.Split(txt, "\n") {
		if k, v, ok := strings.Cut(l, sep); ok {
			m[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return m
}

// kib parses "1234 kB".
func kib(s string) (int64, bool) {
	f := strings.Fields(s)
	if len(f) == 0 {
		return 0, false
	}
	n, ok := atoi64(f[0])
	return n * 1024, ok
}

// parseSize reads Docker sizes such as "1.234GB", "12.3MB", "500kB", "0B".
func parseSize(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, ' '); i > 0 {
		s = s[:i]
	}
	units := []struct {
		suf string
		mul float64
	}{{"TB", 1e12}, {"GB", 1e9}, {"MB", 1e6}, {"kB", 1e3}, {"KB", 1e3}, {"B", 1}}
	for _, u := range units {
		if strings.HasSuffix(s, u.suf) {
			f, err := strconv.ParseFloat(strings.TrimSuffix(s, u.suf), 64)
			if err != nil {
				return 0, false
			}
			return int64(f * u.mul), true
		}
	}
	return 0, false
}

func itoa(n int) string { return strconv.Itoa(n) }

func sortScopes(s []*agentv1.OomScope) {
	sort.Slice(s, func(i, j int) bool {
		if s[i].Scope != s[j].Scope {
			return s[i].Scope < s[j].Scope
		}
		return s[i].Name < s[j].Name
	})
}
