package cli

import (
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/internal/cli/contexts"
	"github.com/luis-mc/ci-runners-shared-pool/internal/secrets"
	"gopkg.in/yaml.v3"
)

func allPaths(cmd *command, prefix []string, fn func(path []string, c *command)) {
	for _, s := range cmd.subs {
		p := append(append([]string{}, prefix...), s.name)
		fn(p, s)
		allPaths(s, p, fn)
	}
}

func TestEveryCommandHasHelp(t *testing.T) {
	f := newFixture(t)
	root := tree(&Env{})
	n := 0
	allPaths(root, nil, func(path []string, c *command) {
		code, out, errOut := f.run(append(append([]string{}, path...), "--help")...)
		if code != 0 || !strings.Contains(out, "Usage: crsp") {
			t.Errorf("%v --help: exit %d, out %q, err %q", path, code, out, errOut)
		}
		n++
	})
	if n < 30 {
		t.Fatalf("walked only %d commands", n)
	}
	for _, want := range []string{"controller", "agent", "tray", "login", "logout", "open", "context", "repo", "machine", "runner", "job", "doctor", "config", "backup", "restore", "update", "completion", "version"} {
		if root.find(want) == nil {
			t.Errorf("missing command %s", want)
		}
	}
}

func TestUnbuiltCommandExits2(t *testing.T) {
	f := newFixture(t)
	for _, c := range [][]string{{"config", "export"}, {"tray"}} {
		code, _, errOut := f.run(c...)
		if code != 2 || !strings.Contains(errOut, "not built yet") {
			t.Errorf("%v: exit %d, %q", c, code, errOut)
		}
	}
	if len(f.calls) != 0 {
		t.Fatalf("an unbuilt command called the controller: %v", f.calls)
	}
}

func TestUsageErrors(t *testing.T) {
	f := newFixture(t)
	if code, _, _ := f.run(); code != 2 {
		t.Errorf("no args: %d", code)
	}
	if code, _, e := f.run("bogus"); code != 2 || !strings.Contains(e, "unknown command") {
		t.Errorf("bogus: %d %q", code, e)
	}
	if code, _, _ := f.run("machine", "list", "--nope"); code != 2 {
		t.Errorf("bad flag: %d", code)
	}
	if code, out, _ := f.run("help"); code != 0 || !strings.Contains(out, "Usage") {
		t.Errorf("help: %d", code)
	}
}

func TestVersionJSON(t *testing.T) {
	f := newFixture(t)
	code, out, _ := f.run("version", "--json")
	if code != 0 || !strings.Contains(out, `"version": "test"`) || !strings.Contains(out, `"go": "go-test"`) {
		t.Fatalf("%d %q", code, out)
	}
	_, out, _ = f.run("version")
	if out != "crsp test (go-test)\n" {
		t.Fatalf("%q", out)
	}
}

// TestNoHandWrittenControllerCalls: the controller is reached only through the
// generated client (R5.2). No file builds a request by hand except the GitHub
// device flow in cmd_auth.go.
func TestNoHandWrittenControllerCalls(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		raw, _ := os.ReadFile(name)
		src := string(raw)
		if strings.Contains(src, "/api/v1") || strings.Contains(src, "/healthz") {
			t.Errorf("%s writes a controller path by hand", name)
		}
		if strings.Contains(src, "http.NewRequest") && name != "cmd_auth.go" {
			t.Errorf("%s builds an HTTP request by hand", name)
		}
	}
}

func TestNoInsecureFlag(t *testing.T) {
	root := tree(&Env{})
	bad := regexp.MustCompile(`(?i)insecure|skip-?verify|no-?verify|plain-?http`)
	allPaths(root, nil, func(path []string, c *command) {
		if bad.MatchString(c.name) {
			t.Errorf("command %v", path)
		}
		if c.flags != nil {
			cl := &call{}
			fs := newFlagSetForTest()
			cl.g.bind(fs)
			c.flags(cl, fs)
			fs.VisitAll(func(f *flagT) {
				if bad.MatchString(f.Name) {
					t.Errorf("%v has flag --%s", path, f.Name)
				}
			})
		}
	})
}

func TestEveryOperationHasCommand(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "packages", "contract", "openapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var spec struct {
		Paths map[string]map[string]yaml.Node `yaml:"paths"`
	}
	if err := yaml.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for path, item := range spec.Paths {
		for method, node := range item {
			var op struct {
				ID     string `yaml:"operationId"`
				XCLI   string `yaml:"x-cli"`
				Reason string `yaml:"x-cli-reason"`
			}
			if node.Decode(&op) != nil || op.ID == "" {
				continue
			}
			seen[op.ID] = true
			_, has := operationCommands[op.ID]
			switch {
			case has && op.XCLI != "":
				t.Errorf("%s %s (%s) has a command and x-cli", method, path, op.ID)
			case !has && op.XCLI != "none":
				t.Errorf("%s %s (%s) has no CLI command and no `x-cli: none`", method, path, op.ID)
			case !has && strings.TrimSpace(op.Reason) == "":
				t.Errorf("%s %s (%s) has `x-cli: none` without x-cli-reason", method, path, op.ID)
			}
		}
	}
	for id := range operationCommands {
		if !seen[id] {
			t.Errorf("operationCommands lists %s, which is not in the contract", id)
		}
	}
}

func TestStatusToExitCode(t *testing.T) {
	cases := map[int]int{401: ExitAuth, 403: ExitAuth, 404: ExitNotFound, 409: ExitConflict, 500: ExitFailed, 501: ExitFailed, 400: ExitFailed}
	for status, want := range cases {
		err := apiError(status, []byte(`{"error":{"code":"x","message":"m"}}`))
		ee, ok := err.(*exitError)
		if !ok || ee.Code != want || ee.Message != "m" {
			t.Errorf("status %d: %+v", status, err)
		}
	}
	if apiError(200, nil) != nil || apiError(202, nil) != nil {
		t.Error("success mapped to an error")
	}
	if ee := apiError(500, nil).(*exitError); !strings.Contains(ee.Message, "500") {
		t.Error(ee.Message)
	}
}

func TestUnreachableExits6(t *testing.T) {
	f := newFixture(t)
	f.srv.Close()
	code, _, errOut := f.run("machine", "list")
	if code != ExitUnreachable || !strings.Contains(errOut, "Could not reach the controller") {
		t.Fatalf("%d %q", code, errOut)
	}
}

func TestNotSignedInExits3(t *testing.T) {
	f := newFixture(t)
	f.secrets = &memSecrets{}
	code, _, errOut := f.run("machine", "list")
	if code != ExitAuth || !strings.Contains(errOut, "crsp login") {
		t.Fatalf("%d %q", code, errOut)
	}
	f.dir = t.TempDir()
	if code, _, _ := f.run("machine", "list"); code != ExitAuth {
		t.Fatalf("no context: %d", code)
	}
}

func TestJSONErrorEnvelopeOnStderr(t *testing.T) {
	f := newFixture(t)
	f.reply("GET /api/v1/machines", 403, apiErr("forbidden", "You need the admin role."))
	code, out, errOut := f.run("machine", "list", "--json")
	if code != ExitAuth || out != "" || !strings.Contains(errOut, `"code":"forbidden"`) {
		t.Fatalf("%d %q %q", code, out, errOut)
	}
}

func TestCallTimeout(t *testing.T) {
	f := newFixture(t)
	seen := make(chan struct{})
	f.handle("GET /api/v1/machines", func(w http.ResponseWriter, r *http.Request) {
		// Hold the answer until the client gives up; no wall-clock bound.
		<-r.Context().Done()
		close(seen)
	})
	code, _, errOut := f.run("machine", "list", "--timeout", "100ms")
	if code != ExitUnreachable {
		t.Fatalf("%d: %q", code, errOut)
	}
	<-seen // the server saw the request end
}

func TestNoColor(t *testing.T) {
	f := newFixture(t)
	f.reply("GET /api/v1/machines", 200, map[string]any{"items": []any{}})
	for _, a := range [][]string{{"machine", "list"}, {"machine", "list", "--no-color"}} {
		_, out, errOut := f.run(a...)
		if strings.Contains(out+errOut, "\x1b[") {
			t.Fatalf("colour in %v: %q", a, out+errOut)
		}
	}
}

func TestControllerFlagNeedsPinnedURL(t *testing.T) {
	f := newFixture(t)
	f.reply("GET /api/v1/machines", 200, map[string]any{"items": []any{}})
	if code, _, _ := f.run("machine", "list", "--controller", f.srv.URL); code != 0 {
		t.Fatalf("pinned url: %d", code)
	}
	code, _, errOut := f.run("machine", "list", "--controller", "https://unknown.example:1")
	if code != ExitUsage || !strings.Contains(errOut, "No context pins") {
		t.Fatalf("%d %q", code, errOut)
	}
}

func TestContextEnvAndFlag(t *testing.T) {
	f := newFixture(t)
	f.reply("GET /api/v1/machines", 200, map[string]any{"items": []any{}})
	if code, _, e := f.run("machine", "list", "--context", "nope"); code != ExitUsage {
		t.Fatalf("%d %q", code, e)
	}
	if code, _, _ := f.run("machine", "list", "--context", "home"); code != 0 {
		t.Fatalf("%d", code)
	}
}

func TestContextCommands(t *testing.T) {
	f := newFixture(t)
	code, out, _ := f.run("context", "list")
	if code != 0 || !strings.Contains(out, "home") || !strings.Contains(out, "*") {
		t.Fatalf("%q", out)
	}
	if code, out, _ := f.run("context", "list", "--json"); code != 0 || !strings.Contains(out, `"current": true`) {
		t.Fatalf("%q", out)
	}
	if code, _, _ := f.run("context", "use", "nope"); code != ExitNotFound {
		t.Fatalf("use nope: %d", code)
	}
	if code, _, _ := f.run("context", "use", "home"); code != 0 {
		t.Fatalf("use: %d", code)
	}
	// remove: asks, then removes with its token.
	if code, _, _ := f.run("context", "remove", "home"); code != ExitNotConfirmed {
		t.Fatalf("remove without yes: %d", code)
	}
	if _, err := f.secrets.Get(f.ctx, tokenKey("home")); err != nil {
		t.Fatal("token deleted without a yes")
	}
	if code, _, _ := f.run("context", "remove", "home", "--yes"); code != 0 {
		t.Fatalf("remove: %d", code)
	}
	if _, err := f.secrets.Get(f.ctx, tokenKey("home")); err != secrets.ErrNotFound {
		t.Fatal("token kept")
	}
	if code, out, _ := f.run("context", "list"); code != 0 || out != "" {
		t.Fatalf("%q", out)
	}
}

func TestNoTokenInFile(t *testing.T) {
	f := newFixture(t)
	raw, _ := os.ReadFile(contexts.Path(f.dir))
	if strings.Contains(string(raw), canary) || strings.Contains(strings.ToLower(string(raw)), "token") && !strings.Contains(string(raw), "No tokens") {
		t.Fatalf("config file: %s", raw)
	}
	info, _ := os.Stat(contexts.Path(f.dir))
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", info.Mode())
	}
}

func TestFallbackFileMode(t *testing.T) {
	p := filepath.Join(t.TempDir(), "secrets.json")
	s := &FileStore{Path: p}
	if err := s.Put(t.Context(), "k", []byte("v")); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(p); runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", info.Mode())
	}
	if v, err := s.Get(t.Context(), "k"); err != nil || string(v) != "v" {
		t.Fatal(v, err)
	}
	if _, err := s.Get(t.Context(), "missing"); err != secrets.ErrNotFound {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(p, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Get(t.Context(), "k"); err == nil || !strings.Contains(err.Error(), "0600") {
			t.Fatalf("a wide file was read: %v", err)
		}
	}
	_ = os.Chmod(p, 0o600)
	if err := s.Delete(t.Context(), "k"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(t.Context(), "k"); err != secrets.ErrNotFound {
		t.Fatal(err)
	}
	if s.Backend() != "file" {
		t.Fatal(s.Backend())
	}
}
