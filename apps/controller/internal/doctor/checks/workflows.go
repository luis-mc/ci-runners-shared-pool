package checks

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/doctor"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/insights/workflows"
)

// WorkflowSource reads the stored workflow file checks (CI run insights R5.43). *store.Store's
// WorkflowChecks() satisfies it; the doctor reads only what the insights collector already stored.
type WorkflowSource interface {
	Latest(ctx context.Context, repositoryID int64) (headSHA string, results []workflows.Result, err error)
}

var workflowSource atomic.Pointer[WorkflowSource]

// SetWorkflowSource sets where the workflows.* checks read from (called once at wiring). With none set the
// checks skip.
func SetWorkflowSource(s WorkflowSource) {
	if s == nil {
		workflowSource.Store(nil)
		return
	}
	workflowSource.Store(&s)
}

// workflowDoctorChecks lists the check IDs of the area Workflows (R5.43): the IDs of R5.29.
func workflowCheckIDs() []workflows.CheckID { return workflows.AllChecks }

func registerWorkflowChecks(reg *doctor.Registry) {
	for _, id := range workflowCheckIDs() {
		id := id
		reg.Register(doctor.NewCheck(string(id), doctor.GroupWorkflow, M1, false,
			func(ctx context.Context, env doctor.Env) []doctor.Finding { return workflowFindings(ctx, env, id) }))
	}
}

// workflowFindings: one finding per repository for check id. A failing check lists its jobs and lines and
// names the fix; a clean one says how many jobs were checked (R5.43). A repository with nothing stored yet
// is a skip.
func workflowFindings(ctx context.Context, env doctor.Env, id workflows.CheckID) []doctor.Finding {
	check := string(id)
	sp := workflowSource.Load()
	if sp == nil {
		return []doctor.Finding{skip(check, controllerSubject, "The workflow file checks are not available",
			"This controller has not stored workflow file checks yet.")}
	}
	repos, out := poolRepos(ctx, env, check)
	for _, r := range repos {
		s := repoSubject(r)
		sha, results, err := (*sp).Latest(ctx, r.ID)
		if err != nil {
			out = append(out, skipFor(check, s, "The workflow file checks of "+r.FullName, err))
			continue
		}
		if sha == "" {
			out = append(out, skip(check, s, "The workflow files of "+r.FullName+" were not checked yet",
				"crsp checks them at each new commit of the default branch; none was checked yet."))
			continue
		}
		var fails []string
		checked, skipped := 0, 0
		var first string
		for _, res := range results {
			if res.Check != id {
				continue
			}
			switch res.Status {
			case workflows.Skip:
				skipped++
			case workflows.Fail:
				checked++
				path, job := workflows.SplitJob(res.Job)
				fails = append(fails, fmt.Sprintf("%s:%d (job %s): %s", path, res.Line, job, res.Detail))
				if first == "" {
					first = res.Detail
				}
			default:
				checked++
			}
		}
		switch {
		case len(fails) > 0:
			out = append(out, doctor.Finding{Check: check, Subject: s, Severity: doctor.Warn,
				Title:  fmt.Sprintf("%d %s in %s fail %s", len(fails), jobWord(len(fails)), r.FullName, check),
				Detail: strings.Join(fails, "\n"),
				Fix:    doctor.Fix{Text: fixText(id), Link: "/repositories/" + fmt.Sprint(r.ID)}})
		case checked == 0 && skipped > 0:
			out = append(out, skip(check, s, "The workflow files of "+r.FullName+" could not be checked",
				"A file could not be read or parsed. Fix the file, then the next commit is checked again."))
		case checked == 0:
			// no job in the workflow files this check looks at (for example, no matrix)
			continue
		default:
			out = append(out, doctor.Finding{Check: check, Subject: s, Severity: doctor.OK,
				Title: fmt.Sprintf("%s passes in %s (%d %s checked)", check, r.FullName, checked, jobWord(checked))})
		}
	}
	return out
}

func jobWord(n int) string {
	if n == 1 {
		return "job"
	}
	return "jobs"
}

func fixText(id workflows.CheckID) string {
	switch id {
	case workflows.CheckTimeout:
		return "Add `timeout-minutes: 60` or less to each job."
	case workflows.CheckRetry:
		return "Remove the test retry and fix or quarantine the flaky test."
	case workflows.CheckMatrixAxis:
		return "Keep a new matrix axis or value only where it finds bugs, or run it on a schedule."
	case workflows.CheckStepOrder:
		return "Make the end-to-end job need the lint and type check job, and run lint and type steps before tests."
	case workflows.CheckCache:
		return "Turn on the package manager cache in the set-up step."
	case workflows.CheckSlowTestsOnPR:
		return "Run mutation and many-run property tests on a schedule or on the default branch only."
	}
	return "See the finding detail."
}
