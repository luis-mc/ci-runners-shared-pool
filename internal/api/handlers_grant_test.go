package api

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func grantRepo(t *testing.T) (*harness, cookies, string) {
	t.Helper()
	h := newHarness(t)
	owner, csrf := h.setup(1, "alice")
	h.fake.AddInstall(10, "alice", "User", 1)
	h.fake.AddRepo(100, 10, "alice", "app", true, 1)
	_, _ = h.do("GET", "/api/v1/github/repositories", nil, owner, nil)
	return h, owner, csrf
}

type grantOut struct {
	Ready bool
	Next  string
	Steps []struct {
		ID          string
		Done        bool
		Permissions []string
		URL         string
	}
	Missing []string
}

func getGrant(t *testing.T, h *harness, owner cookies, query string) grantOut {
	t.Helper()
	res, b := h.do("GET", "/api/v1/github/grant?"+query, nil, owner, nil)
	if res.StatusCode != 200 {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	var g grantOut
	if err := json.Unmarshal(b, &g); err != nil {
		t.Fatal(err)
	}
	return g
}

func TestGrantReadsBothSidesOfGitHub(t *testing.T) { // R5.86
	h, owner, _ := grantRepo(t)
	h.fake.Installs[10].Perms["secrets"] = "read"
	// The App does not ask for it yet: step ask is next.
	h.fake.SetAppPermissions(map[string]string{"actions_variables": "write"})
	g := getGrant(t, h, owner, "permissions=secrets,actions_variables&repository_id=100")
	if g.Ready || g.Next != "ask" || strings.Join(g.Missing, ",") != "secrets" {
		t.Fatalf("not asked: %+v", g)
	}
	// The App asks, the installation has not accepted: step accept is next.
	h.fake.SetAppPermissions(map[string]string{"actions_variables": "write", "secrets": "write"})
	g = getGrant(t, h, owner, "permissions=secrets,actions_variables&repository_id=100")
	if g.Ready || g.Next != "accept" || !g.Steps[1].Done || g.Steps[2].Done || g.Steps[2].URL != "https://github.com/settings/installations/10" {
		t.Fatalf("asked, not accepted: %+v", g)
	}
	// The installation accepts: everything is granted.
	h.fake.Installs[10].Perms["secrets"] = "write"
	g = getGrant(t, h, owner, "permissions=secrets,actions_variables&installation_id=10")
	if !g.Ready || g.Next != "" || len(g.Missing) != 0 || !g.Steps[1].Done || !g.Steps[2].Done {
		t.Fatalf("all done: %+v", g)
	}
}

func TestGrantWithoutAnInstallationStartsWithInstall(t *testing.T) { // R5.86
	h := newHarness(t)
	owner, _ := h.setup(1, "alice")
	g := getGrant(t, h, owner, "permissions=secrets")
	if g.Ready || g.Next != "install" || len(g.Steps) != 3 || g.Steps[0].ID != "install" ||
		!strings.HasPrefix(g.Steps[0].URL, "https://github.com/apps/") || !strings.HasSuffix(g.Steps[0].URL, "/installations/new") {
		t.Fatalf("%+v", g)
	}
}

func TestGrantNeedsAPermissionNameAndAnAdmin(t *testing.T) { // R5.86
	h, owner, _ := grantRepo(t)
	if res, _ := h.do("GET", "/api/v1/github/grant", nil, owner, nil); res.StatusCode != 400 {
		t.Fatalf("no permission named: %d", res.StatusCode)
	}
	if res, _ := h.do("GET", "/api/v1/github/grant?permissions=secrets", nil, nil, nil); res.StatusCode != 401 {
		t.Fatalf("signed out: %d", res.StatusCode)
	}
}

func TestAcceptedPermissionsRefreshTheInstallationToken(t *testing.T) { // R5.87
	h, owner, _ := grantRepo(t)
	h.fake.Installs[10].Perms["secrets"] = "read"
	// The owner accepts on GitHub; the next read sees it and the token is new.
	before := h.fake.TokensMade()
	h.fake.Installs[10].Perms["secrets"] = "write"
	g := getGrant(t, h, owner, "permissions=secrets&installation_id=10")
	if !g.Ready {
		t.Fatalf("%+v", g)
	}
	if err := h.repos.PermissionsAccepted(context.Background(), 10); err != nil {
		t.Fatal(err)
	}
	if h.fake.TokensMade() <= before {
		t.Fatalf("no new installation token after the permissions were accepted: %d <= %d", h.fake.TokensMade(), before)
	}
}
