package routing

import (
	"bytes"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/doctor/runson"
)

// RoutedJob is a job whose runs-on reads a pool variable (CRSP_RUNS_ON_<OS>, or a
// size variable such as CRSP_RUNS_ON_LINUX_LARGE), or already a per-job variable.
// A job that still reads a name older versions wrote (RUNS_ON_<OS>,
// RUNS_ON_<OS>_<SIZE>) is routed too: Base is then the new name and OldBase is
// set, so the patch rewrites it to the new name.
type RoutedJob struct {
	Path string
	Key  string
	Line int
	OS   string
	// Base is the pool variable the job falls back to, "" when none.
	Base string
	// OldBase is true when the runs-on reads the old name of Base (RUNS_ON_<OS>).
	OldBase bool
	// PerJob is true when the runs-on already reads a CRSP_ variable first.
	PerJob bool
	// Default is the labels after the last ||, empty when the job has none.
	Default []string
}

// sizeVarRE is a pool variable: CRSP_RUNS_ON_<OS> or CRSP_RUNS_ON_<OS>_<SIZE>,
// or the same without CRSP_, the name older versions wrote (CRSP229).
var sizeVarRE = regexp.MustCompile(`^(CRSP_)?RUNS_ON_(LINUX|WINDOWS|MACOS)(_[A-Z][A-Z0-9]*)?$`)

// Routed reads the routed jobs of a set of workflow files, sorted by path and
// line. A job with literal labels, a matrix value or no variable is not routed.
func Routed(files map[string][]byte) []RoutedJob {
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	var out []RoutedJob
	for _, p := range paths {
		jobs, err := runson.Parse(p, files[p])
		if err != nil {
			continue
		}
		for _, j := range jobs {
			if j.Kind != runson.Variable {
				continue
			}
			rj := RoutedJob{Path: p, Key: j.Job, Line: j.Line, Default: j.Labels}
			chain := j.Vars
			if len(chain) == 0 {
				chain = []string{j.Var}
			}
			for _, v := range chain {
				if m := sizeVarRE.FindStringSubmatch(v); m != nil {
					rj.OS, rj.Base, rj.OldBase = strings.ToLower(m[2]), PoolVarPrefix+m[2]+m[3], m[1] == ""
				} else if IsJobVar(v) {
					rj.PerJob = true
				}
			}
			if rj.Base == "" {
				continue
			}
			out = append(out, rj)
		}
	}
	return out
}

// Change is one runs-on line the patch rewrites.
type Change struct {
	Job  string
	Line int
	From string
	To   string
}

// FilePatch is the new content of one workflow file.
type FilePatch struct {
	Path    string
	Old     []byte
	New     []byte
	Changes []Change
}

// Patch is the one-time workflow change: every routed runs-on becomes the
// per-job form, with the pool variable and a hosted label as fallback.
type Patch struct {
	Files []FilePatch
	// Skipped lists routed jobs whose runs-on is not on one line, so the
	// patch leaves them as they are ("path: job").
	Skipped []string
}

// Empty says whether the patch changes nothing.
func (p Patch) Empty() bool { return len(p.Files) == 0 }

var hostedFor = map[string]string{"linux": "ubuntu-latest", "windows": "windows-latest", "macos": "macos-latest"}

// Expression is the per-job runs-on: fromJSON(vars.CRSP_E2E || vars.CRSP_RUNS_ON_LINUX || '"ubuntu-latest"').
// A default the job already had stays the last fallback.
func Expression(varName, base, os string, def []string) string {
	last := `'"` + hostedFor[os] + `"'`
	if len(def) > 0 {
		last = "'" + jsonList(def) + "'"
	}
	return "${{ fromJSON(vars." + varName + " || vars." + base + " || " + last + ") }}"
}

func jsonList(l []string) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, s := range l {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, "%q", s)
	}
	b.WriteByte(']')
	return b.String()
}

var runsOnLineRE = regexp.MustCompile(`^(\s*(?:-\s+)?runs-on:\s*)(\S.*?)(\s+#.*)?$`)

// MakePatch builds the patch for the workflow files of a repository. Variable
// names come from VarNames over every routed job key, so the patch and the
// variables the sync writes always agree.
func MakePatch(files map[string][]byte) Patch {
	routed := Routed(files)
	keys := make([]string, 0, len(routed))
	for _, r := range routed {
		keys = append(keys, r.Key)
	}
	names := VarNames(keys)
	byPath := map[string][]RoutedJob{}
	for _, r := range routed {
		byPath[r.Path] = append(byPath[r.Path], r)
	}
	var patch Patch
	paths := make([]string, 0, len(byPath))
	for p := range byPath {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		old := files[p]
		lines := strings.Split(string(old), "\n")
		fp := FilePatch{Path: p, Old: old}
		for _, r := range byPath[p] {
			if r.PerJob && !r.OldBase {
				continue
			}
			if r.Line < 1 || r.Line > len(lines) {
				patch.Skipped = append(patch.Skipped, p+": "+r.Key)
				continue
			}
			ln := strings.TrimRight(lines[r.Line-1], "\r")
			m := runsOnLineRE.FindStringSubmatch(ln)
			if m == nil || !strings.Contains(m[2], "${{") {
				patch.Skipped = append(patch.Skipped, p+": "+r.Key)
				continue
			}
			to := m[1] + Expression(names[r.Key], r.Base, r.OS, r.Default) + m[3]
			lines[r.Line-1] = to + strings.TrimPrefix(lines[r.Line-1], ln)
			fp.Changes = append(fp.Changes, Change{Job: r.Key, Line: r.Line, From: ln, To: to})
		}
		if len(fp.Changes) > 0 {
			fp.New = []byte(strings.Join(lines, "\n"))
			patch.Files = append(patch.Files, fp)
		}
	}
	return patch
}

// Diff writes the patch as a unified diff that git apply takes, three lines
// of context. Every change replaces one line, so hunks never shift.
func (p Patch) Diff() string {
	var b bytes.Buffer
	for _, f := range p.Files {
		old := strings.Split(string(f.Old), "\n")
		if len(old) > 0 && old[len(old)-1] == "" {
			old = old[:len(old)-1]
		}
		fmt.Fprintf(&b, "diff --git a/%s b/%s\n--- a/%s\n+++ b/%s\n", f.Path, f.Path, f.Path, f.Path)
		newText := strings.Split(string(f.New), "\n")
		const ctx = 3
		type hunk struct{ from, to int } // 0-based inclusive line range
		var hunks []hunk
		for _, c := range f.Changes {
			lo, hi := c.Line-1-ctx, c.Line-1+ctx
			if lo < 0 {
				lo = 0
			}
			if hi > len(old)-1 {
				hi = len(old) - 1
			}
			if n := len(hunks); n > 0 && lo <= hunks[n-1].to+1 {
				hunks[n-1].to = hi
				continue
			}
			hunks = append(hunks, hunk{lo, hi})
		}
		for _, h := range hunks {
			n := h.to - h.from + 1
			fmt.Fprintf(&b, "@@ -%d,%d +%d,%d @@\n", h.from+1, n, h.from+1, n)
			for i := h.from; i <= h.to; i++ {
				if i < len(newText) && newText[i] != old[i] {
					fmt.Fprintf(&b, "-%s\n+%s\n", old[i], newText[i])
				} else {
					fmt.Fprintf(&b, " %s\n", old[i])
				}
			}
		}
	}
	return b.String()
}
