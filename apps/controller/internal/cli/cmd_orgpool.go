package cli

import (
	"fmt"
	"strings"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api/client"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/cli/output"
)

// cmdOrgPool is `crsp org-pool` (github-integration R5.21): list the pool mode
// of each installation, and turn it on or off for an organization.
func cmdOrgPool() *command {
	set := func(on bool) func(c *call, args []string) error {
		return func(c *call, args []string) error { return c.setOrgPool(args, on) }
	}
	return &command{
		name: "org-pool", summary: "run the pool at organization level, in one runner group",
		subs: []*command{
			{name: "list", summary: "list the installations and whether the pool runs at organization level",
				run: func(c *call, _ []string) error {
					items, err := c.orgPools()
					if err != nil {
						return err
					}
					if c.g.json {
						return output.JSON(c.env.Stdout, client.OrgPoolList{Items: nonNil(items)})
					}
					if len(items) == 0 {
						_, _ = fmt.Fprintln(c.env.Stderr, "No installations yet. Install the GitHub App on an account first.")
						return nil
					}
					var rows [][]string
					for _, o := range items {
						state := "repository runners"
						switch {
						case o.Enabled:
							state = "organization runners"
						case !o.IsOrganization:
							state = "repository runners (personal account)"
						case !o.Eligible && o.Reason != nil:
							state = "not available: " + *o.Reason
						}
						rows = append(rows, []string{o.Organization, state, o.GroupName, strings.Join(o.Repositories, ",")})
					}
					output.Table(c.env.Stdout, output.MaxWidth, []string{"ACCOUNT", "RUNNERS", "GROUP", "REPOSITORIES"}, rows)
					return nil
				}},
			{name: "on", summary: "use organization runners in a runner group for the selected repositories", args: "ORGANIZATION", run: set(true)},
			{name: "off", summary: "go back to repository runners; running jobs finish", args: "ORGANIZATION", run: set(false)},
		},
	}
}

func (c *call) orgPools() ([]client.OrgPool, error) {
	s, err := c.open()
	if err != nil {
		return nil, err
	}
	r, err := s.client.ListOrgPoolsWithResponse(c.env.Context)
	if err != nil {
		return nil, s.transport(err)
	}
	if err := apiError(r.StatusCode(), r.Body); err != nil {
		return nil, err
	}
	if r.JSON200 == nil {
		return nil, fail(ExitFailed, "The controller sent no list.")
	}
	return r.JSON200.Items, nil
}

func (c *call) setOrgPool(args []string, on bool) error {
	if len(args) != 1 {
		return fail(ExitUsage, "Name the organization: crsp org-pool %s ORGANIZATION.", map[bool]string{true: "on", false: "off"}[on])
	}
	items, err := c.orgPools()
	if err != nil {
		return err
	}
	var found *client.OrgPool
	for i := range items {
		if strings.EqualFold(items[i].Organization, args[0]) {
			found = &items[i]
		}
	}
	if found == nil {
		return fail(ExitNotFound, "No installation on %s. Run crsp org-pool list.", args[0])
	}
	if !found.IsOrganization {
		return fail(ExitUsage, "%s is a personal account. Its runners stay at repository level.", found.Organization)
	}
	s, err := c.open()
	if err != nil {
		return err
	}
	r, err := s.client.SetOrgPoolWithResponse(c.env.Context, found.InstallationId, client.OrgPoolSet{Enabled: on})
	if err != nil {
		return s.transport(err)
	}
	if err := apiError(r.StatusCode(), r.Body); err != nil {
		return err
	}
	if r.JSON200 == nil {
		return fail(ExitFailed, "The controller sent no answer.")
	}
	o := r.JSON200
	if c.g.json {
		return output.JSON(c.env.Stdout, o)
	}
	if o.Enabled {
		_, _ = fmt.Fprintf(c.env.Stdout, "%s: organization runners in the group %s, for %d repositories.\n", o.Organization, o.GroupName, len(o.Repositories))
	} else {
		_, _ = fmt.Fprintf(c.env.Stdout, "%s: repository runners again. Runners with a job finish it.\n", o.Organization)
	}
	return nil
}
