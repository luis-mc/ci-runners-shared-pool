package contexts_test

import (
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/cli/contexts"
)

func TestAddUseRemove(t *testing.T) {
	dir := t.TempDir()
	cf, err := contexts.Load(dir)
	if err != nil || len(cf.Contexts) != 0 {
		t.Fatal(err, cf)
	}
	cf.Set(contexts.Context{Name: "home", URL: "https://home:8460", Fingerprint: "aa"})
	cf.Set(contexts.Context{Name: "office", URL: "https://office:8460", Fingerprint: "bb"})
	if cf.Current != "office" {
		t.Fatal("login must make the context current")
	}
	if err := cf.Save(dir); err != nil {
		t.Fatal(err)
	}
	cf, _ = contexts.Load(dir)
	if len(cf.Contexts) != 2 || cf.Contexts["home"].Fingerprint != "aa" || cf.Current != "office" {
		t.Fatalf("%+v", cf)
	}
	if err := cf.Use("home"); err != nil || cf.Current != "home" {
		t.Fatal(err)
	}
	if cf.Use("nope") == nil {
		t.Fatal("use of an unknown context worked")
	}
	if err := cf.Remove("home"); err != nil || cf.Current != "" || len(cf.Contexts) != 1 {
		t.Fatalf("%+v", cf)
	}
	if u, ok := cf.ByURL("https://office:8460/"); !ok || u.Name != "office" {
		t.Fatal("ByURL")
	}
	if _, ok := cf.ByURL("https://other"); ok {
		t.Fatal("ByURL matched an unpinned URL")
	}
}

func TestPickOrder(t *testing.T) {
	cf := &contexts.File{Current: "c", Contexts: map[string]contexts.Context{"a": {Name: "a"}, "b": {Name: "b"}, "c": {Name: "c"}}}
	for _, tc := range []struct{ flag, env, want string }{{"a", "b", "a"}, {"", "b", "b"}, {"", "", "c"}} {
		got, err := cf.Pick(tc.flag, tc.env)
		if err != nil || got.Name != tc.want {
			t.Errorf("%+v: %v %v", tc, got, err)
		}
	}
	if _, err := (&contexts.File{Contexts: map[string]contexts.Context{}}).Pick("", ""); err == nil {
		t.Error("no context chosen should fail")
	}
}

func TestNoTokenInFile(t *testing.T) {
	dir := t.TempDir()
	cf := &contexts.File{Contexts: map[string]contexts.Context{}}
	cf.Set(contexts.Context{Name: "home", URL: "https://home:8460", Fingerprint: "aa"})
	if err := cf.Save(dir); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(contexts.Path(dir))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(strings.ReplaceAll(string(raw), "No tokens are kept here", "")), "token") {
		t.Fatalf("config file mentions a token: %s", raw)
	}
	if info, _ := os.Stat(contexts.Path(dir)); runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", info.Mode())
	}
}
