package trial

import (
	"context"
	"hash/fnv"
	"strings"
)

// CanaryPrefix starts every variable a canary writes. It sits under the pool
// prefix CRSP_RUNS_ON_ (routing.PoolVarPrefix), so no job takes the name as its
// per-job variable and the routing sync never deletes it as a stale one.
const CanaryPrefix = "CRSP_RUNS_ON_CANARY_"

// LegacyCanaryPrefix started the canary variables of older versions (before
// CRSP229); a canary started then is still stopped by name.
const LegacyCanaryPrefix = "RUNS_ON_CANARY_"

// Selected says whether a job name is in the canary. The choice depends on the
// job name alone, so it is the same on every run and only grows when the
// percentage grows: a job in at 10% is in at 50%.
func Selected(job string, percent int, named []string) bool {
	for _, n := range named {
		if strings.EqualFold(n, job) {
			return true
		}
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(strings.ToLower(strings.TrimSpace(job))))
	return int(h.Sum32()%100) < percent
}

// CanaryVariable is the variable name for a job: upper case letters and digits,
// every other character an underscore. A workflow reads it as
// `runs-on: ${{ fromJSON(vars.CRSP_RUNS_ON_CANARY_BUILD || '["ubuntu-latest"]') }}`.
func CanaryVariable(job string) string {
	var b strings.Builder
	b.WriteString(CanaryPrefix)
	for _, r := range strings.ToUpper(job) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}

var osVariable = map[string]string{"linux": "CRSP_RUNS_ON_LINUX", "windows": "CRSP_RUNS_ON_WINDOWS", "macos": "CRSP_RUNS_ON_MACOS"}

// applyCanary snapshots the CRSP_RUNS_ON family variables, then writes one variable
// per selected job through the owner code.
func (m *Manager) applyCanary(ctx context.Context, s *State) error {
	before, err := m.vars.Variables(ctx, s.Repo)
	if err != nil {
		return err
	}
	s.Before = before
	jobs := map[string]string{} // name -> os
	for n, os := range s.Seen {
		jobs[n] = os
	}
	for _, n := range s.Jobs {
		if _, ok := jobs[n]; !ok {
			jobs[n] = ""
		}
	}
	var picked []string
	for n := range jobs {
		if Selected(n, s.Percent, s.Jobs) {
			picked = append(picked, n)
		}
	}
	if len(picked) == 0 {
		return ErrNoJobs
	}
	want, err := m.vars.Desired(ctx, s.Repo)
	if err != nil {
		return err
	}
	sortStrings(picked)
	for _, n := range picked {
		val := want[osVariable[jobs[n]]]
		if val == "" {
			// OS unknown or not served: the first OS crsp serves.
			for _, os := range []string{"linux", "windows", "macos"} {
				if v := want[osVariable[os]]; v != "" {
					val = v
					break
				}
			}
		}
		if val == "" {
			return ErrNoJobs
		}
		name := CanaryVariable(n)
		s.Written = append(s.Written, name)
		if err := m.vars.Write(ctx, s.Repo, name, val); err != nil {
			return err
		}
	}
	return nil
}

// restore puts the variables back as the snapshot had them: a name the canary
// wrote is deleted, or set back to its earlier value when it existed.
func (m *Manager) restore(ctx context.Context, s *State) error {
	for _, name := range s.Written {
		var err error
		if old, ok := s.Before[name]; ok {
			err = m.vars.Write(ctx, s.Repo, name, old)
		} else {
			err = m.vars.Remove(ctx, s.Repo, name)
		}
		if err != nil {
			return err
		}
	}
	s.Written, s.Before = nil, nil
	return nil
}
