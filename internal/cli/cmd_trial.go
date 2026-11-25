package cli

import (
	"flag"
	"fmt"
	"strings"

	"github.com/luis-mc/ci-runners-shared-pool/internal/api/client"
	"github.com/luis-mc/ci-runners-shared-pool/internal/cli/output"
)

// cmdTrial is `crsp trial OWNER/NAME [observe|shadow|canary|off]`
// (github-integration R5.66): without a mode it shows the trial and its report.
func cmdTrial() *command {
	return &command{
		name: "trial", summary: "try a repository before the pool takes it: observe, shadow or canary",
		args: "OWNER/NAME [observe|shadow|canary|off] [--percent N] [--jobs A,B] [--shadow-workflow FILE] [--shadow-jobs A,B]",
		flags: func(c *call, fs *flag.FlagSet) {
			fs.Int("percent", 0, "canary: the share of jobs that use crsp, chosen by job name")
			fs.String("jobs", "", "canary: jobs that always use crsp, names comma separated")
			fs.String("shadow-workflow", "", "shadow: the workflow file to dispatch (default crsp-shadow.yml)")
			fs.String("shadow-jobs", "", "shadow: the jobs to copy, names comma separated (default every job crsp would take)")
		},
		run: func(c *call, args []string) error { return c.trial(args) },
	}
}

func (c *call) trial(args []string) error {
	if len(args) < 1 || len(args) > 2 {
		return fail(ExitUsage, "Usage: crsp trial OWNER/NAME [observe|shadow|canary|off]")
	}
	s, err := c.open()
	if err != nil {
		return err
	}
	repo, err := c.findRepo(s, args[0])
	if err != nil {
		return err
	}
	var t *client.Trial
	if len(args) == 1 {
		r, err := s.client.GetRepositoryTrialWithResponse(c.env.Context, repo.Id)
		if err != nil {
			return s.transport(err)
		}
		if err := apiError(r.StatusCode(), r.Body); err != nil {
			return err
		}
		t = r.JSON200
	} else {
		mode := strings.ToLower(args[1])
		switch mode {
		case "observe", "shadow", "canary", "live", "off":
		default:
			return fail(ExitUsage, "The mode is observe, shadow, canary, live or off.")
		}
		yes := true // the typed command is the yes
		body := client.TrialSet{Mode: client.TrialSetMode(mode), Confirmed: &yes}
		if p := c.fs.Lookup("percent").Value.String(); p != "0" {
			var n int
			if _, err := fmt.Sscanf(p, "%d", &n); err != nil {
				return fail(ExitUsage, "--percent takes a number from 1 to 100.")
			}
			body.Percent = &n
		}
		if flagSet(c.fs, "jobs") {
			j := splitList(c.fs.Lookup("jobs").Value.String())
			body.Jobs = &j
		}
		if flagSet(c.fs, "shadow-workflow") {
			w := c.fs.Lookup("shadow-workflow").Value.String()
			body.ShadowWorkflow = &w
		}
		if flagSet(c.fs, "shadow-jobs") {
			j := splitList(c.fs.Lookup("shadow-jobs").Value.String())
			body.ShadowJobs = &j
		}
		r, err := s.client.SetRepositoryTrialWithResponse(c.env.Context, repo.Id, body)
		if err != nil {
			return s.transport(err)
		}
		if err := apiError(r.StatusCode(), r.Body); err != nil {
			return err
		}
		t = r.JSON200
	}
	if t == nil {
		return fail(ExitFailed, "The controller sent no trial.")
	}
	if c.g.json {
		return output.JSON(c.env.Stdout, t)
	}
	out := c.env.Stdout
	_, _ = fmt.Fprintf(out, "%s: trial %s", t.Repository, t.Mode)
	if t.Percent != nil {
		_, _ = fmt.Fprintf(out, " %d%%", *t.Percent)
	}
	_, _ = fmt.Fprintln(out)
	if len(t.Variables) > 0 {
		_, _ = fmt.Fprintf(out, "Canary variables: %s\n", strings.Join(t.Variables, ", "))
	}
	if len(t.Jobs) == 0 {
		_, _ = fmt.Fprintln(out, "No jobs recorded yet.")
		return nil
	}
	var rows [][]string
	for _, j := range t.Jobs {
		agree := "-"
		if j.Agree != nil {
			agree = map[bool]string{true: "yes", false: "no"}[*j.Agree]
		}
		rows = append(rows, []string{j.Job, fmt.Sprint(j.Hosted.Runs), fmt.Sprintf("%ds/%ds", j.Hosted.AvgWaitSec, j.Hosted.AvgRunSec),
			fmt.Sprint(j.Crsp.Runs), fmt.Sprintf("%ds/%ds", j.Crsp.AvgWaitSec, j.Crsp.AvgRunSec), fmt.Sprint(j.WouldTake), agree})
	}
	output.Table(out, output.MaxWidth, []string{"JOB", "HOSTED", "WAIT/RUN", "CRSP", "WAIT/RUN", "WOULD TAKE", "SAME RESULT"}, rows)
	return nil
}
