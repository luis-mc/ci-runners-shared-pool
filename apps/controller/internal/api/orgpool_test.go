package api

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestOrgPoolOverHTTP(t *testing.T) { // github-integration R5.21
	h := newHarness(t)
	owner, csrf := h.setup(1, "alice")
	hdr := map[string]string{"X-CSRF-Token": csrf}
	h.fake.AddInstall(10, "alice", "User", 1)
	h.fake.AddRepo(100, 10, "alice", "app", true, 1)
	h.fake.AddInstall(20, "acme", "Organization", 1)
	h.fake.AddRepo(200, 20, "acme", "api", true, 1)
	h.fake.AddRepo(201, 20, "acme", "web", true, 1)
	h.fake.AddOrgMember("acme", "alice", "admin")
	if res, b := h.do("GET", "/api/v1/github/repositories", nil, owner, nil); res.StatusCode != 200 {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	res, b := h.do("POST", "/api/v1/repository-plans", map[string]any{"changes": []map[string]any{{"repository_id": 200, "pool": "on"}}}, owner, hdr)
	if res.StatusCode != 201 {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	var plan struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(b, &plan)
	if res, b := h.do("POST", "/api/v1/repository-plans/"+plan.ID+"/apply", map[string]any{"approved_steps": []string{}}, owner, hdr); res.StatusCode != 202 {
		t.Fatalf("%d %s", res.StatusCode, b)
	}

	var list struct {
		Items []struct {
			InstallationID int64    `json:"installation_id"`
			Organization   string   `json:"organization"`
			IsOrganization bool     `json:"is_organization"`
			Eligible       bool     `json:"eligible"`
			Enabled        bool     `json:"enabled"`
			Repositories   []string `json:"repositories"`
		}
	}
	_, b = h.do("GET", "/api/v1/github/org-pools", nil, owner, nil)
	_ = json.Unmarshal(b, &list)
	if len(list.Items) != 2 {
		t.Fatalf("%s", b)
	}

	// A personal account is refused, and says why.
	res, b = h.do("PUT", "/api/v1/github/installations/10/org-pool", map[string]any{"enabled": true}, owner, hdr)
	if res.StatusCode != 409 || !strings.Contains(string(b), "personal account") {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	// The organization owner turns it on: one group, the selected repository only.
	res, b = h.do("PUT", "/api/v1/github/installations/20/org-pool", map[string]any{"enabled": true}, owner, hdr)
	if res.StatusCode != 200 || !strings.Contains(string(b), `"enabled":true`) || !strings.Contains(string(b), `"acme/api"`) || strings.Contains(string(b), `"acme/web"`) {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	if g, ok := h.fake.GroupByName("acme", "crsp"); !ok || g.Visibility != "selected" || len(g.Repos) != 1 || g.Repos[0] != 200 {
		t.Fatalf("the group is visible to the selected repository only: %+v %v", g, ok)
	}
	// A member who is an admin of the product is refused: not an owner on GitHub.
	h.fake.AddUser(2, "bob")
	h.fake.AddOrgMember("acme", "bob", "member")
	res, b = h.do("POST", "/api/v1/users", map[string]string{"login": "bob", "role": "admin"}, owner, hdr)
	if res.StatusCode != 201 {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	bob, bcsrf := h.signIn(2, cookies{})
	res, b = h.do("PUT", "/api/v1/github/installations/20/org-pool", map[string]any{"enabled": false}, bob, map[string]string{"X-CSRF-Token": bcsrf})
	if res.StatusCode != 403 || !strings.Contains(string(b), "not an owner of the acme organization") {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	// The owner turns it off.
	res, b = h.do("PUT", "/api/v1/github/installations/20/org-pool", map[string]any{"enabled": false}, owner, hdr)
	if res.StatusCode != 200 || !strings.Contains(string(b), `"enabled":false`) {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
}
