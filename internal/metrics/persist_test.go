package metrics

import (
	"context"
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/internal/audit"
	"github.com/luis-mc/ci-runners-shared-pool/internal/secrets"
	"github.com/luis-mc/ci-runners-shared-pool/internal/store"
)

func TestDBStoreRoundTripAndAudit(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenForTests(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	st := DBStore{DB: db}
	if c, _ := st.Load(ctx); c.Enabled || c.Port != 9464 {
		t.Fatalf("default: %+v", c)
	}
	c := Default()
	c.Enabled, c.RequireToken = true, true
	if err := st.Save(ctx, audit.Actor{Login: "octo", Source: audit.SourceWeb}, c); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.Load(ctx); !got.Enabled || !got.RequireToken {
		t.Fatalf("kept: %+v", got)
	}
	rows, err := db.ListAudit(ctx, store.AuditFilter{Action: ActionChanged})
	if err != nil || len(rows) != 1 || !strings.Contains(rows[0].Detail, `"off -\u003e on"`) && strings.Contains(rows[0].Detail, "page_auth") {
		t.Fatalf("audit: %v %+v", err, rows)
	}
}

func TestSecretToken(t *testing.T) {
	ctx := context.Background()
	tk := SecretToken{Secrets: secrets.NewMemory()}
	if v, err := tk.Get(ctx); v != "" || err != nil {
		t.Fatalf("%q %v", v, err)
	}
	_ = tk.Put(ctx, "abc")
	if v, _ := tk.Get(ctx); v != "abc" {
		t.Fatal(v)
	}
}
