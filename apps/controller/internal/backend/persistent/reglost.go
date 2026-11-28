package persistent

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend/sys"
)

// A runner whose registration was deleted at GitHub (the runner was offline
// for too long, or somebody deleted it) starts, then its listener log says so
// and it ends: the supervisor, service or unit starts it again for ever. These
// helpers read the listener logs the same way SessionWait does, so the agent
// can tell the controller (runners R5.152).

// LostTexts are the pieces of the listener's own words that say the
// registration is gone, matched without case.
var LostTexts = []string{
	"registration has been deleted",
	"registration was not found",
}

// IsRegistrationLost says whether listener output says the registration is
// gone at GitHub.
func IsRegistrationLost(text string) bool {
	l := strings.ToLower(text)
	for _, t := range LostTexts {
		if strings.Contains(l, t) {
			return true
		}
	}
	return false
}

// RegistrationLost says whether the newest listener log of a runner folder
// that has any content says the registration was deleted at GitHub. A folder
// with no logs, or whose newest log is healthy, is not lost. A folder with no
// `.runner` file at all is not registered either (a half-done re-register).
func RegistrationLost(folder string) bool {
	if folder == "" {
		return false
	}
	dir := filepath.Join(folder, "_diag")
	ents, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	type log struct {
		name string
		at   int64
	}
	var logs []log
	for _, e := range ents {
		if t, ok := DiagStart(e.Name()); ok && !e.IsDir() {
			logs = append(logs, log{e.Name(), t.Unix()})
		}
	}
	sort.Slice(logs, func(i, j int) bool { return logs[i].at > logs[j].at })
	for _, l := range logs {
		b, err := os.ReadFile(filepath.Join(dir, l.name))
		if err != nil || len(b) == 0 {
			continue
		}
		return IsRegistrationLost(string(b))
	}
	return false
}

// StaleRegistrationFiles are the files `config.sh` leaves that hold the old
// registration; they are removed before a runner registers again in place.
var StaleRegistrationFiles = []string{".runner", ".credentials", ".credentials_rsaparams"}

// RegistrationLostIn is RegistrationLost read through the backend's own file
// system (a Windows backend may run on another machine than the controller).
// A file system that cannot list a folder says not lost.
func RegistrationLostIn(fsys sys.FS, folder string, join func(...string) string) bool {
	dr, ok := fsys.(sys.DirReader)
	if !ok || folder == "" {
		return false
	}
	dir := join(folder, "_diag")
	names, err := dr.ReadDir(dir)
	if err != nil {
		return false
	}
	type log struct {
		name string
		at   int64
	}
	var logs []log
	for _, n := range names {
		if t, ok := DiagStart(n); ok {
			logs = append(logs, log{n, t.Unix()})
		}
	}
	sort.Slice(logs, func(i, j int) bool { return logs[i].at > logs[j].at })
	for _, l := range logs {
		b, err := fsys.ReadFile(join(dir, l.name))
		if err != nil || len(b) == 0 {
			continue
		}
		return IsRegistrationLost(string(b))
	}
	return false
}
