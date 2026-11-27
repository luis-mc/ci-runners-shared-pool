package checks

import (
	"context"
	"fmt"
	"strings"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/doctor"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/routing"
)

// OwnerLogin is the account whose repositories crsp opens the per-job pull
// request on (github-integration R5.107). On any other repository the doctor
// shows the patch and opens nothing.
const OwnerLogin = "luis-mc"

// workflowPerJob: a routed runs-on that still reads only CRSP_RUNS_ON_<OS> instead
// of the per-job form fromJSON(vars.CRSP_<JOB> || vars.CRSP_RUNS_ON_<OS> || '"..."').
// It is info, never a failure: the job runs as before. On the owner's
// repositories crsp opens one pull request; elsewhere the finding carries the
// patch to copy or download (github-integration R5.107, R5.108).
func workflowPerJob(ctx context.Context, env doctor.Env) []doctor.Finding {
	repos, out := poolRepos(ctx, env, "workflow.perjob")
	for _, r := range repos {
		files, err := repoWorkflowFiles(ctx, env, r)
		if err != nil {
			out = append(out, skipFor("workflow.perjob", repoSubject(r), "The workflows of "+r.FullName, err))
			continue
		}
		raw := map[string][]byte{}
		for _, f := range files {
			raw[f.Path] = f.Content
		}
		routed := routing.Routed(raw)
		if len(routed) == 0 {
			continue
		}
		patch := routing.MakePatch(raw)
		f := doctor.Finding{Check: "workflow.perjob", Subject: repoSubject(r)}
		if patch.Empty() {
			f.Severity, f.Title = doctor.OK, fmt.Sprintf("Every routed job in %s reads its own variable (%d jobs)", r.FullName, len(routed))
			out = append(out, f)
			continue
		}
		n := 0
		var lines []string
		for _, fp := range patch.Files {
			for _, c := range fp.Changes {
				n++
				lines = append(lines, fmt.Sprintf("%s:%d (job %s)", fp.Path, c.Line, c.Job))
			}
		}
		f.Severity = doctor.Info
		f.Title = fmt.Sprintf("%d jobs in %s still read only the shared variable", n, r.FullName)
		f.Detail = strings.Join(lines, "\n")
		if strings.EqualFold(r.Owner, OwnerLogin) {
			f.Fix = doctor.Fix{Text: "crsp opens one pull request that changes each routed runs-on to the per-job form.", Link: "/repositories"}
		} else {
			f.Fix = doctor.Fix{Text: "Apply this patch to the workflow files (copy it, or download it as crsp-per-job.patch).", Command: patch.Diff()}
		}
		out = append(out, f)
	}
	return out
}
