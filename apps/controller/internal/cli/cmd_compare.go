package cli

import (
	"encoding/json"
	"flag"
	"strings"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api/client"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/cli/output"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/compare"
)

// cmdCompare prints the same job measured on crsp and on a GitHub-hosted
// runner (observability R5.34). It reads; it starts nothing.
func cmdCompare() *command {
	return &command{
		name: "compare", summary: "the same job on crsp and on GitHub-hosted runners", args: "OWNER/REPO [--days N]",
		flags: func(c *call, fs *flag.FlagSet) { fs.Int("days", compare.DefaultDays, "how many days back, at most 90") },
		run: func(c *call, args []string) error {
			if len(args) != 1 || strings.Count(args[0], "/") != 1 {
				return fail(ExitUsage, "Name one repository as owner/repo, for example: crsp compare acme/api")
			}
			days := mustInt(c.fs.Lookup("days").Value.String())
			if days < 1 || days > compare.MaxDays {
				return fail(ExitUsage, "--days must be between 1 and 90.")
			}
			s, err := c.open()
			if err != nil {
				return err
			}
			r, err := s.client.GetSpeedComparisonWithResponse(c.env.Context, &client.GetSpeedComparisonParams{Repository: &args[0], Days: &days})
			if err != nil {
				return s.transport(err)
			}
			if err := apiError(r.StatusCode(), r.Body); err != nil {
				return err
			}
			if r.JSON200 == nil {
				return fail(ExitFailed, "The controller sent no comparison.")
			}
			if c.g.json {
				return output.JSON(c.env.Stdout, r.JSON200)
			}
			raw, err := json.Marshal(r.JSON200)
			if err != nil {
				return err
			}
			var rep compare.Report
			if err := json.Unmarshal(raw, &rep); err != nil {
				return err
			}
			return compare.WriteTable(c.env.Stdout, rep)
		},
	}
}
