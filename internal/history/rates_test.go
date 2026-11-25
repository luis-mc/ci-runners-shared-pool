package history

import (
	"context"
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/internal/audit"
	"github.com/luis-mc/ci-runners-shared-pool/internal/store"
)

func TestRateTableHasSourceAndDate(t *testing.T) {
	s := Shipped()
	if !strings.HasPrefix(s.SourceURL, "https://docs.github.com/") || len(s.CopiedOn) != 10 {
		t.Fatalf("%q %q", s.SourceURL, s.CopiedOn)
	}
	if _, err := s.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRefusesBadTables(t *testing.T) {
	bad := []Table{
		{},
		{Rates: []Rate{{OS: "beos", Size: "standard", PerMinute: 1}}},
		{Rates: []Rate{{OS: "linux", Size: "standard", PerMinute: -1}}},
		{Rates: []Rate{{OS: "linux", Size: "standard", PerMinute: 1}, {OS: "linux", Size: "standard", PerMinute: 2}}},
		{Rates: []Rate{{OS: "linux", Size: "standard", PerMinute: 1}}, MachineSizes: map[string]string{"m": "huge"}},
	}
	for i, b := range bad {
		if _, err := b.Validate(); err == nil {
			t.Errorf("table %d must be refused", i)
		}
	}
}

func TestOverrideAndResetWriteAudit(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenForTests(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	r := &Rates{DB: db}
	if got, _ := r.Get(ctx); got.Changed {
		t.Fatal("a new install has the shipped table")
	}
	tab := Shipped()
	tab.Rates[1].PerMinute = 0.01 // linux standard (sorted: arm, standard)
	if _, err := r.Set(ctx, audit.Actor{Login: "owner", Source: audit.SourceWeb}, tab); err != nil {
		t.Fatal(err)
	}
	got, _ := r.Get(ctx)
	if !got.Changed || got.Rates[1].PerMinute != 0.01 || got.SourceURL != ShippedSourceURL {
		t.Fatalf("%+v", got)
	}
	if _, err := r.Reset(ctx, audit.Actor{Login: "owner", Source: audit.SourceWeb}); err != nil {
		t.Fatal(err)
	}
	if got, _ = r.Get(ctx); got.Changed || got.Rates[1].PerMinute != 0.008 {
		t.Fatalf("reset: %+v", got)
	}
	page, err := audit.New(db).List(ctx, audit.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	actions := ""
	for _, e := range page.Items {
		actions += e.Action + " "
	}
	if !strings.Contains(actions, "savings.rates.change") || !strings.Contains(actions, "savings.rates.reset") {
		t.Fatalf("audit rows: %s", actions)
	}
}
