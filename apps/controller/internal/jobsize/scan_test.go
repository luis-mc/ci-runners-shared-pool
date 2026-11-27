package jobsize

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/stackscan"
)

// fixture is an in-memory repository: path to text.
type fixture map[string]string

func (f fixture) Blobs(_ context.Context, _, _ string) ([]stackscan.Blob, error) {
	var out []stackscan.Blob
	for p, t := range f {
		out = append(out, stackscan.Blob{Path: p, SHA: "x", Size: int64(len(t))})
	}
	return out, nil
}

func (f fixture) File(_ context.Context, _, p, _ string) ([]byte, error) {
	t, ok := f[p]
	if !ok {
		return nil, errors.New("missing " + p)
	}
	return []byte(t), nil
}

func many(n int, pathFmt, body string) fixture {
	out := fixture{}
	for i := 0; i < n; i++ {
		out[fmt.Sprintf(pathFmt, i)] = body
	}
	return out
}

func merge(fs ...fixture) fixture {
	out := fixture{}
	for _, f := range fs {
		for k, v := range f {
			out[k] = v
		}
	}
	return out
}

func wf(job string) fixture {
	return fixture{".github/workflows/ci.yml": "name: ci\non: push\njobs:\n" + job}
}

const pwTests = "test('a', async () => {});\ntest('b', async () => {});\ntest.describe('g', () => {\n  test('c', () => {});\n});\n"

func scanOne(t *testing.T, f fixture) Guess {
	t.Helper()
	res, err := (&Scanner{R: f}).Scan(context.Background(), "o/r", strings.Repeat("a", 40), DefaultCostTable())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Jobs) != 1 {
		t.Fatalf("jobs = %d, want 1: %+v", len(res.Jobs), res.Jobs)
	}
	return res.Jobs[0]
}

func TestScanStacks(t *testing.T) {
	cases := []struct {
		name       string
		fx         fixture
		tests      int
		workers    int
		parallel   int
		cpu, gib   float64
		conf       Confidence
		wantWeight Weight
		wantClass  Class
		service    string
	}{
		{
			name: "playwright workers from config, CI branch",
			fx: merge(wf("  e2e:\n    steps:\n      - run: pnpm exec playwright test\n"), many(5, "e2e/f%d.spec.ts", pwTests),
				fixture{"playwright.config.ts": "export default { workers: process.env.CI ? 3 : undefined, fullyParallel: true, testDir: './e2e' };"}),
			tests: 15, workers: 3, parallel: 3, cpu: 3, gib: 3, conf: ConfHigh, wantWeight: Weight{3, 3}, wantClass: Heavy,
		},
		{
			name: "playwright workers flag wins, capped by files",
			fx: merge(wf("  e2e:\n    steps:\n      - run: npx playwright test --workers=8\n"), many(2, "e2e/f%d.spec.ts", pwTests),
				fixture{"playwright.config.ts": "export default { workers: 1, testDir: 'e2e' };"}),
			tests: 6, workers: 8, parallel: 2, cpu: 2, gib: 2, conf: ConfHigh, wantWeight: Weight{2, 2}, wantClass: Normal,
		},
		{
			name: "playwright three projects, four shards",
			fx: merge(wf("  e2e:\n    strategy:\n      matrix:\n        shard: [1, 2, 3, 4]\n    steps:\n      - run: npx playwright test --shard=${{ matrix.shard }}/4\n"),
				many(20, "e2e/f%d.spec.ts", pwTests),
				fixture{"playwright.config.ts": "export default { fullyParallel: true, workers: 2, projects: [{ name: 'chromium' }, { name: 'firefox' }, { name: 'webkit' }] };"}),
			tests: 60, workers: 2, parallel: 2, cpu: 2, gib: 2, conf: ConfHigh, wantWeight: Weight{2, 2}, wantClass: Normal,
		},
		{
			name: "vitest max workers from config",
			fx: merge(wf("  unit:\n    steps:\n      - run: pnpm vitest run\n"), many(3, "src/f%d.test.ts", "it('x', () => {});\nit('y', () => {});\n"),
				fixture{"vitest.config.ts": "export default { test: { maxWorkers: 4 } }"}),
			tests: 6, workers: 4, parallel: 4, cpu: 4, gib: 2, conf: ConfHigh, wantWeight: Weight{4, 2}, wantClass: Heavy,
		},
		{
			name:  "jest runInBand is one worker",
			fx:    merge(wf("  unit:\n    steps:\n      - run: npx jest --runInBand\n"), many(3, "src/f%d.test.ts", "it('x', () => {});\n")),
			tests: 3, workers: 1, parallel: 1, cpu: 1, gib: 0.5, conf: ConfHigh, wantWeight: Weight{1, 1}, wantClass: Light,
		},
		{
			name: "npm script test reads package.json",
			fx: merge(wf("  unit:\n    steps:\n      - run: npm test\n"), many(2, "src/f%d.test.ts", "it('x', () => {});\n"),
				fixture{"package.json": `{"scripts": {"test": "vitest run --maxWorkers=3"}}`}),
			tests: 2, workers: 3, parallel: 2, cpu: 2, gib: 1, conf: ConfHigh, wantWeight: Weight{2, 1}, wantClass: Normal,
		},
		{
			name: "go packages and -p",
			fx: merge(wf("  unit:\n    steps:\n      - run: go test -p 3 ./...\n"),
				fixture{"a/a_test.go": "func TestA(t *testing.T) {}\nfunc TestB(t *testing.T) {}", "b/b_test.go": "func TestC(t *testing.T) {}", "c/c_test.go": "func TestD(t *testing.T) {}", "d/d_test.go": "func TestE(t *testing.T) {}"}),
			tests: 5, workers: 3, parallel: 3, cpu: 3, gib: 1.5, conf: ConfHigh, wantWeight: Weight{3, 2}, wantClass: Heavy,
		},
		{
			name:  "go default workers, one package",
			fx:    merge(wf("  unit:\n    steps:\n      - run: go test ./...\n"), fixture{"a/a_test.go": "func TestA(t *testing.T) {}"}),
			tests: 1, workers: 2, parallel: 1, cpu: 1, gib: 0.5, conf: ConfHigh, wantWeight: Weight{1, 1}, wantClass: Light,
		},
		{
			name: "pytest xdist -n 4",
			fx: merge(wf("  unit:\n    steps:\n      - run: pytest -n 4\n"), many(3, "tests/test_f%d.py", "def test_x():\n    pass\n"),
				fixture{"tests/test_f0.py": "def test_x():\n    pass\ndef test_y():\n    pass\n"}),
			tests: 4, workers: 4, parallel: 4, cpu: 4, gib: 2, conf: ConfHigh, wantWeight: Weight{4, 2}, wantClass: Heavy,
		},
		{
			name: "pytest config addopts",
			fx: merge(wf("  unit:\n    steps:\n      - run: pytest\n"), many(2, "tests/test_f%d.py", "def test_x():\n    pass\n"),
				fixture{"pytest.ini": "[pytest]\naddopts = -n 2\n"}),
			tests: 2, workers: 2, parallel: 2, cpu: 2, gib: 1, conf: ConfHigh, wantWeight: Weight{2, 1}, wantClass: Normal,
		},
		{
			name: "rust test-threads",
			fx: merge(wf("  test:\n    steps:\n      - run: cargo test -- --test-threads 4\n"),
				fixture{"src/lib.rs": "#[test]\nfn a() {}\n#[test]\nfn b() {}\n"}),
			tests: 2, workers: 4, parallel: 2, cpu: 2, gib: 4, conf: ConfHigh, wantWeight: Weight{2, 4}, wantClass: Heavy,
		},
		{
			name: "rust nextest config",
			fx: merge(wf("  test:\n    steps:\n      - run: cargo nextest run\n"),
				fixture{"src/lib.rs": "#[test]\nfn a() {}\n#[test]\nfn b() {}\n#[test]\nfn c() {}\n", ".config/nextest.toml": "[profile.default]\ntest-threads = 1\n"}),
			tests: 3, workers: 1, parallel: 1, cpu: 1, gib: 2, conf: ConfHigh, wantWeight: Weight{1, 2}, wantClass: Normal,
		},
		{
			name: "junit through gradle, one fork by default",
			fx: merge(wf("  test:\n    steps:\n      - run: ./gradlew test\n"),
				fixture{"src/test/java/AaTest.java": "@Test void a() {}\n@Test void b() {}", "build.gradle": "plugins {}"}),
			tests: 2, workers: 1, parallel: 1, cpu: 1, gib: 2, conf: ConfHigh, wantWeight: Weight{1, 2}, wantClass: Normal,
		},
		{
			name: "maven forkCount",
			fx: merge(wf("  test:\n    steps:\n      - run: mvn -B verify\n"),
				fixture{"src/test/java/AaTest.java": "@Test void a() {}\n@Test void b() {}", "src/test/java/BbTest.java": "@Test void c() {}",
					"pom.xml": "<project><forkCount>2</forkCount></project>"}),
			tests: 3, workers: 2, parallel: 2, cpu: 2, gib: 4, conf: ConfHigh, wantWeight: Weight{2, 4}, wantClass: Heavy,
		},
		{
			name: "dotnet xunit parallel threads",
			fx: merge(wf("  test:\n    steps:\n      - run: dotnet test\n"),
				fixture{"T/AaTests.cs": "[Fact]\npublic void A() {}\n[Fact]\npublic void B() {}", "T/xunit.runner.json": `{"maxParallelThreads": 3}`}),
			tests: 2, workers: 3, parallel: 2, cpu: 2, gib: 2, conf: ConfHigh, wantWeight: Weight{2, 2}, wantClass: Normal,
		},
		{
			name:  "opaque script is low confidence, named by the job",
			fx:    merge(wf("  e2e:\n    steps:\n      - run: ./ci.sh\n"), many(2, "e2e/f%d.spec.ts", pwTests)),
			tests: 6, workers: 2, parallel: 2, cpu: 2, gib: 2, conf: ConfLow, wantWeight: Weight{2, 2}, wantClass: Normal,
		},
		{
			name: "service sqlserver adds over 2 GiB",
			fx: merge(wf("  integration:\n    services:\n      db:\n        image: mcr.microsoft.com/mssql/server:2022\n    steps:\n      - run: go test ./...\n"),
				fixture{"a/a_test.go": "func TestA(t *testing.T) {}"}),
			tests: 1, workers: 2, parallel: 1, cpu: 2, gib: 3, conf: ConfHigh, wantWeight: Weight{2, 3}, wantClass: Heavy, service: "sqlserver",
		},
		{
			name: "docker compose up reads the compose images",
			fx: merge(wf("  integration:\n    steps:\n      - run: docker compose up -d\n      - run: go test ./...\n"),
				fixture{"a/a_test.go": "func TestA(t *testing.T) {}", "docker-compose.yml": "services:\n  k:\n    image: confluentinc/cp-kafka:7\n"}),
			tests: 1, workers: 2, parallel: 1, cpu: 2, gib: 3, conf: ConfHigh, wantWeight: Weight{2, 3}, wantClass: Heavy, service: "kafka",
		},
		{
			name: "testcontainers import in the manifest",
			fx: merge(wf("  integration:\n    steps:\n      - run: go test ./...\n"),
				fixture{"a/a_test.go": "func TestA(t *testing.T) {}", "go.mod": "require github.com/testcontainers/testcontainers-go v0.30.0"}),
			tests: 1, workers: 2, parallel: 1, cpu: 2, gib: 1.5, conf: ConfHigh, wantWeight: Weight{2, 2}, wantClass: Normal, service: "testcontainers",
		},
	}
	if len(cases) > 20 {
		t.Fatal("at most 20 cases per test")
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g := scanOne(t, c.fx)
			if g.Tests != c.tests || g.Workers != c.workers || g.Parallel != c.parallel {
				t.Errorf("tests/workers/parallel = %d/%d/%d, want %d/%d/%d: %v", g.Tests, g.Workers, g.Parallel, c.tests, c.workers, c.parallel, g.Reasons)
			}
			if g.CPU != c.cpu || g.GiB != c.gib {
				t.Errorf("peak = %v CPU %v GiB, want %v/%v: %v", g.CPU, g.GiB, c.cpu, c.gib, g.Reasons)
			}
			if g.Confidence != c.conf || g.Weight != c.wantWeight || g.Class != c.wantClass {
				t.Errorf("conf/weight/class = %s/%v/%s, want %s/%v/%s", g.Confidence, g.Weight, g.Class, c.conf, c.wantWeight, c.wantClass)
			}
			if c.service != "" && !contains(g.Services, c.service) {
				t.Errorf("services = %v, want %s", g.Services, c.service)
			}
		})
	}
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

func TestScanFindsNothing(t *testing.T) {
	f := wf("  lint:\n    steps:\n      - run: echo hello\n")
	res, err := (&Scanner{R: f}).Scan(context.Background(), "o/r", strings.Repeat("a", 40), DefaultCostTable())
	if err != nil || len(res.Jobs) != 0 {
		t.Fatalf("jobs = %v err = %v, want none", res.Jobs, err)
	}
}

func TestScanWorkflowFields(t *testing.T) {
	f := merge(wf("  e2e:\n    name: End to end\n    timeout-minutes: 25\n    strategy:\n      max-parallel: 2\n      matrix:\n        shard: [1, 2]\n    steps:\n      - run: npx playwright test\n"),
		many(2, "e2e/f%d.spec.ts", pwTests))
	g := scanOne(t, f)
	if g.Name != "End to end" || g.TimeoutMin != 25 || g.MaxParallel != 2 || g.Workflow != ".github/workflows/ci.yml" {
		t.Fatalf("fields = %+v", g)
	}
}

func TestParseSizes(t *testing.T) {
	rules, err := ParseSizes([]byte("sizes: { 'integration-*': heavy, lint: Light }\n"))
	if err != nil || len(rules) != 2 || rules[0] != (FileRule{"integration-*", Heavy}) || rules[1].Class != Light {
		t.Fatalf("rules = %v err = %v", rules, err)
	}
	if _, err := ParseSizes([]byte("sizes: { a: huge }")); err == nil || !strings.Contains(err.Error(), "huge") {
		t.Fatalf("err = %v, want a bad class error", err)
	}
	if r, err := ParseSizes([]byte("stack: node")); err != nil || len(r) != 0 {
		t.Fatalf("no sizes: %v %v", r, err)
	}
}

func TestScanReadsCrspFile(t *testing.T) {
	f := merge(wf("  lint:\n    steps:\n      - run: pnpm vitest run\n"), many(1, "src/f%d.test.ts", "it('x', () => {});"),
		fixture{".crsp.yml": "sizes: { 'integration-*': heavy }"})
	res, err := (&Scanner{R: f}).Scan(context.Background(), "o/r", strings.Repeat("a", 40), DefaultCostTable())
	if err != nil || len(res.File) != 1 || res.File[0].Pattern != "integration-*" {
		t.Fatalf("file = %v err = %v", res.File, err)
	}
}
