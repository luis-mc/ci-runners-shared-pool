package checks

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/luis-mc/ci-runners-shared-pool/internal/assign"
	"github.com/luis-mc/ci-runners-shared-pool/internal/doctor"
	"github.com/luis-mc/ci-runners-shared-pool/internal/routing"
)

// AssignSource reads the jobs that wait for a runner (runners R5.114). The
// assign controller implements it.
type AssignSource interface {
	// Unserved lists the waiting jobs no machine can ever serve.
	Unserved(ctx context.Context) ([]assign.Lease, error)
	// LongWaiting lists the waiting jobs past the 12 h alert.
	LongWaiting(ctx context.Context) ([]assign.Lease, error)
}

func jobSubject(l assign.Lease) doctor.Subject {
	return doctor.Subject{Kind: "job", ID: fmt.Sprint(l.JobID), Name: l.Repository + " " + l.JobName}
}

// AssignChecks are the checks the assign controller feeds, for the registry or
// DoctorDeps.Extra: `repo.runson_always` and the two queue checks below.
func AssignChecks(src AssignSource) []doctor.Check {
	reg := doctor.NewRegistry()
	RegisterAssign(reg, src)
	return reg.All()
}

// RegisterAssign adds `repo.runson_always` and the two queue checks:
//   - `assign.unserved`: a queued job no machine can ever serve (no runner of
//     its class, label or OS). It is a failure at once: the job would wait the
//     24 hours GitHub allows and be cancelled.
//   - `assign.waiting`: a job that has waited 12 hours or more.
func RegisterAssign(reg *doctor.Registry, src AssignSource) {
	reg.Register(doctor.NewCheck("repo.runson_always", doctor.GroupRepository, M1, false, repoRunsOnAlways))
	reg.Register(doctor.NewCheck("assign.unserved", doctor.GroupRepository, M1, false, func(ctx context.Context, env doctor.Env) []doctor.Finding {
		const id = "assign.unserved"
		ls, err := src.Unserved(ctx)
		if err != nil {
			return []doctor.Finding{skipFor(id, controllerSubject, "The queue", err)}
		}
		var out []doctor.Finding
		for _, l := range ls {
			out = append(out, doctor.Finding{Check: id, Subject: jobSubject(l), Severity: doctor.Fail,
				Title:  fmt.Sprintf("No machine can ever run %s in %s", l.JobName, l.Repository),
				Detail: l.Reason + " GitHub cancels the job after 24 hours in the queue.",
				Fix:    doctor.Fix{Text: "Add a runner of that class, label or system, or change the job's runs-on.", Link: "/runners"}})
		}
		if len(out) == 0 {
			out = append(out, doctor.Finding{Check: id, Subject: controllerSubject, Severity: doctor.OK, Title: "Every queued job has a machine that can run it"})
		}
		return out
	}))
	reg.Register(doctor.NewCheck("assign.waiting", doctor.GroupRepository, M1, false, func(ctx context.Context, env doctor.Env) []doctor.Finding {
		const id = "assign.waiting"
		ls, err := src.LongWaiting(ctx)
		if err != nil {
			return []doctor.Finding{skipFor(id, controllerSubject, "The queue", err)}
		}
		var out []doctor.Finding
		for _, l := range ls {
			out = append(out, doctor.Finding{Check: id, Subject: jobSubject(l), Severity: doctor.Warn,
				Title:  fmt.Sprintf("%s in %s has waited 12 hours or more", l.JobName, l.Repository),
				Detail: l.Reason + " GitHub cancels a job queued for 24 hours.",
				Fix:    doctor.Fix{Text: "Free room on a machine or cancel the run.", Link: "/jobs"}})
		}
		if len(out) == 0 {
			out = append(out, doctor.Finding{Check: id, Subject: controllerSubject, Severity: doctor.OK, Title: "No job has waited 12 hours"})
		}
		return out
	}))
}

// repoRunsOnAlways: CRSP_RUNS_ON_<OS> exists, and is not empty, for every OS of a
// routed repository (runners R5.116). A per-job variable falls back to it, and
// crsp deletes a per-job variable but never blanks one: an empty variable is
// refused by GitHub with HTTP 422.
func repoRunsOnAlways(ctx context.Context, env doctor.Env) []doctor.Finding {
	const id = "repo.runson_always"
	repos, out := poolRepos(ctx, env, id)
	for _, r := range repos {
		files, err := repoWorkflowFiles(ctx, env, r)
		if err != nil {
			out = append(out, skipFor(id, repoSubject(r), "The workflows of "+r.FullName, err))
			continue
		}
		raw := map[string][]byte{}
		for _, f := range files {
			raw[f.Path] = f.Content
		}
		if len(routing.Routed(raw)) == 0 {
			continue // not a routed repository
		}
		vars, err := repoVariables(ctx, env, r)
		if err != nil {
			out = append(out, skipFor(id, repoSubject(r), "The variables of "+r.FullName, err))
			continue
		}
		names := make([]string, 0, len(r.Expected))
		for n := range r.Expected {
			if strings.HasPrefix(n, "CRSP_RUNS_ON_") {
				names = append(names, n)
			}
		}
		sort.Strings(names)
		for _, name := range names {
			f := doctor.Finding{Check: id, Subject: varSubject(r, name)}
			switch got, set := vars[name]; {
			case !set:
				f.Severity, f.Title = doctor.Fail, name+" is missing on "+r.FullName
				f.Detail = "Its jobs read a per-job variable first and this one when that is not set, so a job with neither gets no labels and never starts."
				f.Fix = doctor.Fix{Text: "Set it from the Repositories screen.", Command: ghCommand(r, name, r.Expected[name]), Link: "/repositories"}
			case strings.TrimSpace(got) == "":
				f.Severity, f.Title = doctor.Fail, name+" is empty on "+r.FullName
				f.Detail = "GitHub refuses an empty variable, so something blanked it by another route."
				f.Fix = doctor.Fix{Text: "Set it again from the Repositories screen.", Command: ghCommand(r, name, r.Expected[name]), Link: "/repositories"}
			default:
				f.Severity, f.Title = doctor.OK, name+" is set on "+r.FullName
			}
			out = append(out, f)
		}
	}
	return out
}
