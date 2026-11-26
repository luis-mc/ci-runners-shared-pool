package cli

import "flag"

// cmdHistory lists the finished jobs, newest first (observability R5.11). It reads the same route and gives the
// same rows as the History page of the console: `--json` prints the page the API sent, so a script sees what the
// console shows. It filters by repository, machine, backend and result, and `--since` and `--limit` choose the
// range.
func cmdHistory() *command {
	return &command{
		name: "history", summary: "finished jobs, newest first",
		args:  "[--repo OWNER/NAME] [--machine M] [--backend B] [--result R] [--since 24h] [--limit N] [--json]",
		flags: func(_ *call, fs *flag.FlagSet) { jobFilterFlags(fs) },
		run:   func(c *call, _ []string) error { return c.listJobs("done") },
	}
}
