package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api/gen"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/auth"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/github"
)

type stalePage struct {
	Items []struct {
		FullName string `json:"full_name"`
	} `json:"items"`
	Stale struct {
		Stale   bool   `json:"stale"`
		Reason  string `json:"reason"`
		Status  int    `json:"status"`
		Message string `json:"message"`
	} `json:"stale"`
}

// A GitHub error on the refresh is shown with its real status and words,
// never as "not answering" (CRSP148, UX-28).
func TestStaleListCarriesTheRealGitHubError(t *testing.T) {
	h := newHarness(t)
	owner, _ := h.setup(1, "alice")
	h.fake.AddInstall(10, "alice", "User", 1)
	h.fake.AddRepo(100, 10, "alice", "app", true, 1)
	h.fake.FailMatching("/app/installations", 422, 10)
	var page stalePage
	res, b := h.do("GET", "/api/v1/github/repositories", nil, owner, nil)
	if err := json.Unmarshal(b, &page); err != nil || res.StatusCode != 200 {
		t.Fatalf("%d %s %v", res.StatusCode, b, err)
	}
	if !page.Stale.Stale || page.Stale.Reason != "github_error" || page.Stale.Status != 422 {
		t.Fatalf("stale %+v", page.Stale)
	}
}

func TestStaleListWhenGitHubIsDown(t *testing.T) {
	h := newHarness(t)
	owner, _ := h.setup(1, "alice")
	h.fake.AddInstall(10, "alice", "User", 1)
	h.fake.FailMatching("/app/installations", 503, 10)
	var page stalePage
	_, b := h.do("GET", "/api/v1/github/repositories", nil, owner, nil)
	if err := json.Unmarshal(b, &page); err != nil {
		t.Fatal(err)
	}
	if !page.Stale.Stale || page.Stale.Reason != "github_error" || page.Stale.Status != 503 {
		t.Fatalf("stale %+v", page.Stale)
	}
}

func TestStaleCauseNamesEachReason(t *testing.T) {
	api := func(status int, msg string, sentinel error) error {
		return &github.APIError{Status: status, Message: msg, Err: fmt.Errorf("%w: %s", sentinel, msg)}
	}
	cases := []struct {
		name   string
		err    error
		reason gen.StalenessReason
		status int
		msg    string
	}{
		{"the sign-in ended", fmt.Errorf("sync: %w", auth.ErrGitHubSessionExpired), gen.SessionExpired, 0, "sign in with GitHub again"},
		{"GitHub unreachable", fmt.Errorf("%w: connection refused", github.ErrUnavailable), gen.Unreachable, 0, ""},
		{"a permission is missing", api(403, "Resource not accessible by integration", github.ErrForbidden), gen.PermissionMissing, 403, "Resource not accessible by integration"},
		{"GitHub says 422", errors.New("github: status 422: Validation Failed"), gen.GithubError, 0, "github: status 422: Validation Failed"},
		{"GitHub answers 500", api(500, "Server Error", github.ErrUnavailable), gen.GithubError, 500, "Server Error"},
	}
	for _, c := range cases {
		reason, status, msg := staleCause(c.err)
		if reason != c.reason || status != c.status || (c.msg != "" && msg != c.msg) {
			t.Errorf("%s: got %s %d %q", c.name, reason, status, msg)
		}
	}
}

// With no GitHub user token in memory (after a restart) the list still
// refreshes through the App and is not stale (OPS13).
func TestListRefreshesThroughTheAppWithoutAUserToken(t *testing.T) {
	h := newHarness(t)
	owner, _ := h.setup(1, "alice")
	h.fake.AddInstall(10, "alice", "User", 1)
	h.fake.AddRepo(100, 10, "alice", "app", true, 1)
	var page stalePage
	res, b := h.do("GET", "/api/v1/github/repositories", nil, owner, nil)
	if err := json.Unmarshal(b, &page); err != nil || res.StatusCode != 200 {
		t.Fatalf("%d %s %v", res.StatusCode, b, err)
	}
	if page.Stale.Stale || len(page.Items) != 1 || page.Items[0].FullName != "alice/app" {
		t.Fatalf("page %+v", page)
	}
}

// When the App call fails the reason is the App's error, never
// session_expired (OPS13).
func TestStaleReasonIsTheAppErrorNotTheSignIn(t *testing.T) {
	h := newHarness(t)
	owner, _ := h.setup(1, "alice")
	h.fake.AddInstall(10, "alice", "User", 1)
	h.fake.FailMatching("/installation/repositories", 403, 10)
	var page stalePage
	_, b := h.do("GET", "/api/v1/github/repositories", nil, owner, nil)
	if err := json.Unmarshal(b, &page); err != nil {
		t.Fatal(err)
	}
	if !page.Stale.Stale || page.Stale.Reason == "session_expired" || page.Stale.Reason == "" {
		t.Fatalf("stale %+v", page.Stale)
	}
}
