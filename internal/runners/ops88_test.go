package runners

import (
	"strings"
	"testing"
)

// OPS88: the plan of a pool runner does not name an account that is never made;
// a repository runner names the account its name gives.
func TestAddTextAccount(t *testing.T) {
	pool := addText(AddSpec{Name: "gameroompc-pool-1", Kind: "persistent", OS: "windows", Scope: Scope{Kind: ScopePool}}, "GameRoomPC")
	if strings.Contains(pool, AccountName("gameroompc-pool-1", nil)) || !strings.Contains(pool, "each repository") {
		t.Errorf("pool text: %s", pool)
	}
	repo := addText(AddSpec{Name: "box-1", Kind: "persistent", OS: "windows", Scope: Scope{Kind: "repository"}}, "Box")
	if !strings.Contains(repo, "own account "+AccountName("box-1", nil)) {
		t.Errorf("repo text: %s", repo)
	}
}
