// Package builds is the truth about image builds (IMG1, registry R5.68 to
// R5.72): it parses the real progress of a build, keeps each build as a record
// and says how many builds may run at once and on how many cores.
package builds

import (
	"regexp"
	"strconv"
	"strings"
)

// State of one step of a build.
const (
	StepRunning = "running"
	StepDone    = "done"
	StepCached  = "cached"
	StepFailed  = "failed"
)

// Progress is where a build is now (R5.68): the step n of m with its name and
// state, the layer or blob being fetched with its bytes, and the seconds the
// last finished step took. Total is 0 until the first numbered step appears.
type Progress struct {
	Step       int
	Total      int
	Name       string
	State      string
	Layer      string
	BytesDone  int64
	BytesTotal int64
	// StepSeconds is the time BuildKit reported for the step that just ended.
	StepSeconds float64
	// Elapsed is the seconds since the build began; the backend sets it.
	Elapsed float64
}

var (
	// `#5 [stage-1 3/9] RUN apt-get update` and `#5 [3/9] RUN ...`.
	reStep = regexp.MustCompile(`^#(\d+) \[(?:[^\]]*? )?(\d+)/(\d+)\] (.+)$`)
	// Classic builder: `Step 3/9 : RUN apt-get update`.
	reClassic = regexp.MustCompile(`^Step (\d+)/(\d+) : (.+)$`)
	reDone    = regexp.MustCompile(`^#(\d+) DONE ([0-9.]+)s$`)
	reCached  = regexp.MustCompile(`^#(\d+) CACHED$`)
	reError   = regexp.MustCompile(`^#(\d+) ERROR: (.*)$`)
	// `#5 sha256:0123abcd 12.3MB / 45.6MB 1.2s` (a blob being pulled).
	reBlob = regexp.MustCompile(`^#(\d+) sha256:([0-9a-f]{6,64}) ([0-9.]+)\s?([kKMGT]?i?B) / ([0-9.]+)\s?([kKMGT]?i?B)`)
)

// Parser reads the lines of `docker build --progress=plain` one at a time
// (R5.68). It is not safe for concurrent use.
type Parser struct {
	steps map[string]stepInfo // BuildKit vertex id -> its numbered step
	cur   Progress
}

type stepInfo struct {
	n, of int
	name  string
}

// NewParser starts a parser with no step seen.
func NewParser() *Parser { return &Parser{steps: map[string]stepInfo{}} }

// Current is the progress so far.
func (p *Parser) Current() Progress { return p.cur }

// Feed reads one line and reports whether the progress changed.
func (p *Parser) Feed(line string) (Progress, bool) {
	line = strings.TrimSpace(line)
	switch {
	case reStep.MatchString(line):
		m := reStep.FindStringSubmatch(line)
		n, _ := strconv.Atoi(m[2])
		of, _ := strconv.Atoi(m[3])
		p.steps[m[1]] = stepInfo{n: n, of: of, name: clip(m[4])}
		return p.begin(n, of, clip(m[4])), true
	case reClassic.MatchString(line):
		m := reClassic.FindStringSubmatch(line)
		n, _ := strconv.Atoi(m[1])
		of, _ := strconv.Atoi(m[2])
		return p.begin(n, of, clip(m[3])), true
	case reDone.MatchString(line):
		m := reDone.FindStringSubmatch(line)
		return p.end(m[1], StepDone, parseSeconds(m[2]))
	case reCached.MatchString(line):
		m := reCached.FindStringSubmatch(line)
		return p.end(m[1], StepCached, 0)
	case reError.MatchString(line):
		m := reError.FindStringSubmatch(line)
		if si, ok := p.steps[m[1]]; ok {
			p.cur.Step, p.cur.Total, p.cur.Name = si.n, si.of, si.name
		}
		p.cur.State = StepFailed
		return p.cur, true
	case reBlob.MatchString(line):
		m := reBlob.FindStringSubmatch(line)
		p.cur.Layer = m[2][:min(12, len(m[2]))]
		p.cur.BytesDone = parseBytes(m[3], m[4])
		p.cur.BytesTotal = parseBytes(m[5], m[6])
		return p.cur, true
	}
	return p.cur, false
}

// begin moves the build to step n. A step that comes back lower than one
// already seen (a second stage restarts its count) does not move it back.
func (p *Parser) begin(n, of int, name string) Progress {
	if n < p.cur.Step && p.cur.Total == of {
		return p.cur
	}
	p.cur = Progress{Step: n, Total: of, Name: name, State: StepRunning}
	return p.cur
}

func (p *Parser) end(id, state string, seconds float64) (Progress, bool) {
	si, ok := p.steps[id]
	if !ok || si.n != p.cur.Step || si.of != p.cur.Total {
		return p.cur, false // an internal step, or one of an earlier stage
	}
	p.cur.State = state
	p.cur.StepSeconds = seconds
	return p.cur, true
}

func clip(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 120 {
		return s[:120]
	}
	return s
}

func parseSeconds(s string) float64 {
	f, _ := strconv.ParseFloat(s, 64)
	return f
}

// parseBytes reads `12.3` `MB` into bytes. BuildKit prints decimal units.
func parseBytes(num, unit string) int64 {
	f, err := strconv.ParseFloat(num, 64)
	if err != nil {
		return 0
	}
	mult := float64(1)
	switch strings.ToUpper(strings.TrimSuffix(strings.TrimSuffix(strings.ToUpper(unit), "B"), "I")) {
	case "K":
		mult = 1e3
	case "M":
		mult = 1e6
	case "G":
		mult = 1e9
	case "T":
		mult = 1e12
	}
	if strings.Contains(strings.ToLower(unit), "i") {
		mult = map[string]float64{"K": 1 << 10, "M": 1 << 20, "G": 1 << 30, "T": 1 << 40}[strings.ToUpper(unit[:1])]
	}
	return int64(f * mult)
}
