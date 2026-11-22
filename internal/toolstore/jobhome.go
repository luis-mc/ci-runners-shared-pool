package toolstore

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// JobHome is the private, writable home of one job (step 4). It is the only place a job can write besides
// its checkout. Env is the full environment the job gets, KEY=VALUE, sorted; the store is only read.
type JobHome struct {
	Root      string // the job's folder, removed whole at the end
	Home      string
	CargoHome string
	Caches    string
	Temp      string
	Path      []string
	Env       []string
}

var jobIDShape = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z._-]{0,63}$`)

// BuildJobHome makes the job folder under jobsRoot and the environment from the plan. Every tool of the plan
// must be in the store (ErrNeedsInstall otherwise); each version used is marked as used now. base is the
// environment the job would otherwise get from the runner (system folders, runner variables): the store's
// variables replace its PATH, HOME and temp names; a variable of a manager home (NVM_DIR and the like) is
// dropped, because a job never runs a manager against the store (R5.117).
func (s *Store) BuildJobHome(jobsRoot, jobID string, p Plan, base map[string]string) (JobHome, error) {
	if !jobIDShape.MatchString(jobID) {
		return JobHome{}, fmt.Errorf("toolstore: %q is not a valid job id", jobID)
	}
	if miss := s.Missing(p); len(miss) > 0 {
		return JobHome{}, fmt.Errorf("%w: %s", ErrNeedsInstall, planString(miss))
	}
	g := s.GOOS
	root := filepath.Join(jobsRoot, jobID)
	jh := JobHome{Root: root, Home: filepath.Join(root, "home"), CargoHome: filepath.Join(root, "cargo"),
		Caches: filepath.Join(root, "caches"), Temp: filepath.Join(root, "tmp")}
	for _, d := range []string{jh.Home, jh.CargoHome, jh.Caches, jh.Temp} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return JobHome{}, err
		}
	}
	env := map[string]string{}
	for k, v := range base {
		if !managerVar(k) {
			env[k] = v
		}
	}
	sep := ":"
	var basePath []string
	if g == Windows {
		sep = ";"
	}
	for _, d := range strings.Split(base["PATH"], sep) {
		if d != "" {
			basePath = append(basePath, d)
		}
	}
	for _, t := range p {
		if t.Tool == ToolXcode {
			// Selected for this job only, in its private environment: no global xcode-select.
			x, err := s.Xcodes.Find(context.Background(), t.Version)
			if err != nil {
				return JobHome{}, err
			}
			env["DEVELOPER_DIR"] = x.DeveloperDir()
			continue
		}
		l := LayoutFor(g, t, s.Dir(t))
		jh.Path = append(jh.Path, l.Path...)
		for k, v := range l.Env {
			env[k] = v
		}
		_ = s.Touch(t)
	}
	jh.Path = append(jh.Path, basePath...)
	env["HOME"], env["CARGO_HOME"] = jh.Home, jh.CargoHome
	env["XDG_CACHE_HOME"] = jh.Caches
	env["PATH"] = strings.Join(jh.Path, sep)
	if g == Windows {
		env["USERPROFILE"], env["APPDATA"], env["LOCALAPPDATA"] = jh.Home, filepath.Join(jh.Home, "AppData", "Roaming"), filepath.Join(jh.Home, "AppData", "Local")
		env["TEMP"], env["TMP"] = jh.Temp, jh.Temp
		for _, d := range []string{env["APPDATA"], env["LOCALAPPDATA"]} {
			if err := os.MkdirAll(d, 0o700); err != nil {
				return JobHome{}, err
			}
		}
	} else {
		env["TMPDIR"] = jh.Temp
	}
	for k, v := range env {
		jh.Env = append(jh.Env, k+"="+v)
	}
	sort.Strings(jh.Env)
	return jh, nil
}

// managerVar says whether a variable points at a manager home; those are never passed to a job.
func managerVar(k string) bool {
	switch strings.ToUpper(k) {
	case "NVM_DIR", "NVM_HOME", "NVM_SYMLINK", "VOLTA_HOME", "PYENV", "PYENV_ROOT", "PYENV_HOME", "SDKMAN_DIR", "RUSTUP_INIT_SKIP_PATH_CHECK":
		return true
	}
	return false
}

func planString(p Plan) string {
	var out []string
	for _, t := range p {
		out = append(out, t.Tool+" "+t.Version)
	}
	return strings.Join(out, ", ")
}
