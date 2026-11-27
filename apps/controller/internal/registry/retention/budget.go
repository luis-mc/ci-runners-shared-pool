package retention

import (
	"context"
	"fmt"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry"
)

// BudgetReport is the budgets with the free disk of the registry folder's volume.
type BudgetReport struct {
	Standard, Custom registry.Budget
	FreeDiskBytes    int64
	// MaxTotalBytes is half the free disk plus what the registry holds now: the
	// two limits may not add up to more (R5.41).
	MaxTotalBytes int64
}

// BudgetTooBig is a budget sum above the most allowed.
type BudgetTooBig struct{ MaxTotalBytes int64 }

func (e *BudgetTooBig) Error() string {
	return fmt.Sprintf("the two budgets may add up to at most %d bytes (half the free disk)", e.MaxTotalBytes)
}

// Budgets returns the standard and custom budgets with what each uses.
func (s *Service) Budgets(ctx context.Context) ([]registry.Budget, error) {
	r, err := s.BudgetReport(ctx)
	if err != nil {
		return nil, err
	}
	return []registry.Budget{r.Standard, r.Custom}, nil
}

// BudgetReport computes the budgets, the free disk and the most allowed.
func (s *Service) BudgetReport(ctx context.Context) (BudgetReport, error) {
	st, err := s.Settings(ctx)
	if err != nil {
		return BudgetReport{}, err
	}
	use, err := s.o.Repo.Use(ctx)
	if err != nil {
		return BudgetReport{}, err
	}
	free, err := s.o.Disk.Free(st.Folder)
	if err != nil {
		return BudgetReport{}, fmt.Errorf("free disk of the registry folder: %w", err)
	}
	mk := func(k registry.Kind, limit int64) registry.Budget {
		return registry.Budget{Kind: k, LimitBytes: limit, UsedBytes: use[k].UsedBytes, SharedBytes: use[k].SharedBytes}
	}
	held := use[registry.KindStandard].UsedBytes + use[registry.KindCustom].UsedBytes
	return BudgetReport{
		Standard:      mk(registry.KindStandard, st.StandardBudget),
		Custom:        mk(registry.KindCustom, st.CustomBudget),
		FreeDiskBytes: free,
		MaxTotalBytes: free/2 + held,
	}, nil
}

// PutBudgets saves both budgets when their sum is not above half the free disk
// (plus what the registry holds). Otherwise it returns *BudgetTooBig.
func (s *Service) PutBudgets(ctx context.Context, standard, custom int64, by string) (BudgetReport, error) {
	if standard < 0 || custom < 0 {
		return BudgetReport{}, &ValidationError{"a budget cannot be negative"}
	}
	r, err := s.BudgetReport(ctx)
	if err != nil {
		return r, err
	}
	if standard+custom > r.MaxTotalBytes {
		return r, &BudgetTooBig{MaxTotalBytes: r.MaxTotalBytes}
	}
	st, err := s.o.Repo.Settings(ctx)
	if err != nil {
		return r, err
	}
	st.StandardBudget, st.CustomBudget = standard, custom
	if err := s.o.Repo.PutSettings(ctx, st, by); err != nil {
		return r, err
	}
	if err := s.record(ctx, Actor{Login: by, Source: SourceWeb}, ActionBudgets, "registry", "budgets",
		map[string]any{"summary": "changed the registry budgets", "standard_bytes": standard, "custom_bytes": custom}); err != nil {
		return r, err
	}
	return s.BudgetReport(ctx)
}
