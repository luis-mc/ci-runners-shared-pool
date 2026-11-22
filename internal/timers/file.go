package timers

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// FileLimits are the per-job job timers of a repository's .crsp.yml (scheduling
// R5.103):
//
//	timers:
//	  jobs:
//	    integration-*: 45
//
// The key is a job key or a glob of it; the value is whole minutes above 0. The
// first line that matches wins, in file order.
type FileLimits struct {
	rules []fileRule
	// Errors say, in the owner's words, which lines were ignored.
	Errors []string
}

type fileRule struct {
	pattern string
	minutes int
}

// ParseFile reads the timers: map of .crsp.yml. A file with no timers: part
// gives no limits and no error; a line that is not whole minutes above 0 is
// ignored and named in Errors.
func ParseFile(data []byte) FileLimits {
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return FileLimits{Errors: []string{".crsp.yml is not valid YAML: " + err.Error()}}
	}
	if root.Kind != yaml.DocumentNode || len(root.Content) == 0 || root.Content[0].Kind != yaml.MappingNode {
		return FileLimits{}
	}
	var out FileLimits
	top := root.Content[0]
	for i := 0; i+1 < len(top.Content); i += 2 {
		if top.Content[i].Value != "timers" {
			continue
		}
		t := top.Content[i+1]
		if t.Kind != yaml.MappingNode {
			out.Errors = append(out.Errors, ".crsp.yml: timers must be a map with a jobs: part")
			continue
		}
		for j := 0; j+1 < len(t.Content); j += 2 {
			if t.Content[j].Value != "jobs" {
				continue
			}
			jobs := t.Content[j+1]
			if jobs.Kind != yaml.MappingNode {
				out.Errors = append(out.Errors, ".crsp.yml: timers.jobs must be a map of job to minutes")
				continue
			}
			for k := 0; k+1 < len(jobs.Content); k += 2 {
				pat, val := jobs.Content[k].Value, jobs.Content[k+1].Value
				var n int
				if _, err := fmt.Sscanf(val, "%d", &n); err != nil || n < 1 || fmt.Sprint(n) != strings.TrimSpace(val) {
					out.Errors = append(out.Errors, fmt.Sprintf(".crsp.yml: the job timer of %q is %q; use whole minutes above 0", pat, val))
					continue
				}
				out.rules = append(out.rules, fileRule{pat, n})
			}
		}
	}
	return out
}

// Minutes returns the minutes for a job key and whether a line matched.
func (f FileLimits) Minutes(job string) (int, bool) {
	for _, r := range f.rules {
		if r.pattern == job {
			return r.minutes, true
		}
		if ok, err := path.Match(r.pattern, job); err == nil && ok {
			return r.minutes, true
		}
	}
	return 0, false
}

// Patterns lists the jobs the file sets, sorted, for messages and tests.
func (f FileLimits) Patterns() []string {
	var out []string
	for _, r := range f.rules {
		out = append(out, r.pattern)
	}
	sort.Strings(out)
	return out
}

// LimitFor is the job timer of one job: the file's line when it matches and is
// below the run timer, else the default. The second result says why a line was
// not used, or "".
func LimitFor(set Settings, f FileLimits, job string) (minutes int, why string) {
	n, ok := f.Minutes(job)
	if !ok {
		return set.JobMin, ""
	}
	if n >= set.RunMin {
		return set.JobMin, fmt.Sprintf("the job timer of %q (%d min) is not below the run timer (%d min), so the default %d min is used", job, n, set.RunMin, set.JobMin)
	}
	return n, ""
}
