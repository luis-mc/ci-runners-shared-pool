package api

import (
	"encoding/json"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/jobsdata"
)

// observability R5.4: the job of a job.* event carries the job id and the run id.
func TestJobEventJSONCarriesRunAndJobIDs(t *testing.T) {
	b := JobEventJSON(jobsdata.Job{ID: 42, RunID: 4200, Attempt: 2, Repository: "octo/app", Name: "test (linux)", State: jobsdata.Running})
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got["id"] != float64(42) || got["run_id"] != float64(4200) || got["state"] != "running" || got["run_attempt"] != float64(2) {
		t.Fatalf("payload = %s", b)
	}
}
