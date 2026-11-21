package jobsize

import (
	"context"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/luis-mc/ci-runners-shared-pool/internal/stackscan"
)

// Limits of one scan. The scan reads at most MaxReads files of a commit.
const (
	MaxReads         = 250
	MaxTestReads     = 120
	MaxFileBytes     = 256 << 10
	MaxWorkflows     = 60
	MaxConfigPerTool = 6
)

// Guess is the scan's size guess for one job.
type Guess struct {
	Job      string // the job key in the workflow
	Name     string // the display name, "" when none
	Workflow string // the workflow file
	// Peak is the guessed peak in CPUs and GiB; Weight is it in whole units.
	CPU, GiB   float64
	Weight     Weight
	Class      Class
	Confidence Confidence
	Tests      int // tests counted for the stack the job runs; 0 when none
	Workers    int
	Units      int
	Parallel   int
	Services   []string
	// EstSeconds is the time estimate when the table has a time per test; 0 if not.
	EstSeconds int
	// TimeoutMin and MaxParallel come from the workflow; 0 when absent.
	TimeoutMin  int
	MaxParallel int
	Reasons     []string
}

// Scan is the result for one repository at one commit.
type Scan struct {
	Repository string
	Commit     string
	Jobs       []Guess
	// File is the sizes: map of .crsp.yml, pattern to class, in file order.
	File []FileRule
	// FileError is set when .crsp.yml is there but could not be read.
	FileError string
}

// FileRule is one line of the sizes: map of .crsp.yml.
type FileRule struct {
	Pattern string
	Class   Class
}

// Scanner scans a commit through a stackscan.Reader.
type Scanner struct {
	R stackscan.Reader
}

type facts struct {
	files   []string
	dirs    map[string]bool
	tests   int
	cfg     string
	cfgSeen bool
}

type wfJob struct {
	Name     string `yaml:"name"`
	Timeout  any    `yaml:"timeout-minutes"`
	Strategy struct {
		Matrix      any `yaml:"matrix"`
		MaxParallel any `yaml:"max-parallel"`
	} `yaml:"strategy"`
	Services map[string]struct {
		Image string `yaml:"image"`
	} `yaml:"services"`
	Steps []struct {
		Run  string `yaml:"run"`
		Uses string `yaml:"uses"`
	} `yaml:"steps"`
}

var skipSeg = map[string]bool{
	"node_modules": true, "vendor": true, ".git": true, "dist": true, "build": true, "target": true,
	".venv": true, "venv": true, "third_party": true, ".gradle": true, "bin": true, "obj": true,
}

func skipped(p string) bool {
	for _, s := range strings.Split(p, "/") {
		if skipSeg[s] {
			return true
		}
	}
	return false
}

var (
	reWorkflow   = regexp.MustCompile(`^\.github/workflows/[^/]+\.ya?ml$`)
	reCsproj     = regexp.MustCompile(`\.csproj$`)
	reOpaque     = regexp.MustCompile(`(^|[\s;&|])(\./|bash\s|sh\s|make(\s|$)|just\s|task\s|\S+\.sh(\s|$))`)
	reCompose    = regexp.MustCompile(`docker[ -]compose\b.*\bup\b`)
	reImageLine  = regexp.MustCompile(`(?m)^\s*image:\s*['"]?([^\s'"#]+)`)
	reShard      = regexp.MustCompile(`--(?:shard|splits)[= ]+(?:[^\s/]*/)?(\S+)`)
	reTestcont   = regexp.MustCompile(`(?i)testcontainers`)
	reMatrixRef  = regexp.MustCompile(`matrix\.([A-Za-z0-9_-]+)`)
	reFullyPar   = regexp.MustCompile(`fullyParallel\s*:\s*true`)
	reNPMScript  = regexp.MustCompile(`^(?:npm|pnpm|yarn|bun)(?:\s+(?:run|-w\S*|--filter\s+\S+|-r))*\s+([A-Za-z0-9:_-]+)`)
	reScriptsKey = regexp.MustCompile(`"([A-Za-z0-9:_-]+)"\s*:\s*"((?:[^"\\]|\\.)*)"`)
)

// Scan lists the commit's files once, reads the files it needs and guesses
// each workflow job. It returns Scan with no Jobs when the repository has no
// workflow it can read.
func (s *Scanner) Scan(ctx context.Context, repository, commit string, table CostTable) (Scan, error) {
	out := Scan{Repository: repository, Commit: commit}
	blobs, err := s.R.Blobs(ctx, repository, commit)
	if err != nil {
		return out, err
	}
	sort.Slice(blobs, func(i, j int) bool { return blobs[i].Path < blobs[j].Path })
	reads := 0
	read := func(b stackscan.Blob) (string, bool) {
		if reads >= MaxReads || b.Size > MaxFileBytes {
			return "", false
		}
		reads++
		raw, err := s.R.File(ctx, repository, b.Path, commit)
		if err != nil {
			return "", false
		}
		return strings.ReplaceAll(string(raw), "\r\n", "\n"), true
	}

	var workflows, composes, manifests []stackscan.Blob
	var crsp *stackscan.Blob
	cfgFiles := map[string][]stackscan.Blob{}
	fx := map[string]*facts{}
	for _, st := range Stacks {
		fx[st.Key] = &facts{dirs: map[string]bool{}}
	}
	for i := range blobs {
		b := blobs[i]
		switch {
		case reWorkflow.MatchString(b.Path):
			workflows = append(workflows, b)
			continue
		case b.Path == ".crsp.yml":
			crsp = &blobs[i]
			continue
		}
		if skipped(b.Path) {
			continue
		}
		if k, ok := stackscan.Classify(b.Path); ok {
			switch k {
			case stackscan.KindCompose:
				composes = append(composes, b)
			case stackscan.KindManifest:
				manifests = append(manifests, b)
			}
		} else if reCsproj.MatchString(b.Path) && strings.Count(b.Path, "/") <= stackscan.MaxDepth {
			manifests = append(manifests, b)
		}
		for _, st := range Stacks {
			if st.Config != nil && st.Config.MatchString(b.Path) && len(cfgFiles[st.Key]) < MaxConfigPerTool {
				cfgFiles[st.Key] = append(cfgFiles[st.Key], b)
			}
		}
	}
	// Config text first: some stacks claim test files by it.
	pkgJSON := ""
	for _, st := range Stacks {
		f := fx[st.Key]
		for _, b := range cfgFiles[st.Key] {
			if txt, ok := read(b); ok {
				f.cfg += "\n" + txt
				f.cfgSeen = true
				if b.Path == "package.json" {
					pkgJSON = txt
				}
			}
		}
	}
	// Test files by stack.
	type tf struct {
		b  stackscan.Blob
		st int
	}
	var tests []tf
	claimed := map[string]bool{}
	for i, st := range Stacks {
		for _, b := range blobs {
			if skipped(b.Path) || !st.TestFile.MatchString(b.Path) || claimed[b.Path] {
				continue
			}
			if st.Claims != nil && !st.Claims(b.Path, fx[st.Key].cfg) {
				continue
			}
			if st.Key == "rust" && !strings.Contains(b.Path, "tests/") && !strings.HasPrefix(b.Path, "src/") && !strings.Contains(b.Path, "/src/") {
				continue
			}
			claimed[b.Path] = true
			f := fx[st.Key]
			f.files = append(f.files, b.Path)
			f.dirs[path.Dir(b.Path)] = true
			tests = append(tests, tf{b, i})
		}
	}
	// Count the tests of a sample of each stack's files and scale it up.
	for i, st := range Stacks {
		f := fx[st.Key]
		if len(f.files) == 0 {
			continue
		}
		if st.Marker == nil {
			f.tests = len(f.files)
			continue
		}
		var mine []stackscan.Blob
		for _, t := range tests {
			if t.st == i {
				mine = append(mine, t.b)
			}
		}
		step := 1
		if len(mine) > MaxTestReads {
			step = (len(mine) + MaxTestReads - 1) / MaxTestReads
		}
		sampled, count := 0, 0
		for j := 0; j < len(mine); j += step {
			txt, ok := read(mine[j])
			if !ok {
				continue
			}
			sampled++
			count += len(st.Marker.FindAllStringIndex(txt, -1))
		}
		switch {
		case sampled == 0:
			f.tests = len(f.files)
		case sampled == len(mine):
			f.tests = count
		default:
			f.tests = int(float64(count) * float64(len(mine)) / float64(sampled))
		}
	}
	// Manifests: testcontainers.
	testcontainers := false
	for _, b := range manifests {
		if txt, ok := read(b); ok && reTestcont.MatchString(txt) {
			testcontainers = true
			break
		}
	}
	scripts := map[string]string{}
	for _, m := range reScriptsKey.FindAllStringSubmatch(scriptsBlock(pkgJSON), -1) {
		scripts[m[1]] = strings.ToLower(m[2])
	}
	composeImages := []string{}
	if len(composes) > 0 {
		for _, b := range composes {
			if txt, ok := read(b); ok {
				for _, m := range reImageLine.FindAllStringSubmatch(txt, -1) {
					composeImages = append(composeImages, strings.ToLower(m[1]))
				}
			}
		}
	}

	if crsp != nil {
		if txt, ok := read(*crsp); ok {
			out.File, err = ParseSizes([]byte(txt))
			if err != nil {
				out.FileError = err.Error()
			}
		}
	}

	ctx0 := &jobCtx{table: table, fx: fx, scripts: scripts, testcontainers: testcontainers, composeImages: composeImages}
	for i, b := range workflows {
		if i >= MaxWorkflows {
			break
		}
		txt, ok := read(b)
		if !ok {
			continue
		}
		out.Jobs = append(out.Jobs, ctx0.workflow(b.Path, txt)...)
	}
	return out, nil
}

func scriptsBlock(pkg string) string {
	i := strings.Index(pkg, `"scripts"`)
	if i < 0 {
		return ""
	}
	rest := pkg[i:]
	if j := strings.Index(rest, "}"); j > 0 {
		return rest[:j]
	}
	return rest
}

type jobCtx struct {
	table          CostTable
	fx             map[string]*facts
	scripts        map[string]string
	testcontainers bool
	composeImages  []string
}

func (c *jobCtx) workflow(file, text string) []Guess {
	var doc yaml.Node
	if yaml.Unmarshal([]byte(text), &doc) != nil || len(doc.Content) == 0 {
		return nil
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil
	}
	var jobs *yaml.Node
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == "jobs" {
			jobs = root.Content[i+1]
		}
	}
	if jobs == nil || jobs.Kind != yaml.MappingNode {
		return nil
	}
	var out []Guess
	for i := 0; i+1 < len(jobs.Content); i += 2 {
		key, node := jobs.Content[i].Value, jobs.Content[i+1]
		var j wfJob
		if node.Decode(&j) != nil {
			continue
		}
		raw, _ := yaml.Marshal(node)
		if g, ok := c.guess(key, j, strings.ToLower(string(raw))); ok {
			g.Workflow = file
			out = append(out, g)
		}
	}
	return out
}

func toInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case string:
		if i, err := strconv.Atoi(strings.TrimSpace(n)); err == nil {
			return i
		}
	}
	return 0
}

func matrixOf(v any) map[string][]any {
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	out := map[string][]any{}
	for k, val := range m {
		if k == "include" || k == "exclude" {
			continue
		}
		if l, ok := val.([]any); ok {
			out[k] = l
		}
	}
	return out
}

// shardsOf reads the number of shards a job splits its tests into.
func shardsOf(runs string, matrix map[string][]any) int {
	if m := reShard.FindStringSubmatch(runs); m != nil {
		den := strings.Trim(m[1], `"'`)
		if n, err := strconv.Atoi(den); err == nil && n > 0 {
			return n
		}
		if r := reMatrixRef.FindStringSubmatch(den); r != nil {
			if l := matrix[r[1]]; len(l) == 1 {
				if n := toInt(l[0]); n > 0 {
					return n
				}
			}
			if l := matrix[r[1]]; len(l) > 1 {
				return len(l)
			}
		}
		for k, l := range matrix {
			if strings.Contains(strings.ToLower(k), "shard") && len(l) > 1 {
				return len(l)
			}
		}
	}
	return 1
}

func (c *jobCtx) guess(key string, j wfJob, jobText string) (Guess, bool) {
	g := Guess{Job: key, Name: j.Name, TimeoutMin: toInt(j.Timeout), MaxParallel: toInt(j.Strategy.MaxParallel), Confidence: ConfHigh}
	matrix := matrixOf(j.Strategy.Matrix)

	// The command lines of the job, with an npm script replaced by its text.
	var lines []string
	opaque := false
	for _, st := range j.Steps {
		for _, ln := range strings.Split(strings.ToLower(st.Run), "\n") {
			ln = strings.TrimSpace(ln)
			if ln == "" || strings.HasPrefix(ln, "#") {
				continue
			}
			if m := reNPMScript.FindStringSubmatch(ln); m != nil {
				if body, ok := c.scripts[m[1]]; ok {
					ln += " " + body
				}
			}
			lines = append(lines, ln)
		}
	}
	runs := strings.Join(lines, "\n")

	type hit struct {
		st      Stack
		cpu     float64
		gib     float64
		workers int
		units   int
		par     int
		tests   int
		reason  string
		sec     int
	}
	var hits []hit
	table := c.table
	for _, st := range Stacks {
		if !st.Run.MatchString(runs) {
			continue
		}
		if _, ok := table.Frameworks[st.Key]; !ok {
			continue
		}
		hits = append(hits, hit{st: st})
	}
	hint := false
	if len(hits) == 0 && reOpaque.MatchString(runs) {
		opaque = true
		if st, ok := c.hintStack(key + " " + j.Name); ok {
			hits = append(hits, hit{st: st})
			hint = true
		}
	}
	for i := range hits {
		h := &hits[i]
		st := h.st
		f := c.fx[st.Key]
		cost := table.Frameworks[st.Key]
		units := f.tests
		cfg := strings.ToLower(f.cfg)
		cfgRaw := f.cfg
		projects := 1
		if st.Projects != nil {
			projects = st.Projects(cfgRaw, runs)
		}
		shards := shardsOf(runs, matrix)
		if st.UnitsByDir {
			units = len(f.dirs)
		}
		if units > 0 {
			units = units * projects / shards
			if units < 1 {
				units = 1
			}
		}
		workers := st.DefaultWorkers
		if workers == 0 {
			workers = DefaultWorkers
		}
		src := "default"
		found := false
		if st.Serial != nil && st.Serial.MatchString(runs) {
			workers, src, found = 1, "serial flag", true
		}
		for _, w := range st.Workers {
			if found {
				break
			}
			if m := w.FindStringSubmatch(runs); m != nil {
				workers, src, found = atoi(m[1], workers), "command line", true
			}
		}
		if !found && st.Auto != nil && st.Auto.MatchString(runs) {
			workers, src, found = AutoWorkers, "auto", true
		}
		if !found && f.cfgSeen {
			if st.Serial != nil && st.Serial.MatchString(cfg) {
				workers, src, found = 1, "serial config", true
			}
			for _, w := range st.Workers {
				if found {
					break
				}
				if m := w.FindStringSubmatch(cfgRaw); m != nil {
					workers, src, found = atoi(m[1], workers), "config", true
				}
			}
		}
		parUnits := units
		if st.FileParallel && !reFullyPar.MatchString(cfgRaw) && len(f.files) > 0 {
			parUnits = len(f.files)
		}
		par := workers
		if parUnits > 0 && parUnits < par {
			par = parUnits
		}
		if par < 1 {
			par = 1
		}
		h.cpu, h.gib = float64(par)*cost.CPU, float64(par)*cost.GiB
		h.workers, h.units, h.par, h.tests = workers, units, par, f.tests
		if cost.SecPerTest > 0 && units > 0 {
			h.sec = int(float64(units) * cost.SecPerTest / float64(par))
		}
		h.reason = fmt.Sprintf("%s: %d tests x %d projects / %d shards = %d units; %d workers (%s); %d in parallel",
			st.Key, f.tests, projects, shards, units, workers, src, par)
		if f.tests == 0 {
			h.reason = fmt.Sprintf("%s: no tests counted; %d workers (%s)", st.Key, workers, src)
			g.Confidence = ConfLow
		}
	}

	// Services: the largest sequential step wins, services add on top.
	svcs := c.services(j, jobText, runs, len(hits) > 0)
	if len(hits) == 0 && len(svcs) == 0 {
		return g, false
	}
	var cpu, gib float64
	for _, h := range hits {
		if h.cpu > cpu {
			cpu = h.cpu
		}
		if h.gib > gib {
			gib = h.gib
		}
		g.Reasons = append(g.Reasons, h.reason)
		if h.tests > g.Tests {
			g.Tests, g.Workers, g.Units, g.Parallel = h.tests, h.workers, h.units, h.par
		}
		g.EstSeconds += h.sec
	}
	if len(hits) == 0 {
		cpu, gib = 1, 1
		g.Reasons = append(g.Reasons, "no test tool found; a base of 1 CPU and 1 GiB")
	}
	for _, k := range svcs {
		sc := table.Services[k]
		cpu += sc.CPU
		gib += sc.GiB
		g.Reasons = append(g.Reasons, fmt.Sprintf("service %s: %.2g CPU, %.2g GiB", k, sc.CPU, sc.GiB))
	}
	g.Services = svcs
	if opaque || hint {
		g.Confidence = ConfLow
		g.Reasons = append(g.Reasons, "a step runs a script, so what it runs is a guess")
	}
	g.CPU, g.GiB = cpu, gib
	g.Weight = WeightOfPeak(cpu, gib)
	g.Class = ClassOfPeak(cpu, gib)
	return g, true
}

func atoi(s string, def int) int {
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 {
		return def
	}
	return n
}

// hintStack picks a stack from the job name when the steps hide the command:
// e2e names Playwright; unit and test names take the stack with most tests.
func (c *jobCtx) hintStack(name string) (Stack, bool) {
	name = strings.ToLower(name)
	byKey := map[string]Stack{}
	for _, st := range Stacks {
		byKey[st.Key] = st
	}
	if strings.Contains(name, "e2e") || strings.Contains(name, "playwright") {
		if c.fx["playwright"].tests > 0 {
			return byKey["playwright"], true
		}
	}
	for _, w := range []string{"unit", "test", "integration", "spec"} {
		if !strings.Contains(name, w) {
			continue
		}
		best, n := "", 0
		for _, st := range Stacks {
			if st.Key == "playwright" {
				continue
			}
			if t := c.fx[st.Key].tests; t > n {
				best, n = st.Key, t
			}
		}
		if best != "" {
			return byKey[best], true
		}
	}
	return Stack{}, false
}

// services names the CostTable.Services keys a job starts.
func (c *jobCtx) services(j wfJob, jobText, runs string, hasTests bool) []string {
	seen := map[string]bool{}
	var out []string
	add := func(k string) {
		if _, ok := c.table.Services[k]; ok && !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	image := func(img string) {
		for _, r := range ImageRules {
			if r.Re.MatchString(strings.ToLower(img)) {
				add(r.Key)
				return
			}
		}
	}
	names := make([]string, 0, len(j.Services))
	for n := range j.Services {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		image(j.Services[n].Image)
	}
	if reCompose.MatchString(runs) {
		for _, img := range c.composeImages {
			image(img)
		}
	}
	if hasTests && c.testcontainers {
		add("testcontainers")
	}
	for _, r := range TextRules {
		if r.Re.MatchString(jobText) {
			add(r.Key)
		}
	}
	sort.Strings(out)
	return out
}

// ParseSizes reads the sizes: map of .crsp.yml, in file order. A class that is
// not light, normal or heavy is an error that names the pattern.
func ParseSizes(data []byte) ([]FileRule, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf(".crsp.yml is not valid YAML: %w", err)
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, nil
	}
	var out []FileRule
	m := doc.Content[0]
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value != "sizes" {
			continue
		}
		sz := m.Content[i+1]
		if sz.Kind != yaml.MappingNode {
			return nil, fmt.Errorf(".crsp.yml: sizes must be a map of job pattern to light, normal or heavy")
		}
		for k := 0; k+1 < len(sz.Content); k += 2 {
			pat, cl := sz.Content[k].Value, Class(strings.ToLower(sz.Content[k+1].Value))
			if !ValidClass(cl) {
				return out, fmt.Errorf(".crsp.yml: size of %q is %q; use light, normal or heavy", pat, sz.Content[k+1].Value)
			}
			out = append(out, FileRule{Pattern: pat, Class: cl})
		}
	}
	return out, nil
}
