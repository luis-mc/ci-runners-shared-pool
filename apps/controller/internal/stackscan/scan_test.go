package stackscan

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/github"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry"
)

const commit = "0123456789abcdef0123456789abcdef01234567"

// recorded replays testdata/tree.json, a GitHub git-tree answer, and file
// contents recorded by path.
type recorded struct {
	blobs    []Blob
	files    map[string]string
	treeCall atomic.Int32
	fileCall atomic.Int32
	treeErr  error
	gate     chan struct{}
}

func load(t *testing.T) *recorded {
	t.Helper()
	raw, err := os.ReadFile("testdata/tree.json")
	if err != nil {
		t.Fatal(err)
	}
	var tr struct {
		Tree []struct {
			Path, Mode, Type, SHA string
			Size                  int64
		}
	}
	if err := json.Unmarshal(raw, &tr); err != nil {
		t.Fatal(err)
	}
	r := &recorded{files: map[string]string{
		".crsp.yml":                         "profile: node\r\n",
		".devcontainer/devcontainer.json":   "{}\r\n",
		".nvmrc":                            "22\r\n",
		"Dockerfile":                        "FROM node:22\r\nRUN a\rb\r\n",
		"compose.yaml":                      "services: {}\n",
		"go.mod":                            "module x\n",
		"package.json":                      "{}\n",
		"services/api/requirements-dev.txt": "psycopg\n",
	}}
	for _, e := range tr.Tree {
		if e.Type == "blob" {
			r.blobs = append(r.blobs, Blob{Path: e.Path, SHA: e.SHA, Size: e.Size})
		}
	}
	return r
}

func (r *recorded) Blobs(context.Context, string, string) ([]Blob, error) {
	r.treeCall.Add(1)
	if r.gate != nil {
		<-r.gate
	}
	return r.blobs, r.treeErr
}

func (r *recorded) File(_ context.Context, _, p, _ string) ([]byte, error) {
	r.fileCall.Add(1)
	c, ok := r.files[p]
	if !ok {
		return nil, errors.New("not recorded: " + p)
	}
	return []byte(c), nil
}

func TestScanReadsOnlyStackFilesLFNormalised(t *testing.T) { // R5.73, R5.74
	r := load(t)
	s := New(r)
	res, err := s.Scan(context.Background(), "alice/demo", commit)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range res.Files {
		got = append(got, f.Path)
		if strings.Contains(string(f.Content), "\r\n") {
			t.Errorf("%s keeps CRLF", f.Path)
		}
	}
	want := ".crsp.yml .devcontainer/devcontainer.json .nvmrc Dockerfile compose.yaml go.mod package.json pnpm-lock.yaml services/api/requirements-dev.txt"
	if strings.Join(got, " ") != want {
		t.Fatalf("files\n got %v\nwant %s", got, want)
	}
	// a lone CR stays (R5.22: only CRLF is normalised)
	for _, f := range res.Files {
		if f.Path == "Dockerfile" && string(f.Content) != "FROM node:22\nRUN a\rb\n" {
			t.Errorf("dockerfile %q", f.Content)
		}
	}
	if r.fileCall.Load() != 8 { // the 5 MB lock file is not read
		t.Errorf("file reads %d, want 8", r.fileCall.Load())
	}
	h, _ := registry.StackHash(res.Files)
	if h != res.StackHash || len(h) != 64 {
		t.Errorf("hash %q vs %q", res.StackHash, h)
	}
}

func TestTooLargeFileHashedByBlobSHA(t *testing.T) { // R5.76
	r := load(t)
	s := New(r)
	res, err := s.Scan(context.Background(), "alice/demo", commit)
	if err != nil {
		t.Fatal(err)
	}
	var lock *Finding
	for i := range res.Findings {
		if res.Findings[i].Path == "pnpm-lock.yaml" {
			lock = &res.Findings[i]
		}
	}
	if lock == nil || !lock.TooLarge || lock.Kind != KindLock || lock.Size != 5000000 {
		t.Fatalf("finding %+v", lock)
	}
	h1 := res.StackHash
	// a different blob sha for that file changes the stack hash
	for i := range r.blobs {
		if r.blobs[i].Path == "pnpm-lock.yaml" {
			r.blobs[i].SHA = strings.Repeat("f", 40)
		}
	}
	res2, err := New(r).Scan(context.Background(), "alice/demo", commit)
	if err != nil || res2.StackHash == h1 {
		t.Fatalf("hash did not follow the blob sha: %v", err)
	}
}

func TestFindingsKeepNoText(t *testing.T) { // R5.74
	var _ = Finding{} // compile-time: no content field
	typ := strings.ToLower(strings.Join(fieldNames(Finding{}), " "))
	if strings.Contains(typ, "content") || strings.Contains(typ, "text") {
		t.Fatalf("Finding holds text: %s", typ)
	}
}

func fieldNames(v Finding) []string {
	b, _ := json.Marshal(v)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestCacheByCommit(t *testing.T) {
	r := load(t)
	s := New(r)
	ctx := context.Background()
	a, err := s.StackFiles(ctx, "alice/demo", commit)
	if err != nil {
		t.Fatal(err)
	}
	a[0].Content[0] = 'X' // a caller edit must not reach the cache
	b, _ := s.StackFiles(ctx, "alice/demo", strings.ToUpper(commit))
	if r.treeCall.Load() != 1 || r.fileCall.Load() != 8 {
		t.Fatalf("calls tree %d files %d", r.treeCall.Load(), r.fileCall.Load())
	}
	if b[0].Content[0] == 'X' {
		t.Fatal("cache shares caller memory")
	}
	if _, err := s.StackFiles(ctx, "alice/demo", strings.Repeat("a", 40)); err != nil {
		t.Fatal(err)
	}
	if r.treeCall.Load() != 2 {
		t.Fatal("another commit must be read")
	}
	if _, err := s.StackFiles(ctx, "bob/demo", commit); err != nil || r.treeCall.Load() != 3 {
		t.Fatal("another repository must be read")
	}
}

func TestConcurrentAsksShareOneRead(t *testing.T) {
	r := load(t)
	r.gate = make(chan struct{})
	s := New(r)
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.Scan(context.Background(), "alice/demo", commit); err != nil {
				t.Error(err)
			}
		}()
	}
	// the first read is in flight (blocked on the gate); the others join it or hit the cache
	deadline := time.Now().Add(5 * time.Second)
	for r.treeCall.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("no scan reached the reader")
		}
		runtime.Gosched()
	}
	close(r.gate)
	wg.Wait()
	if r.treeCall.Load() != 1 {
		t.Fatalf("tree calls %d", r.treeCall.Load())
	}
}

func TestBadCommitRefused(t *testing.T) {
	s := New(load(t))
	for _, c := range []string{"", "main", "abc123", "../x"} {
		if _, err := s.Scan(context.Background(), "alice/demo", c); !errors.Is(err, ErrBadCommit) {
			t.Errorf("%q: %v", c, err)
		}
	}
}

func TestRateLimitDelaysAndIsNotCached(t *testing.T) { // R5.75
	r := load(t)
	reset := time.Date(2026, 11, 19, 18, 0, 0, 0, time.UTC)
	r.treeErr = &github.RateLimitError{ResetAt: reset}
	s := New(r)
	_, err := s.Scan(context.Background(), "alice/demo", commit)
	var d *DelayedError
	if !errors.Is(err, ErrDelayed) || !errors.As(err, &d) || !d.ResetAt.Equal(reset) {
		t.Fatalf("err %v", err)
	}
	if !strings.Contains(err.Error(), "delayed") || !strings.Contains(err.Error(), "2026-11-19T18:00:00Z") {
		t.Errorf("message %q", err)
	}
	r.treeErr = nil
	if _, err := s.Scan(context.Background(), "alice/demo", commit); err != nil {
		t.Fatalf("retry: %v", err)
	}
}

func TestReadErrorNotCached(t *testing.T) {
	r := load(t)
	delete(r.files, "go.mod")
	s := New(r)
	if _, err := s.Scan(context.Background(), "alice/demo", commit); err == nil {
		t.Fatal("want error")
	}
	r.files["go.mod"] = "module x\n"
	if _, err := s.Scan(context.Background(), "alice/demo", commit); err != nil {
		t.Fatal(err)
	}
}

func TestTooManyFiles(t *testing.T) {
	r := load(t)
	r.blobs = nil
	for i := 0; i <= MaxFiles; i++ {
		r.blobs = append(r.blobs, Blob{Path: "d" + string(rune('a'+i%26)) + "/" + string(rune('a'+i/26%26)) + "/" + string(rune('a'+i/676)) + "/package.json", Size: 1})
	}
	if _, err := New(r).Scan(context.Background(), "alice/demo", commit); !errors.Is(err, ErrTooManyFiles) {
		t.Fatalf("err %v", err)
	}
}

func TestNoStackFilesGivesEmptyHash(t *testing.T) {
	r := load(t)
	r.blobs = []Blob{{Path: "README.md", Size: 3}}
	res, err := New(r).Scan(context.Background(), "alice/demo", commit)
	if err != nil || len(res.Files) != 0 || res.StackHash != "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" {
		t.Fatalf("%v %+v", err, res)
	}
}

func TestClassify(t *testing.T) {
	yes := map[string]Kind{
		"package.json": KindManifest, "pnpm-lock.yaml": KindLock, ".nvmrc": KindVersion,
		"Dockerfile": KindDockerfile, "Dockerfile.ci": KindDockerfile, "app.Dockerfile": KindDockerfile,
		"compose.yaml": KindCompose, "docker-compose.override.yml": KindCompose, "compose.prod.yaml": KindCompose,
		".devcontainer/devcontainer.json": KindDevcontainer, ".devcontainer/py/devcontainer.json": KindDevcontainer,
		".devcontainer.json": KindDevcontainer, ".crsp.yml": KindCRSP, "requirements.txt": KindManifest,
		"a/b/c/go.mod": KindManifest, "rust-toolchain": KindVersion, "gradle/wrapper/gradle-wrapper.properties": KindManifest,
	}
	for p, k := range yes {
		if got, ok := Classify(p); !ok || got != k {
			t.Errorf("%s: %v %v", p, got, ok)
		}
	}
	for _, p := range []string{
		".github/workflows/ci.yml", "README.md", "go.sum", "a/b/c/d/go.mod", "node_modules/x/package.json",
		"vendor/go.mod", "sub/.crsp.yml", "sub/.devcontainer.json", "devcontainer.json", "x/.devcontainer/devcontainer.json",
		"/package.json", "./package.json", `a\package.json`, "", "mycompose.txt", "notes-Dockerfile",
	} {
		if k, ok := Classify(p); ok {
			t.Errorf("%q wrongly a stack file (%s)", p, k)
		}
	}
}

type fakeClient struct {
	inst int64
}

func (f *fakeClient) ListBlobsAt(_ context.Context, inst int64, owner, repo, ref string) ([]github.TreeBlob, error) {
	f.inst = inst
	return []github.TreeBlob{{Path: "go.mod", SHA: "ab", Size: 3}}, nil
}

func (f *fakeClient) GetFileAt(_ context.Context, inst int64, owner, repo, p, ref string) ([]byte, error) {
	return []byte(owner + "|" + repo + "|" + p + "|" + ref), nil
}

func TestGitHubReaderSplitsRepositoryAndFindsInstallation(t *testing.T) {
	fc := &fakeClient{}
	g := GitHub{C: fc, Target: func(context.Context, string) (int64, error) { return 42, nil }}
	s := New(g)
	res, err := s.Scan(context.Background(), "alice/demo", commit)
	if err != nil || fc.inst != 42 || string(res.Files[0].Content) != "alice|demo|go.mod|"+commit {
		t.Fatalf("%v %+v", err, res)
	}
	if _, err := s.Scan(context.Background(), "nodash", commit); err == nil {
		t.Fatal("bad repository accepted")
	}
}
