package jobcmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func envOf(m map[string]string, out, errw *strings.Builder) Env {
	return Env{Getenv: func(k string) string { return m[k] }, Stdout: out, Stderr: errw}
}

func TestDbEnvMasksSecretsAndAppends(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "db.env")
	ghenv := filepath.Join(dir, "github_env")
	const pw = "s3cretPassw0rdValue"
	body := "DATABASE" + "_URL=postgres://u:" + pw + "@h:5432/d\nPGHOST=h\nPGPORT=5432\nPGUSER=u\nPGPASSWORD=" + pw + "\nPGDATABASE=d\n"
	if err := os.WriteFile(src, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errw strings.Builder
	code := DBEnv(envOf(map[string]string{EnvFileVar: src, "GITHUB_ENV": ghenv}, &out, &errw))
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errw.String())
	}
	got, _ := os.ReadFile(ghenv)
	if string(got) != strings.TrimSuffix(body, "\n")+"\n" {
		t.Errorf("GITHUB_ENV %q", got)
	}
	if !strings.Contains(out.String(), "::add-mask::"+pw) {
		t.Error("password not masked")
	}
	for _, l := range strings.Split(out.String(), "\n") {
		if strings.Contains(l, pw) && !strings.HasPrefix(l, "::add-mask::") {
			t.Errorf("password printed: %q", l)
		}
	}
	// A second step appends; it never truncates.
	DBEnv(envOf(map[string]string{EnvFileVar: src, "GITHUB_ENV": ghenv}, &out, &errw))
	got, _ = os.ReadFile(ghenv)
	if strings.Count(string(got), "PGHOST=h") != 2 {
		t.Error("not appended")
	}
}

func TestDbEnvOutsidePoolJobDoesNothing(t *testing.T) {
	var out, errw strings.Builder
	if code := DBEnv(envOf(map[string]string{"GITHUB_ENV": "x"}, &out, &errw)); code != 0 {
		t.Errorf("exit %d", code)
	}
	if !strings.Contains(out.String(), "nothing to do") {
		t.Errorf("output %q", out.String())
	}
}

func TestDbEnvFailsWithoutGithubEnvOrFile(t *testing.T) {
	var out, errw strings.Builder
	if DBEnv(envOf(map[string]string{EnvFileVar: "/nope"}, &out, &errw)) == 0 {
		t.Error("missing GITHUB_ENV must fail")
	}
	if DBEnv(envOf(map[string]string{EnvFileVar: "/nope", "GITHUB_ENV": "x"}, &out, &errw)) == 0 {
		t.Error("missing file must fail")
	}
}
