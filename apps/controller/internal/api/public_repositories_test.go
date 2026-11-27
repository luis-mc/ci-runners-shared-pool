package api

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/audit"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backup"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/repos"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/settings"
)

// publicHarness is the console with a real settings service behind the
// setting Public repositories.
func publicHarness(t *testing.T) (*harness, *settings.Service, *audit.Log) {
	t.Helper()
	db, err := backup.OpenDBForTests(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	set := settings.New(db)
	h := newHarnessWith(t, nil, func(c *repos.Config, o *Options) {
		c.PublicRepositories = set.PublicRepositoriesOn
		o.Settings = set
	})
	return h, set, audit.New(db)
}

func applyOn(t *testing.T, h *harness, id int, who cookies, hdr map[string]string) (int, string) {
	t.Helper()
	res, b := h.do("POST", "/api/v1/repository-plans", map[string]any{"changes": []map[string]any{{"repository_id": id, "pool": "on"}}}, who, hdr)
	if res.StatusCode != 201 {
		return res.StatusCode, string(b)
	}
	var plan struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(b, &plan)
	res, b = h.do("POST", "/api/v1/repository-plans/"+plan.ID+"/apply", map[string]any{"approved_steps": []string{}}, who, hdr)
	return res.StatusCode, string(b)
}

// github-integration R5.54 to R5.58 over HTTP: default off, owner only,
// refusal with 409 public_repositories_off, off drains and counts, and the
// problem on a public repository that stayed on the pool.
func TestPublicRepositoriesSettingOverHTTP(t *testing.T) {
	h, set, log := publicHarness(t)
	owner, csrf := h.setup(1, "alice")
	hdr := map[string]string{"X-CSRF-Token": csrf}
	h.fake.AddInstall(10, "alice", "User", 1)
	h.fake.AddRepo(100, 10, "alice", "pub", false, 1)
	h.fake.AddRepo(101, 10, "alice", "priv", true, 1)
	if res, b := h.do("GET", "/api/v1/github/repositories", nil, owner, nil); res.StatusCode != 200 || !strings.Contains(string(b), `"visibility":"public"`) {
		t.Fatalf("%d %s", res.StatusCode, b)
	}

	res, b := h.do("GET", "/api/v1/settings/public-repositories", nil, owner, nil)
	if res.StatusCode != 200 || !strings.Contains(string(b), `"enabled":false`) || !strings.Contains(string(b), `"public_on_pool":0`) || strings.Contains(string(b), "changed_at") {
		t.Fatalf("default: %d %s", res.StatusCode, b)
	}

	// Off: a public repository is refused with 409 and the code, a private one is not.
	code, body := applyOn(t, h, 100, owner, hdr)
	var env struct {
		Error struct{ Code, Message string }
	}
	_ = json.Unmarshal([]byte(body), &env)
	if code != 409 || env.Error.Code != CodePublicRepositoriesOff || !strings.Contains(env.Error.Message, "Public repositories") || !strings.Contains(env.Error.Message, "alice/pub") {
		t.Fatalf("refusal: %d %s", code, body)
	}
	if code, body := applyOn(t, h, 101, owner, hdr); code != 202 {
		t.Fatalf("private: %d %s", code, body)
	}

	// Only the owner changes it: an admin is refused.
	h.fake.AddUser(2, "bob")
	if res, b := h.do("POST", "/api/v1/users", map[string]string{"login": "bob", "role": "admin"}, owner, hdr); res.StatusCode != 201 {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	bob, bcsrf := h.signIn(2, cookies{})
	if res, b := h.do("PUT", "/api/v1/settings/public-repositories", map[string]any{"enabled": true}, bob, map[string]string{"X-CSRF-Token": bcsrf}); res.StatusCode != 403 {
		t.Fatalf("admin: %d %s", res.StatusCode, b)
	}
	if on, _ := set.PublicRepositoriesOn(context.Background()); on {
		t.Fatal("a refused change was applied")
	}

	// On: allowed; the pool turns on for the public repository.
	res, b = h.do("PUT", "/api/v1/settings/public-repositories", map[string]any{"enabled": true}, owner, hdr)
	if res.StatusCode != 200 || !strings.Contains(string(b), `"enabled":true`) || !strings.Contains(string(b), `"stopped":0`) || !strings.Contains(string(b), `"changed_by":"alice"`) {
		t.Fatalf("on: %d %s", res.StatusCode, b)
	}
	if code, body := applyOn(t, h, 100, owner, hdr); code != 202 {
		t.Fatalf("public with the setting on: %d %s", code, body)
	}
	_, b = h.do("GET", "/api/v1/settings/public-repositories", nil, owner, nil)
	if !strings.Contains(string(b), `"public_on_pool":1`) {
		t.Fatalf("count: %s", b)
	}

	// The problem: the setting goes off behind the controller's back (as an
	// older release left it), the public repository stays on the pool.
	if _, _, err := set.SetPublicRepositories(context.Background(), audit.Actor{Login: "alice"}, false); err != nil {
		t.Fatal(err)
	}
	res, b = h.do("GET", "/api/v1/repositories/100", nil, owner, nil)
	if res.StatusCode != 200 || !strings.Contains(string(b), `"code":"public_pool_on_while_off"`) ||
		!strings.Contains(string(b), `"turn_on_public_repositories"`) || !strings.Contains(string(b), `"turn_off_pool"`) {
		t.Fatalf("problem: %d %s", res.StatusCode, b)
	}
	if res, b = h.do("GET", "/api/v1/repositories/101", nil, owner, nil); strings.Contains(string(b), `"problem"`) {
		t.Fatalf("a private repository has no problem: %d %s", res.StatusCode, b)
	}

	// Off through the API drains it and says how many.
	res, b = h.do("PUT", "/api/v1/settings/public-repositories", map[string]any{"enabled": false}, owner, hdr)
	if res.StatusCode != 200 || !strings.Contains(string(b), `"enabled":false`) || !strings.Contains(string(b), `"stopped":1`) || !strings.Contains(string(b), `"public_on_pool":0`) {
		t.Fatalf("off: %d %s", res.StatusCode, b)
	}
	_, b = h.do("GET", "/api/v1/repositories/100", nil, owner, nil)
	if !strings.Contains(string(b), `"pool_on":false`) || strings.Contains(string(b), `"problem"`) {
		t.Fatalf("after off: %s", b)
	}
	if res, b = h.do("GET", "/api/v1/repositories/101", nil, owner, nil); !strings.Contains(string(b), `"pool_on":true`) {
		t.Fatalf("the private repository was touched: %d %s", res.StatusCode, b)
	}
	p, err := log.List(context.Background(), audit.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	acts := ""
	for _, it := range p.Items {
		acts += it.Action + " "
	}
	if !strings.Contains(acts, settings.ActionPublicOn) || !strings.Contains(acts, settings.ActionPublicOff) {
		t.Fatalf("audit: %s", acts)
	}
}

// The add-runners path answers the same 409 and code as the pool path (R5.55).
func TestRunnerFailMapsPublicOff(t *testing.T) {
	code, body := runnerFail(&repos.PublicOffError{Repo: "alice/pub"})
	if code != 409 || body.Error.Code != CodePublicRepositoriesOff || !strings.Contains(body.Error.Message, "alice/pub") {
		t.Fatalf("%d %+v", code, body)
	}
}
