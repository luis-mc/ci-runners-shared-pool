package api

import (
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/settings"
)

// R5.115: the budget of an overflow target goes to the API and back.
func TestOverflowBudgetRoundTrip(t *testing.T) {
	in := []settings.OverflowTarget{{Kind: settings.TargetGitHubHosted, Enabled: true, BudgetUSD: 12.5}}
	got := fromGenTargets(toGenTargets(in))
	if got == nil || len(*got) != 1 || (*got)[0].BudgetUSD != 12.5 {
		t.Fatalf("round trip %+v", got)
	}
}
