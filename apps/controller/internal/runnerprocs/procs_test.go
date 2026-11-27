package runnerprocs_test

import (
	"os"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runnerprocs"
)

// The list names this very process, with an owner in the form Self gives.
func TestListIncludesThisProcess(t *testing.T) {
	all, err := runnerprocs.List()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range all {
		if p.PID == int64(os.Getpid()) {
			if p.Name == "" {
				t.Fatalf("this process has no name: %+v", p)
			}
			return
		}
	}
	t.Fatalf("pid %d not in the process list of %d entries", os.Getpid(), len(all))
}
