package windows

import (
	"context"
	"strings"
	"sync"
	"time"
)

// A runner service that stops, as at a boot that came before crsp could give
// the runner group its right to list the runners folder, is started again by
// the heal pass (runners R5.178). A service that keeps failing is not tried on
// every pass: the wait grows from one pass to an hour, and one success starts
// over.

const (
	healBackoffBase = 10 * time.Minute
	healBackoffMax  = time.Hour
)

type healFail struct {
	failures int
	next     time.Time
	problem  string
}

type healBackoff struct {
	mu sync.Mutex
	by map[string]healFail
}

// wait returns the problem of the last failed start while the service is
// still in its backoff; ok is false when it may be tried now.
func (h *healBackoff) wait(service string, now time.Time) (problem string, ok bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	f, found := h.by[service]
	if !found || !now.Before(f.next) {
		return "", false
	}
	return f.problem, true
}

// failed records a failed start and returns the wait before the next one.
func (h *healBackoff) failed(service string, now time.Time, problem string) time.Duration {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.by == nil {
		h.by = map[string]healFail{}
	}
	f := h.by[service]
	f.failures++
	d := healBackoffBase
	for i := 1; i < f.failures && d < healBackoffMax; i++ {
		d *= 2
	}
	if d > healBackoffMax {
		d = healBackoffMax
	}
	f.next, f.problem = now.Add(d), problem
	h.by[service] = f
	return d
}

func (h *healBackoff) ok(service string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.by, service)
}

func (b *Backend) clock() time.Time {
	if b.now != nil {
		return b.now()
	}
	return time.Now()
}

// scriptWhyStopped reads the newest reason the runner service wrote to the
// Application log for its folder, or the Service Control Manager's line.
// Needs CRSP_SERVICE. Prints nothing when it finds none.
const scriptWhyStopped = scriptHeader + `
$w = Get-CimInstance -ClassName Win32_Service -Filter ("Name='" + $env:CRSP_SERVICE.Replace("'", "''") + "'") -ErrorAction SilentlyContinue
$dir = ''
if ($w -and $w.PathName) { $dir = Split-Path -Parent (Split-Path -Parent ($w.PathName.Trim('"'))) }
$ev = Get-WinEvent -FilterHashtable @{ LogName = 'Application'; ProviderName = 'ActionsRunnerService'; StartTime = (Get-Date).AddHours(-12) } -ErrorAction SilentlyContinue |
    Where-Object { $_.Message -like '*An error occurred*' -and ((-not $dir) -or $_.Message -like ('*' + $dir + '*')) } | Select-Object -First 1
if ($ev) { Write-Output ($ev.Message -replace '\s+', ' '); exit 0 }
$ev = Get-WinEvent -FilterHashtable @{ LogName = 'System'; ProviderName = 'Service Control Manager'; StartTime = (Get-Date).AddHours(-12) } -ErrorAction SilentlyContinue |
    Where-Object { $_.Message -like ('*' + $env:CRSP_SERVICE + '*') } | Select-Object -First 1
if ($ev) { Write-Output ($ev.Message -replace '\s+', ' ') }
`

// whyStopped is the reason a stopped service gave, best effort and short.
func (b *Backend) whyStopped(ctx context.Context, service string) string {
	res, err := b.ps(ctx, scriptWhyStopped, map[string]string{"CRSP_SERVICE": service}, nil)
	if err != nil || res.ExitCode != 0 {
		return ""
	}
	why := strings.TrimSpace(b.redact(res.Stdout))
	if len(why) > 300 {
		why = why[:300] + "..."
	}
	return why
}
