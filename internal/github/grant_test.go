package github

import (
	"context"
	"strings"
	"testing"
)

func TestDiffGrantStates(t *testing.T) { // R5.86
	need := []string{"secrets", "actions_variables"}
	cases := []struct {
		name            string
		asked, accepted map[string]string
		ready           bool
		next            string
		missing         string
		askDone         bool
		askWaiting      string
		acceptWaiting   string
	}{
		{"none done", map[string]string{}, map[string]string{}, false, GrantStepAsk, "actions_variables,secrets", false, "actions_variables,secrets", "actions_variables,secrets"},
		{"asked, not accepted", map[string]string{"secrets": "write", "actions_variables": "write"}, map[string]string{"actions_variables": "write"},
			false, GrantStepAccept, "secrets", true, "", "secrets"},
		{"one asked, one not", map[string]string{"secrets": "write"}, map[string]string{},
			false, GrantStepAsk, "actions_variables,secrets", false, "actions_variables", "actions_variables,secrets"},
		{"too weak is not asked", map[string]string{"secrets": "read", "actions_variables": "write"}, map[string]string{"actions_variables": "write"},
			false, GrantStepAsk, "secrets", false, "secrets", "secrets"},
		{"all done", map[string]string{"secrets": "write", "actions_variables": "write"}, map[string]string{"secrets": "write", "actions_variables": "write"},
			true, "", "", true, "", ""},
		{"admin counts as write", nil, map[string]string{"secrets": "admin", "actions_variables": "write"}, true, "", "", true, "", ""},
	}
	for _, c := range cases {
		g := DiffGrant(need, c.asked, c.accepted)
		if g.Ready != c.ready || g.Next != c.next || strings.Join(g.Missing, ",") != c.missing {
			t.Errorf("%s: ready=%v next=%q missing=%v", c.name, g.Ready, g.Next, g.Missing)
			continue
		}
		if len(g.Steps) != 2 || g.Steps[0].ID != GrantStepAsk || g.Steps[1].ID != GrantStepAccept {
			t.Errorf("%s: steps %v", c.name, g.Steps)
			continue
		}
		if g.Steps[0].Done != c.askDone || strings.Join(g.Steps[0].Permissions, ",") != c.askWaiting ||
			g.Steps[1].Done != c.ready || strings.Join(g.Steps[1].Permissions, ",") != c.acceptWaiting {
			t.Errorf("%s: steps %+v", c.name, g.Steps)
		}
	}
}

func TestGrantPagesForAUserAndAnOrganization(t *testing.T) { // R5.86
	app, ins := GrantPages("https://github.com", App{Slug: "crsp", OwnerType: "User", OwnerLogin: "alice"}, Installation{ID: 10, AccountType: "User", AccountLogin: "alice"})
	if app != "https://github.com/settings/apps/crsp/permissions" || ins != "https://github.com/settings/installations/10" {
		t.Fatalf("%s %s", app, ins)
	}
	app, ins = GrantPages("https://github.com", App{Slug: "crsp", OwnerType: "Organization", OwnerLogin: "acme"}, Installation{ID: 7, AccountType: "Organization", AccountLogin: "acme"})
	if app != "https://github.com/organizations/acme/settings/apps/crsp/permissions" || ins != "https://github.com/organizations/acme/settings/installations/7" {
		t.Fatalf("%s %s", app, ins)
	}
}

func TestParseInstallationEvent(t *testing.T) { // R5.87
	ev, err := ParseInstallationEvent([]byte(`{"action":"new_permissions_accepted","installation":{"id":42}}`))
	if err != nil || ev.Action != NewPermissionsAccepted || ev.InstallationID != 42 {
		t.Fatalf("%+v %v", ev, err)
	}
	if _, err := ParseInstallationEvent([]byte(`{`)); err == nil {
		t.Fatal("a broken body is an error")
	}
}

func TestForgetInstallationTokenMakesANewToken(t *testing.T) { // R5.87
	e := newEnv(t)
	ctx := context.Background()
	e.fake.AddInstall(10, "alice", "User")
	if _, err := e.client.installationToken(ctx, 10); err != nil {
		t.Fatal(err)
	}
	if _, err := e.client.installationToken(ctx, 10); err != nil || e.fake.TokensMade() != 1 {
		t.Fatalf("a live token is reused: made %d (%v)", e.fake.TokensMade(), err)
	}
	e.client.ForgetInstallationToken(10)
	if _, err := e.client.installationToken(ctx, 10); err != nil || e.fake.TokensMade() != 2 {
		t.Fatalf("after the permissions are accepted the next call makes a new token: made %d (%v)", e.fake.TokensMade(), err)
	}
}
