package checks

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/doctor"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/github"
)

// demandQuiet: a pool repository has jobs queued on GitHub for the pool, but
// the demand source has seen no demand for it for the allowed time (10 minutes
// by default). The controller's watchdog restarts the demand loop once when it
// first sees this; the check reads what the watchdog found. New check.
func demandQuiet(ctx context.Context, env doctor.Env) []doctor.Finding {
	repos, out := poolRepos(ctx, env, "demand.quiet")
	if len(out) > 0 || len(repos) == 0 {
		return out
	}
	if env.Demand == nil {
		return []doctor.Finding{skip("demand.quiet", controllerSubject, "The demand source is not running", "Nothing starts runners for queued jobs, so it was not checked.")}
	}
	quiet, err := env.Demand.QuietDemand(ctx)
	if err != nil {
		return []doctor.Finding{skipFor("demand.quiet", controllerSubject, "The demand source", err)}
	}
	for _, r := range repos {
		f := doctor.Finding{Check: "demand.quiet", Subject: repoSubject(r)}
		var q *doctor.QuietDemand
		for i := range quiet {
			if strings.EqualFold(quiet[i].Repository, r.FullName) {
				q = &quiet[i]
				break
			}
		}
		if q == nil {
			f.Severity, f.Title = doctor.OK, "The demand source sees the queued jobs of "+r.FullName
			out = append(out, f)
			continue
		}
		f.Severity = doctor.Warn
		f.Title = fmt.Sprintf("%s has %s queued for the pool and the demand source has seen none", r.FullName, plural(q.Queued, "job"))
		if q.Restarted {
			f.Detail = fmt.Sprintf("Quiet since %s. The controller restarted the demand loop once, at %s, and it is still quiet.", clock(q.Since), clock(q.RestartedAt))
			f.Fix = doctor.Fix{Text: "Restart the controller service. If it stays quiet, check that GitHub is reachable and the App can read Actions (github.app.permissions), then read the controller log for \"poll queued jobs\".", Link: appLink}
		} else {
			f.Detail = fmt.Sprintf("Quiet since %s.", clock(q.Since))
			f.Fix = doctor.Fix{Text: "The controller restarts the demand loop once by itself. Run the doctor again in a minute.", Link: appLink}
		}
		out = append(out, f)
	}
	return out
}

func clock(t time.Time) string { return t.UTC().Format("2006-01-02 15:04 MST") }

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// DeliveryLister is the optional part of the GitHub adapter that lists the
// webhook deliveries of the App.
type DeliveryLister interface {
	ListHookDeliveries(ctx context.Context) ([]github.HookDelivery, error)
}

// OpenJobsLister is the optional part of the GitHub adapter that lists the
// open jobs of a repository.
type OpenJobsLister interface {
	ListOpenJobs(ctx context.Context, installationID int64, owner, repo string) ([]github.Job, error)
}

// webhookWindow is how far back deliveries count.
const webhookWindow = 24 * time.Hour

// githubWebhook: recent webhook deliveries failed, or none arrived in 24 hours
// while jobs were open. Deliveries come from GitHub's list for the App. When
// the polling fallback is in use this says so (doctor R5.16).
func githubWebhook(ctx context.Context, env doctor.Env) []doctor.Finding {
	const id = "github.webhook"
	if env.GitHub == nil {
		return []doctor.Finding{skip(id, controllerSubject, "GitHub is not connected", "The GitHub App is not set up yet.")}
	}
	dl, ok := env.GitHub.(DeliveryLister)
	if !ok {
		return []doctor.Finding{skip(id, controllerSubject, "Webhook deliveries are not listed", "This GitHub adapter cannot list them, so they were not checked.")}
	}
	all, err := dl.ListHookDeliveries(ctx)
	if err != nil {
		return []doctor.Finding{skipFor(id, controllerSubject, "The webhook deliveries", err)}
	}
	var recent []github.HookDelivery
	for _, d := range all {
		if env.Now.Sub(d.DeliveredAt) <= webhookWindow {
			recent = append(recent, d)
		}
	}
	f := doctor.Finding{Check: id, Subject: controllerSubject}
	fix := doctor.Fix{Text: "Check that GitHub can reach the controller (the webhook address in the App settings). The polling fallback is in use meanwhile.", Link: appLink}
	if len(recent) == 0 {
		jobs, err := openJobCount(ctx, env, id)
		if err != nil {
			return []doctor.Finding{skipFor(id, controllerSubject, "The open jobs", err)}
		}
		if jobs == 0 {
			f.Severity, f.Title = doctor.OK, "No webhook delivery in 24 hours, and no job is open"
			return []doctor.Finding{f}
		}
		f.Severity, f.Fix = doctor.Warn, fix
		f.Title = "No webhook delivery arrived in 24 hours while jobs are open"
		f.Detail = fmt.Sprintf("%s open now. The controller finds jobs by polling instead.", plural(jobs, "job"))
		return []doctor.Finding{f}
	}
	failed := 0
	for _, d := range recent {
		if !d.OK() {
			failed++
		}
	}
	newest := recent[0]
	for _, d := range recent {
		if d.DeliveredAt.After(newest.DeliveredAt) {
			newest = d
		}
	}
	if !newest.OK() || failed*2 >= len(recent) {
		f.Severity, f.Fix = doctor.Warn, fix
		f.Title = fmt.Sprintf("%d of %d recent webhook deliveries failed", failed, len(recent))
		f.Detail = "The newest was " + newest.Event + " at " + clock(newest.DeliveredAt) + ": " + newest.Status + "."
		return []doctor.Finding{f}
	}
	f.Severity = doctor.OK
	f.Title = fmt.Sprintf("Webhook deliveries arrive (%d in 24 hours, %d failed)", len(recent), failed)
	return []doctor.Finding{f}
}

// openJobCount counts the open jobs of the pool repositories in scope.
func openJobCount(ctx context.Context, env doctor.Env, check string) (int, error) {
	lister, ok := env.GitHub.(OpenJobsLister)
	if !ok {
		return 0, doctor.ErrUnreachable
	}
	repos, out := poolRepos(ctx, env, check)
	if len(out) > 0 {
		return 0, doctor.ErrUnreachable
	}
	n := 0
	for _, r := range repos {
		jobs, err := lister.ListOpenJobs(github.Background(ctx), r.InstallationID, r.Owner, r.Name)
		if err != nil {
			return 0, err
		}
		n += len(jobs)
	}
	return n, nil
}
