package cli

import (
	"flag"
	"fmt"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api/client"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/cli/output"
)

// cmdPool is `crsp pool` (runners R5.141 to R5.145): show the pool name and
// rename it. It calls the same service as the console's Rename the pool wizard.
func cmdPool() *command {
	return &command{
		name: "pool", summary: "the name of the pool: the runner label every crsp runner carries",
		subs: []*command{
			{name: "name", summary: "show the pool name and where it comes from",
				run: func(c *call, _ []string) error {
					s, err := c.open()
					if err != nil {
						return err
					}
					r, err := s.client.GetPoolNameWithResponse(c.env.Context)
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
					_, _ = fmt.Fprintf(c.env.Stdout, "%s (%s)\n", r.JSON200.Name, r.JSON200.Source)
					if p := r.JSON200.Pending; p != nil {
						_, _ = fmt.Fprintf(c.env.Stdout, "A rename to %s is %s. Run crsp pool rename %s to go on.\n", p.New, p.State, p.New)
					}
					return nil
				}},
			{name: "rename", summary: "rename the pool: every runner and repository gets the new label", args: "NEW [--yes]",
				flags: func(c *call, fs *flag.FlagSet) { yesFlag(fs) }, run: func(c *call, args []string) error { return c.poolRename(args) }},
		},
	}
}

func (c *call) poolRename(args []string) error {
	if len(args) != 1 {
		return fail(ExitUsage, "Name the pool: crsp pool rename NEW. A name looks like crsp-home.")
	}
	s, err := c.open()
	if err != nil {
		return err
	}
	pr, err := s.client.PlanPoolRenameWithResponse(c.env.Context, client.PoolNameInput{Name: args[0]})
	if err != nil {
		return s.transport(err)
	}
	if err := apiError(pr.StatusCode(), pr.Body); err != nil {
		return err
	}
	if pr.JSON201 == nil {
		return fail(ExitFailed, "The controller sent no plan.")
	}
	plan := pr.JSON201
	var items []string
	for _, st := range plan.Steps {
		if st.State != client.PoolRenameStepStateDone {
			items = append(items, st.Text)
		}
	}
	title := fmt.Sprintf("Rename the pool from %s to %s: %d runners, %d repositories. A job queued with %s keeps its runner until the last step.",
		plan.Old, plan.New, plan.Counts.Runners, plan.Counts.Repositories, plan.Old)
	if err := c.confirm(title, items, c.yesFlag()); err != nil {
		return err
	}
	cur, err := c.poolApply(s, plan.Id)
	if err != nil {
		return err
	}
	for i := 0; cur.State == client.PoolRenameStateApplying && i < 3600; i++ {
		if err := c.sleep(time.Second); err != nil {
			return err
		}
		g, gerr := s.client.GetPoolRenameWithResponse(c.env.Context, plan.Id)
		if gerr != nil {
			return s.transport(gerr)
		}
		if err := apiError(g.StatusCode(), g.Body); err != nil {
			return err
		}
		cur = g.JSON200
	}
	if cur.State != client.PoolRenameStateDone {
		msg := "The rename did not finish."
		for _, st := range cur.Steps {
			if st.Error != nil && *st.Error != "" {
				msg += " " + st.Text + ": " + *st.Error + "."
			}
		}
		return fail(ExitFailed, "%s Run crsp pool rename %s again to go on.", msg, plan.New)
	}
	if c.g.json {
		return output.JSON(c.env.Stdout, cur)
	}
	_, _ = fmt.Fprintf(c.env.Stdout, "The pool is called %s now.\n", cur.New)
	return nil
}

func (c *call) poolApply(s *session, id string) (*client.PoolRename, error) {
	a, err := s.client.ApplyPoolRenameWithResponse(c.env.Context, id)
	if err != nil {
		return nil, s.transport(err)
	}
	if err := apiError(a.StatusCode(), a.Body); err != nil {
		return nil, err
	}
	if a.JSON202 == nil {
		return nil, fail(ExitFailed, "The controller sent no answer.")
	}
	return a.JSON202, nil
}
