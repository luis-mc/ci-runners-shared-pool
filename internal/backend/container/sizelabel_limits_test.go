package container

import (
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/internal/backend"
)

func TestLimitsForSizeLabelOnlyLowersTheCaps(t *testing.T) {
	b := &Backend{}
	b.cfg.Limits = Limits{MemoryMB: 16384, CPUs: 8}
	got := b.limitsFor(backend.RunnerSpec{Labels: []string{"self-hosted", "cpu=4", "ram=8"}})
	if got.MemoryMB != 8192 || got.CPUs != 4 {
		t.Fatalf("got %+v", got)
	}
	got = b.limitsFor(backend.RunnerSpec{Labels: []string{"cpu=32", "ram=64"}})
	if got.MemoryMB != 16384 || got.CPUs != 8 {
		t.Fatalf("size above machine must not raise caps: %+v", got)
	}
}
