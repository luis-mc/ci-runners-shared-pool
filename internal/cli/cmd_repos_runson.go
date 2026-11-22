package cli

import (
	"flag"
	"fmt"

	"github.com/luis-mc/ci-runners-shared-pool/internal/api/client"
	"github.com/luis-mc/ci-runners-shared-pool/internal/cli/output"
)

// cmdRepos is `crsp repos` (github-integration R5.109): actions over the
// repositories as a set. `runs-on-pr` previews or opens the pull request that
// switches the runs-on lines to the crsp per-job labels. It opens a pull
// request only when the owner runs it; it never merges.
func cmdRepos() *command {
	return &command{
		name: "repos", summary: "actions over repositories",
		subs: []*command{
			{name: "runs-on-pr", summary: "open the pull request that switches runs-on to the crsp runners",
				args: "[--repo OWNER/NAME | --all] [--preview]",
				flags: func(c *call, fs *flag.FlagSet) {
					fs.String("repo", "", "one repository, OWNER/NAME")
					fs.Bool("all", false, "every repository whose pool is on")
					fs.Bool("preview", false, "show the files and the diff; open nothing")
				},
				run: func(c *call, _ []string) error { return c.runsOnPR() }},
		},
	}
}

func (c *call) runsOnPR() error {
	repoName := c.fs.Lookup("repo").Value.String()
	all := c.fs.Lookup("all").Value.String() == "true"
	preview := c.fs.Lookup("preview").Value.String() == "true"
	if all == (repoName != "") {
		return fail(ExitUsage, "Name one repository with --repo OWNER/NAME, or use --all.")
	}
	s, err := c.open()
	if err != nil {
		return err
	}
	var id int64
	if repoName != "" {
		r, err := c.findRepo(s, repoName)
		if err != nil {
			return err
		}
		id = r.Id
	}
	if preview {
		var p client.PreviewRunsOnPullRequestsParams
		if id != 0 {
			p.RepositoryId = &id
		}
		r, err := s.client.PreviewRunsOnPullRequestsWithResponse(c.env.Context, &p)
		if err != nil {
			return s.transport(err)
		}
		if err := apiError(r.StatusCode(), r.Body); err != nil {
			return err
		}
		if r.JSON200 == nil {
			return fail(ExitFailed, "The controller sent no preview.")
		}
		if c.g.json {
			return output.JSON(c.env.Stdout, *r.JSON200)
		}
		if len(r.JSON200.Items) == 0 {
			_, _ = fmt.Fprintln(c.env.Stderr, "No repository has the pool on.")
		}
		for _, it := range r.JSON200.Items {
			switch it.State {
			case client.RunsOnPullRequestPreviewStateChange:
				_, _ = fmt.Fprintf(c.env.Stdout, "%s: %d file(s) change\n%s", it.FullName, len(it.Files), it.Diff)
			case client.RunsOnPullRequestPreviewStateNothingToChange:
				_, _ = fmt.Fprintf(c.env.Stdout, "%s: nothing to change\n", it.FullName)
			case client.RunsOnPullRequestPreviewStateNotOwned:
				_, _ = fmt.Fprintf(c.env.Stdout, "%s: not opened here; the repository is not the owner's\n", it.FullName)
			default:
				_, _ = fmt.Fprintf(c.env.Stdout, "%s: failed: %s\n", it.FullName, deref(it.Message))
			}
		}
		return nil
	}
	body := client.OpenRunsOnPullRequestsRequest{}
	if id != 0 {
		body.RepositoryId = &id
	} else {
		body.All = &all
	}
	r, err := s.client.OpenRunsOnPullRequestsWithResponse(c.env.Context, body)
	if err != nil {
		return s.transport(err)
	}
	if err := apiError(r.StatusCode(), r.Body); err != nil {
		return err
	}
	if r.JSON200 == nil {
		return fail(ExitFailed, "The controller sent no result.")
	}
	if c.g.json {
		return output.JSON(c.env.Stdout, *r.JSON200)
	}
	var rows [][]string
	failed := 0
	for _, it := range r.JSON200.Items {
		what := string(it.State)
		if it.State == client.RunsOnPullRequestResultStateError {
			failed++
			what += ": " + deref(it.Message)
		}
		rows = append(rows, []string{it.FullName, what, deref(it.Url)})
	}
	output.Table(c.env.Stdout, output.MaxWidth, []string{"REPOSITORY", "RESULT", "PULL REQUEST"}, rows)
	if failed > 0 {
		return fail(ExitFailed, "%d repository(ies) failed.", failed)
	}
	return nil
}
