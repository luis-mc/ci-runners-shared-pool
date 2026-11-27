package api

import (
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/addresses"
	"sync/atomic"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/scheduler/slots"
)

// githubAddresses are the GitHub addresses the job links use. The controller
// sets them once at start (SetGitHubAddresses); until then they are the
// github.com defaults of the config package.
var githubAddresses atomic.Pointer[addresses.Set]

// SetGitHubAddresses sets the addresses the job pages are built from.
func SetGitHubAddresses(a addresses.Set) { githubAddresses.Store(&a) }

func githubSet() addresses.Set {
	if a := githubAddresses.Load(); a != nil {
		return *a
	}
	return addresses.Defaults()
}

// jobPage is the GitHub page of a job.
func jobPage(j slots.JobRecord) string {
	return githubSet().JobPage(j.Repository, j.RunID, j.ID)
}
