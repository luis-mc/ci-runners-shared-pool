package hooks_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/hooks"
)

func forkEvent(t *testing.T, name, payload string, extra map[string]string) hooks.Env {
	t.Helper()
	env := map[string]string{"GITHUB_EVENT_NAME": name}
	if payload != "" {
		p := filepath.Join(t.TempDir(), "event.json")
		if err := os.WriteFile(p, []byte(payload), 0o600); err != nil {
			t.Fatal(err)
		}
		env["GITHUB_EVENT_PATH"] = p
	}
	for k, v := range extra {
		env[k] = v
	}
	return hooks.Env{Getenv: func(k string) string { return env[k] }, Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}}
}

// R5.26: a forked pull request job on a public repository fails at once, before
// the gate, with a message that names the policy.
func TestForkPullRequestFailsAtJobStart(t *testing.T) {
	const fork = `{"repository":{"private":false,"full_name":"o/r"},"pull_request":{"head":{"repo":{"fork":true,"full_name":"x/r"}}}}`
	e := forkEvent(t, "pull_request", fork, nil)
	if code := hooks.Run(context.Background(), []string{"job-started"}, e); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	out := e.Stdout.(*bytes.Buffer).String()
	if !strings.Contains(out, "::error title=crsp::") || !strings.Contains(out, "policy") || !strings.Contains(out, "fork pull requests") {
		t.Fatalf("output %q", out)
	}
}

func TestForkPullRequestDecisions(t *testing.T) {
	cases := []struct {
		name, event, payload string
		extra                map[string]string
		want                 bool
	}{
		{"fork flag", "pull_request", `{"repository":{"private":false},"pull_request":{"head":{"repo":{"fork":true}}}}`, nil, true},
		{"other repository name, no flag", "pull_request", `{"repository":{"private":false,"full_name":"o/r"},"pull_request":{"head":{"repo":{"full_name":"x/r"}}}}`, nil, true},
		{"same repository", "pull_request", `{"repository":{"private":false,"full_name":"o/r"},"pull_request":{"head":{"repo":{"fork":false,"full_name":"o/r"}}}}`, nil, false},
		{"deleted fork", "pull_request", `{"repository":{"private":false},"pull_request":{"head":{"repo":null}}}`, nil, true},
		{"target event", "pull_request_target", `{"repository":{"private":false},"pull_request":{"head":{"repo":{"fork":true}}}}`, nil, true},
		{"private repository", "pull_request", `{"repository":{"private":true},"pull_request":{"head":{"repo":{"fork":true}}}}`, nil, false},
		{"visibility unknown counts as public", "pull_request", `{"pull_request":{"head":{"repo":{"fork":true}}}}`, nil, true},
		{"policy allows", "pull_request", `{"repository":{"private":false},"pull_request":{"head":{"repo":{"fork":true}}}}`, map[string]string{hooks.AllowForkEnv: "1"}, false},
		{"push is never refused", "push", `{"repository":{"private":false}}`, nil, false},
		{"unreadable payload", "pull_request", "{not json", nil, true},
		{"no payload path", "pull_request", "", nil, true},
	}
	for _, c := range cases {
		e := forkEvent(t, c.event, c.payload, c.extra)
		if got := hooks.ForkPullRequest(e.Getenv); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}
