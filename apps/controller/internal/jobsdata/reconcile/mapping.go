package reconcile

import "github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/jobsdata"

// StateOf maps GitHub's job status to a console state; "" for a status it does not know.
func StateOf(status string) jobsdata.State {
	switch status {
	case "queued", "waiting", "requested", "pending":
		return jobsdata.Queued
	case "in_progress":
		return jobsdata.Running
	case "completed":
		return jobsdata.Done
	}
	return ""
}

// ResultOf maps GitHub's conclusion. A conclusion with no result of ours (skipped, neutral) gives "".
func ResultOf(conclusion string) jobsdata.Result {
	switch conclusion {
	case "success":
		return jobsdata.Success
	case "failure", "startup_failure":
		return jobsdata.Failure
	case "cancelled":
		return jobsdata.Cancelled
	case "timed_out":
		return jobsdata.TimedOut
	}
	return ""
}
