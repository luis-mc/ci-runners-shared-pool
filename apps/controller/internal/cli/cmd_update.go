package cli

import (
	"flag"
	"fmt"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api/client"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/cli/output"
)

// cmdUpdate is `crsp update` (operations R5.11 to R5.18). Update and restart
// drain first and never stop a running job; both name the running jobs and
// ask for a yes (tray R5.15, R5.16).
func cmdUpdate() *command {
	return &command{
		name: "update", summary: "update this controller to the newest stable release, with drain",
		subs: []*command{
			{name: "status", summary: "show the running version, the newest release and the state",
				run: func(c *call, _ []string) error {
					s, err := c.open()
					if err != nil {
						return err
					}
					r, err := s.client.GetUpdateStatusWithResponse(c.env.Context)
					if err != nil {
						return s.transport(err)
					}
					if err := apiError(r.StatusCode(), r.Body); err != nil {
						return err
					}
					return c.printUpdate(r.JSON200)
				}},
			{name: "check", summary: "look for a newer stable release now",
				run: func(c *call, _ []string) error {
					s, err := c.open()
					if err != nil {
						return err
					}
					r, err := s.client.CheckForUpdateWithResponse(c.env.Context)
					if err != nil {
						return s.transport(err)
					}
					if err := apiError(r.StatusCode(), r.Body); err != nil {
						return err
					}
					return c.printUpdate(r.JSON200)
				}},
			{name: "apply", summary: "drain, then install the newest stable release", args: "[--yes]",
				flags: func(c *call, fs *flag.FlagSet) { yesFlag(fs) },
				run: func(c *call, _ []string) error {
					return c.drainThen("update", "This updates the controller and restarts the service.",
						func(s *session, confirmed bool) (*client.UpdateStatus, int, []byte, error) {
							r, err := s.client.ApplyUpdateWithResponse(c.env.Context, &client.ApplyUpdateParams{Confirmed: &confirmed})
							if err != nil {
								return nil, 0, nil, err
							}
							return r.JSON202, r.StatusCode(), r.Body, nil
						})
				}},
			{name: "auto", summary: "turn automatic updates on or off", args: "on|off",
				run: func(c *call, args []string) error {
					if len(args) != 1 || (args[0] != "on" && args[0] != "off") {
						return fail(ExitUsage, "Usage: crsp update auto on|off")
					}
					s, err := c.open()
					if err != nil {
						return err
					}
					on := args[0] == "on"
					r, err := s.client.UpdateSettingsWithResponse(c.env.Context, client.Settings{AutoUpdate: &on})
					if err != nil {
						return s.transport(err)
					}
					if err := apiError(r.StatusCode(), r.Body); err != nil {
						return err
					}
					if c.g.json {
						return output.JSON(c.env.Stdout, r.JSON200)
					}
					state := "off. A newer release waits for a yes (crsp update apply)."
					if on {
						state = "on. A newer stable release installs by itself after a drain."
					}
					_, _ = fmt.Fprintln(c.env.Stderr, "Automatic updates are "+state)
					return nil
				}},
		},
	}
}

// cmdRestart is `crsp restart`: drain, then restart the service.
func cmdRestart() *command {
	return &command{
		name: "restart", summary: "drain, then restart the service", args: "[--yes]",
		flags: func(c *call, fs *flag.FlagSet) { yesFlag(fs) },
		run: func(c *call, _ []string) error {
			return c.drainThen("restart", "This restarts the service.",
				func(s *session, confirmed bool) (*client.UpdateStatus, int, []byte, error) {
					r, err := s.client.RestartServiceWithResponse(c.env.Context, &client.RestartServiceParams{Confirmed: &confirmed})
					if err != nil {
						return nil, 0, nil, err
					}
					return r.JSON202, r.StatusCode(), r.Body, nil
				})
		},
	}
}

// drainThen lists the running jobs it waits for, asks for the yes, then calls
// the confirmed request. Nothing changes without the yes.
func (c *call) drainThen(what, title string, send func(s *session, confirmed bool) (*client.UpdateStatus, int, []byte, error)) error {
	s, err := c.open()
	if err != nil {
		return err
	}
	g, err := s.client.GetUpdateStatusWithResponse(c.env.Context)
	if err != nil {
		return s.transport(err)
	}
	if err := apiError(g.StatusCode(), g.Body); err != nil {
		return err
	}
	var items []string
	if g.JSON200 != nil {
		if what == "update" {
			if !g.JSON200.UpdateAvailable {
				_, _ = fmt.Fprintf(c.env.Stderr, "Version %s is the newest stable release. Nothing to update.\n", g.JSON200.CurrentVersion)
				return nil
			}
			title = fmt.Sprintf("This updates the controller from %s to %s and restarts the service.", g.JSON200.CurrentVersion, deref(g.JSON200.LatestVersion))
		}
		for _, j := range g.JSON200.RunningJobs {
			items = append(items, "waits for running job: "+j.Repository+" "+deref(j.Workflow)+", run "+fmt.Sprint(j.RunId)+", job "+j.Job)
		}
	}
	if len(items) == 0 {
		items = []string{"no job runs now; this machine takes no new jobs while it waits"}
	}
	if err := c.confirm(title+" No running job is stopped.", items, c.yesFlag()); err != nil {
		return err
	}
	st, code, body, err := send(s, true)
	if err != nil {
		return s.transport(err)
	}
	if err := apiError(code, body); err != nil {
		return err
	}
	if c.g.json {
		return output.JSON(c.env.Stdout, st)
	}
	_, _ = fmt.Fprintln(c.env.Stderr, "Draining. The service "+what+"s when no job runs. Follow it with: crsp update status")
	return nil
}

func (c *call) printUpdate(st *client.UpdateStatus) error {
	if st == nil {
		return fail(ExitFailed, "The controller sent no update status.")
	}
	if c.g.json {
		return output.JSON(c.env.Stdout, st)
	}
	auto := "off"
	if st.AutoUpdate {
		auto = "on"
	}
	rows := [][]string{
		{"Version", st.CurrentVersion},
		{"Newest stable", orDash(deref(st.LatestVersion))},
		{"Update available", fmt.Sprint(st.UpdateAvailable)},
		{"State", string(st.State)},
		{"Automatic updates", auto},
		{"Running jobs", fmt.Sprint(len(st.RunningJobs))},
	}
	if st.Error != nil {
		rows = append(rows, []string{"Problem", *st.Error})
	}
	output.Table(c.env.Stdout, output.MaxWidth, []string{"", ""}, rows)
	return nil
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
