package persistent

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"
)

// Runner.Listener writes one `_diag/Runner_<YYYYMMDD>-<HHMMSS>-utc.log` per
// start. A runner that GitHub still holds a session for refuses at once, so
// its newest logs all say so. These helpers read them: they fill the runner's
// wait reason without the agent keeping any state (runners R5.95).

var diagName = regexp.MustCompile(`^Runner_(\d{8}-\d{6})-utc\.log$`)

// DiagStart is the start time written in a listener log name.
func DiagStart(name string) (time.Time, bool) {
	m := diagName.FindStringSubmatch(name)
	if m == nil {
		return time.Time{}, false
	}
	t, err := time.ParseInLocation("20060102-150405", m[1], time.UTC)
	return t, err == nil
}

// SessionWait says what the listener logs of a runner folder show. refused is
// true when the newest log says a session already exists. until is the end of
// the wait window, counted from the first of the newest unbroken run of
// refusing logs, and is zero when the window is over (a real fault) or when
// nothing is refused.
func SessionWait(folder string, now time.Time, window time.Duration) (refused bool, until time.Time) {
	if window <= 0 {
		window = DefaultSessionWindow
	}
	dir := filepath.Join(folder, "_diag")
	ents, err := os.ReadDir(dir)
	if err != nil {
		return false, time.Time{}
	}
	type log struct {
		name  string
		start time.Time
	}
	var logs []log
	for _, e := range ents {
		if t, ok := DiagStart(e.Name()); ok && !e.IsDir() {
			logs = append(logs, log{e.Name(), t})
		}
	}
	sort.Slice(logs, func(i, j int) bool { return logs[i].start.After(logs[j].start) })
	var first time.Time
	for _, l := range logs {
		b, err := os.ReadFile(filepath.Join(dir, l.name))
		if err != nil || !IsSessionExists(string(b)) {
			break
		}
		first = l.start
	}
	if first.IsZero() {
		return false, time.Time{}
	}
	if end := first.Add(window); now.Before(end) {
		return true, end
	}
	return true, time.Time{}
}
