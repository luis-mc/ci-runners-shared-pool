package jobsize

import (
	"regexp"
	"strings"
)

// Stack is one test framework family, as a table row. A new stack is a new
// row in Stacks, with no new code (scheduling R5.81).
type Stack struct {
	// Key is the CostTable.Frameworks key.
	Key string
	// Run matches a lower-case command line of a job step that runs the tool.
	Run *regexp.Regexp
	// TestFile matches the repository path of a test file.
	TestFile *regexp.Regexp
	// Claims, when set, narrows TestFile: it gets the path and the text of the
	// stack's config files (Playwright and Vitest share file names).
	Claims func(path, cfg string) bool
	// Marker counts the tests in a test file; nil counts one per file.
	Marker *regexp.Regexp
	// UnitsByDir counts a test directory as one unit (a Go package).
	UnitsByDir bool
	// Config matches the config files to read.
	Config *regexp.Regexp
	// Workers are tried in order on the command lines, then on the config;
	// group 1 is the number.
	Workers []*regexp.Regexp
	// Auto matches a command that asks for as many workers as CPUs.
	Auto *regexp.Regexp
	// Serial matches a command or config that runs one worker.
	Serial *regexp.Regexp
	// Projects counts the projects or devices of the config; nil means 1.
	Projects func(cfg string, runs string) int
	// FileParallel is true when tests in one file share a worker unless the
	// config says fullyParallel (Playwright).
	FileParallel bool
	// DefaultWorkers is the worker count when nothing says; 0 means DefaultWorkers.
	DefaultWorkers int
}

// AutoWorkers is the worker count of `-n auto` and the like: an assumption.
const AutoWorkers = 4

func rx(s string) *regexp.Regexp { return regexp.MustCompile(s) }

var (
	reJSTest    = rx(`\.(spec|test)\.(ts|tsx|js|jsx|mjs|cjs|mts|cts)$`)
	reJSMarker  = rx(`(?m)^\s*(?:test|it)(?:\.(?:only|skip|fixme|fail|slow|concurrent|each))*\s*\(`)
	rePWTestDir = rx(`testDir\s*:\s*(?:path\.\w+\([^,)]*,\s*)?['"]\.?/?([^'"]+)['"]`)
	rePWProj    = rx(`\bname\s*:\s*['"]`)
)

// Stacks is the table of frameworks, Playwright, Vitest/Jest and Go first.
var Stacks = []Stack{
	{
		Key:      "playwright",
		Run:      rx(`\bplaywright\b|test:e2e`),
		TestFile: reJSTest,
		Claims: func(p, cfg string) bool {
			if m := rePWTestDir.FindStringSubmatch(cfg); m != nil {
				d := strings.TrimSuffix(m[1], "/")
				return d == "." || strings.HasPrefix(p, d+"/")
			}
			return strings.HasPrefix(p, "e2e/") || strings.Contains(p, "/e2e/") || strings.Contains(p, "playwright")
		},
		Marker:   reJSMarker,
		Config:   rx(`(^|/)playwright[A-Za-z0-9._-]*\.config\.(ts|js|mjs|cjs|mts)$`),
		Workers:  []*regexp.Regexp{rx(`--workers[= ]+(\d+)`), rx(`\s-j[= ]*(\d+)`), rx(`workers\s*:\s*(?:[^,\n?:]*\?\s*)?(\d+)`)},
		Serial:   rx(`workers\s*:\s*1\b|--workers[= ]+1\b`),
		Projects: playwrightProjects,
		// A file is one unit of parallelism unless fullyParallel is true.
		FileParallel: true,
	},
	{
		Key:      "vitest",
		Run:      rx(`\b(vitest|jest)\b`),
		TestFile: reJSTest,
		Marker:   reJSMarker,
		Config:   rx(`(^|/)(vitest|jest)[A-Za-z0-9._-]*\.(config|workspace)\.(ts|js|mjs|cjs|mts|json)$|(^|/)package\.json$`),
		Workers: []*regexp.Regexp{
			rx(`--max-?workers[= ]+(\d+)`), rx(`--pool-?options\.\w+\.max(?:threads|forks)[= ]+(\d+)`),
			rx(`max(?:Workers|Threads|Forks)\s*:\s*(\d+)`), rx(`"maxWorkers"\s*:\s*(\d+)`),
		},
		Auto:   rx(`--max-?workers[= ]+(?:\d+%|auto)`),
		Serial: rx(`--runinband|\s-i(\s|$)|--no-file-parallelism|fileParallelism\s*:\s*false`),
	},
	{
		Key:        "go",
		Run:        rx(`\bgo\s+test\b|\bgotestsum\b|\bgo\s+tool\s+gotestsum\b`),
		TestFile:   rx(`_test\.go$`),
		Marker:     rx(`(?m)^func\s+(?:Test|Example)\w*\s*\(`),
		UnitsByDir: true,
		Workers:    []*regexp.Regexp{rx(`\s-p[= ]+(\d+)`), rx(`-parallel[= ]+(\d+)`)},
	},
	{
		Key:      "jvm",
		Run:      rx(`\b(mvn|mvnw|\./mvnw|gradle|gradlew|\./gradlew)\b`),
		TestFile: rx(`(Test|Tests|IT)\.(java|kt|scala)$`),
		Marker:   rx(`(?m)^\s*@(?:Parameterized|Repeated)?Test\b`),
		Config:   rx(`(^|/)(pom\.xml|build\.gradle(\.kts)?|gradle\.properties|maven\.config)$`),
		Workers: []*regexp.Regexp{
			rx(`--max-workers[= ]+(\d+)`), rx(`\s-t[= ]*(\d+)`), rx(`-dforkcount[= ]+(\d+)`), rx(`maxParallelForks\s*=?\s*(\d+)`),
			rx(`<forkCount>(\d+)`), rx(`org\.gradle\.workers\.max\s*=\s*(\d+)`),
		},
		DefaultWorkers: 1,
	},
	{
		Key:      "dotnet",
		Run:      rx(`\bdotnet\s+test\b`),
		TestFile: rx(`(Tests?|Spec)\.cs$`),
		Marker:   rx(`(?m)^\s*\[(?:Fact|Theory|Test|TestCase|TestMethod|DataTestMethod)\b`),
		Config:   rx(`(\.runsettings|(^|/)xunit\.runner\.json|(^|/)Directory\.Build\.props)$`),
		Workers: []*regexp.Regexp{
			rx(`maxParallelThreads"?\s*[:=]\s*(\d+)`), rx(`<MaxCpuCount>(\d+)`), rx(`<Workers>(\d+)`), rx(`LevelOfParallelism\((\d+)\)`),
			rx(`\s-m:(\d+)`),
		},
	},
	{
		Key:      "pytest",
		Run:      rx(`\bpytest\b|\bpy\.test\b|python3?\s+-m\s+pytest`),
		TestFile: rx(`(^|/)(test_[^/]+|[^/]+_test)\.py$`),
		Marker:   rx(`(?m)^\s*(?:async\s+)?def\s+test_\w*\s*\(`),
		Config:   rx(`(^|/)(pytest\.ini|pyproject\.toml|setup\.cfg|tox\.ini)$`),
		Workers:  []*regexp.Regexp{rx(`\s-n[= ]?(\d+)`), rx(`--numprocesses[= ]+(\d+)`), rx(`--splits[= ]+(\d+)`)},
		Auto:     rx(`\s-n[= ]?(?:auto|logical)|--numprocesses[= ]+(?:auto|logical)`),
	},
	{
		Key:      "rust",
		Run:      rx(`\bcargo\s+(?:\+\S+\s+)?(?:test|nextest|build|check|clippy)\b`),
		TestFile: rx(`\.rs$`),
		Marker:   rx(`#\[(?:[a-z_]+::)*test\b`),
		Config:   rx(`(^|/)\.config/nextest\.toml$`),
		Workers:  []*regexp.Regexp{rx(`--test-threads[= ]+(\d+)`), rx(`test-threads\s*=\s*(\d+)`), rx(`\s-j[= ]*(\d+)`), rx(`--jobs[= ]+(\d+)`)},
	},
}

func playwrightProjects(cfg, runs string) int {
	if n := strings.Count(runs, "--project"); n > 0 {
		return n
	}
	i := strings.Index(cfg, "projects")
	if i < 0 {
		return 1
	}
	if n := len(rePWProj.FindAllString(cfg[i:], -1)); n > 0 {
		return n
	}
	return 1
}

// ServiceRule maps an image name or a command to a CostTable.Services key.
type ServiceRule struct {
	Key string
	Re  *regexp.Regexp
}

// ImageRules match the image of a service or a compose file.
var ImageRules = []ServiceRule{
	{"sqlserver", rx(`mssql|sql-?server`)},
	{"kafka", rx(`kafka|redpanda`)},
	{"elasticsearch", rx(`elasticsearch|opensearch`)},
	{"postgres", rx(`postgres|postgis`)},
	{"mysql", rx(`mysql|mariadb`)},
	{"mongo", rx(`mongo`)},
	{"redis", rx(`redis|valkey`)},
	{"rabbitmq", rx(`rabbitmq`)},
}

// TextRules match the text of a job for what no image names.
var TextRules = []ServiceRule{
	{"kvm", rx(`/dev/kvm`)},
	{"gpu", rx(`--gpus|nvidia|\bgpu\b|\bcuda\b`)},
	{"emulator", rx(`emulator|simulator|simctl|avdmanager`)},
}
