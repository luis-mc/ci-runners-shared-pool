package checks

import (
	"context"
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/doctor"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/insights/workflows"
)

type wfSource map[int64][]workflows.Result

func (w wfSource) Latest(_ context.Context, id int64) (string, []workflows.Result, error) {
	rs, ok := w[id]
	if !ok {
		return "", nil, nil
	}
	return "abc", rs, nil
}

// TestWorkflowFindings: one finding per repository and check ID, with jobs and lines (R5.43).
func TestWorkflowFindings(t *testing.T) {
	SetWorkflowSource(wfSource{
		1: {
			{Check: workflows.CheckTimeout, Job: ".github/workflows/ci.yml#a", Status: workflows.Fail, Line: 3, Detail: "No timeout. Fix: add timeout-minutes."},
			{Check: workflows.CheckTimeout, Job: ".github/workflows/ci.yml#b", Status: workflows.OK},
			{Check: workflows.CheckRetry, Job: ".github/workflows/ci.yml#a", Status: workflows.OK},
			{Check: workflows.CheckCache, Job: ".github/workflows/ci.yml", Status: workflows.Skip, Detail: "bad yaml"},
		},
	})
	defer SetWorkflowSource(nil)
	env := doctor.Env{GitHub: newGH(), Fleet: &fleet{repos: []doctor.Repository{repo(1, "o/r", nil), repo(2, "o/none", nil)}}}
	fs := run(t, env, "workflow")
	var timeout, retry, cache []doctor.Finding
	for _, f := range fs {
		switch f.Check {
		case "workflow.timeout":
			timeout = append(timeout, f)
		case "workflow.retry":
			retry = append(retry, f)
		case "workflow.cache":
			cache = append(cache, f)
		}
	}
	var failed doctor.Finding
	for _, f := range timeout {
		if f.Subject.Name == "o/r" {
			failed = f
		}
	}
	if failed.Severity != doctor.Warn || !strings.Contains(failed.Detail, ".github/workflows/ci.yml:3 (job a)") || failed.Fix.Text == "" {
		t.Fatalf("timeout: %+v", timeout)
	}
	if len(timeout) != 2 { // o/r fails, o/none has nothing stored: skip
		t.Fatalf("timeout count: %+v", timeout)
	}
	if len(retry) != 2 || retry[0].Subject.Name == "o/r" && retry[0].Severity != doctor.OK {
		t.Fatalf("retry: %+v", retry)
	}
	for _, f := range cache {
		if f.Subject.Name == "o/r" && f.Severity != doctor.Skip {
			t.Fatalf("cache: %+v", f)
		}
	}
}

func TestWorkflowFindingsWithoutSource(t *testing.T) {
	SetWorkflowSource(nil)
	env := doctor.Env{GitHub: newGH(), Fleet: &fleet{repos: []doctor.Repository{repo(1, "o/r", nil)}}}
	f := only(t, run(t, env, "workflow.timeout"), "workflow.timeout")
	if f.Severity != doctor.Skip {
		t.Fatalf("%+v", f)
	}
}
