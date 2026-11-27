package checks

import (
	"context"
	"strings"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/doctor"
)

// hosted says whether the repository's jobs for an OS run on GitHub-hosted
// runners now (github-integration R5.43).
func hosted(r doctor.Repository, os string) bool {
	for _, h := range r.HostedOSes {
		if strings.EqualFold(h, os) {
			return true
		}
	}
	return false
}

func osList(oses []string) string {
	t := make([]string, 0, len(oses))
	for _, o := range oses {
		t = append(t, osTitle(o))
	}
	return strings.Join(t, ", ")
}

// repoWaiting names each pool repository whose jobs wait because no crsp
// machine is up for an OS it uses and that has the hosted fallback off. It is
// info, never fail: waiting costs nothing (R5.46). New check.
func repoWaiting(ctx context.Context, env doctor.Env) []doctor.Finding {
	repos, out := poolRepos(ctx, env, "repo.waiting")
	for _, r := range repos {
		if r.Where != "waiting" || r.FallbackOn {
			continue
		}
		out = append(out, doctor.Finding{Check: "repo.waiting", Subject: repoSubject(r), Severity: doctor.Info,
			Title:  "Jobs of " + r.FullName + " wait: no crsp machine is up for " + osList(r.WaitingOSes),
			Detail: "Every machine for " + osList(r.WaitingOSes) + " is offline, asleep or paused. GitHub keeps the jobs queued for up to 24 hours. This costs nothing. The repository could instead run on GitHub-hosted runners while no machine is up, which GitHub may bill for a private repository.",
			Fix:    doctor.Fix{Text: "Start or resume a machine, or turn on the hosted fallback for the repository.", Link: "/repositories"}})
	}
	return out
}

// repoFallback warns while a repository's jobs run on GitHub-hosted runners
// because of the fallback, since GitHub may bill those minutes (R5.46). A
// repository that opted in and runs on crsp machines is ok. New check.
func repoFallback(ctx context.Context, env doctor.Env) []doctor.Finding {
	repos, out := poolRepos(ctx, env, "repo.fallback")
	for _, r := range repos {
		if !r.FallbackOn {
			continue
		}
		f := doctor.Finding{Check: "repo.fallback", Subject: repoSubject(r)}
		if r.Where == "hosted" {
			f.Severity = doctor.Warn
			f.Title = "Jobs of " + r.FullName + " run on GitHub-hosted runners (" + osList(r.HostedOSes) + ")"
			f.Detail = "No crsp machine for " + osList(r.HostedOSes) + " is up, so the hosted fallback is on. GitHub may bill these minutes for a private repository. crsp switches back as soon as a machine is up."
			f.Fix = doctor.Fix{Text: "Start or resume a machine for " + osList(r.HostedOSes) + ".", Link: "/machines"}
		} else {
			f.Severity = doctor.OK
			f.Title = "The hosted fallback is on for " + r.FullName + " and not in use"
		}
		out = append(out, f)
	}
	return out
}
