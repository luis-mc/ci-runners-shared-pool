package cli

import (
	"flag"
	"fmt"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api/client"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/cli/output"
)

// cmdSettings is `crsp settings`: the controller settings the CLI reaches.
func cmdSettings() *command {
	return &command{
		name: "settings", summary: "show or change controller settings",
		subs: []*command{
			{name: "public-repositories", summary: "show, turn on or turn off the serving of public repositories (off by default)",
				args: "[--on|--off] [--yes]",
				flags: func(c *call, fs *flag.FlagSet) {
					fs.Bool("on", false, "serve public repositories (owner)")
					fs.Bool("off", false, "serve private repositories only; public ones on the pool are stopped (owner)")
					yesFlag(fs)
				},
				run: func(c *call, _ []string) error { return c.publicRepositories() }},
		},
	}
}

func (c *call) publicRepositories() error {
	on, off := flagSet(c.fs, "on") && c.fs.Lookup("on").Value.String() == "true", flagSet(c.fs, "off") && c.fs.Lookup("off").Value.String() == "true"
	if on && off {
		return fail(ExitUsage, "Choose one: --on or --off.")
	}
	s, err := c.open()
	if err != nil {
		return err
	}
	cur, err := s.client.GetPublicRepositoriesWithResponse(c.env.Context)
	if err != nil {
		return s.transport(err)
	}
	if err := apiError(cur.StatusCode(), cur.Body); err != nil {
		return err
	}
	if cur.JSON200 == nil {
		return fail(ExitFailed, "The controller sent no answer.")
	}
	if !on && !off {
		if c.g.json {
			return output.JSON(c.env.Stdout, cur.JSON200)
		}
		_, _ = fmt.Fprintf(c.env.Stdout, "Public repositories: %s. %d public %s on the pool.\n", onOff(cur.JSON200.Enabled),
			cur.JSON200.PublicOnPool, plural(cur.JSON200.PublicOnPool, "repository is", "repositories are"))
		return nil
	}
	if off && cur.JSON200.PublicOnPool > 0 {
		if err := c.confirm("Turning public repositories off stops the pool for every public repository that has it on. Its settings are kept; running jobs finish.",
			[]string{fmt.Sprintf("%d public %s lose the pool", cur.JSON200.PublicOnPool, plural(cur.JSON200.PublicOnPool, "repository", "repositories"))}, c.yesFlag()); err != nil {
			return err
		}
	}
	r, err := s.client.SetPublicRepositoriesWithResponse(c.env.Context, client.PublicRepositoriesSet{Enabled: on})
	if err != nil {
		return s.transport(err)
	}
	if err := apiError(r.StatusCode(), r.Body); err != nil {
		return err
	}
	if r.JSON200 == nil {
		return fail(ExitFailed, "The controller sent no answer.")
	}
	if c.g.json {
		return output.JSON(c.env.Stdout, r.JSON200)
	}
	if on {
		_, _ = fmt.Fprintln(c.env.Stdout, "Public repositories: on. Turn the pool on for a public repository as for any other.")
		return nil
	}
	_, _ = fmt.Fprintf(c.env.Stdout, "Public repositories: off. %d %s stopped.\n", r.JSON200.Stopped, plural(r.JSON200.Stopped, "repository", "repositories"))
	return nil
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
