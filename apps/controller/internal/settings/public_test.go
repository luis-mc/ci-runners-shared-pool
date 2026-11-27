package settings

import (
	"context"
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/audit"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/store"
)

func TestPublicRepositoriesDefaultsOff(t *testing.T) {
	s, _ := setup(t)
	v, err := s.GetPublicRepositories(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if v.Enabled || !v.ChangedAt.IsZero() || v.ChangedBy != "" {
		t.Fatalf("default must be off and never changed: %+v", v)
	}
}

func TestPublicRepositoriesChangeIsStoredAndAuditedBothWays(t *testing.T) {
	ctx := context.Background()
	s, l := setup(t)
	on, changed, err := s.SetPublicRepositories(ctx, owner, true)
	if err != nil || !changed || !on.Enabled || on.ChangedBy != "octo" || on.ChangedAt.IsZero() {
		t.Fatalf("on: %+v changed=%v err=%v", on, changed, err)
	}
	if got, _ := s.PublicRepositoriesOn(ctx); !got {
		t.Fatal("the stored value must read back on")
	}
	// The same value again changes and audits nothing.
	if _, changed, err := s.SetPublicRepositories(ctx, owner, true); err != nil || changed {
		t.Fatalf("repeat: changed=%v err=%v", changed, err)
	}
	if _, changed, err := s.SetPublicRepositories(ctx, audit.Actor{Login: "ada", Source: audit.SourceWeb}, false); err != nil || !changed {
		t.Fatalf("off: changed=%v err=%v", changed, err)
	}
	if got, _ := s.PublicRepositoriesOn(ctx); got {
		t.Fatal("the stored value must read back off")
	}
	p, err := l.List(ctx, audit.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	var acts []string
	for _, it := range p.Items {
		acts = append(acts, it.Action+"/"+it.Actor)
	}
	joined := strings.Join(acts, " ")
	if len(p.Items) != 2 || !strings.Contains(joined, ActionPublicOn+"/octo") || !strings.Contains(joined, ActionPublicOff+"/ada") {
		t.Fatalf("audit rows: %v", acts)
	}
}

func TestPublicRepositoriesUnreadableRowIsOff(t *testing.T) {
	ctx := context.Background()
	s, _ := setup(t)
	if err := s.db.WithTx(ctx, func(tx *store.Tx) error { return tx.SetSetting(keyPublic, "not json", "x") }); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.PublicRepositoriesOn(ctx); got {
		t.Fatal("a bad row must read as off")
	}
}
