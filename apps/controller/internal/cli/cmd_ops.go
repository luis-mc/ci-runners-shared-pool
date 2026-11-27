package cli

import (
	"flag"
	"fmt"
	"strings"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api/client"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/cli/output"
)

func cmdDoctor() *command {
	return &command{
		name: "doctor", summary: "run every check and print fixes", args: "[--machine M]",
		flags: func(c *call, fs *flag.FlagSet) { fs.String("machine", "", "only this machine") },
		run: func(c *call, _ []string) error {
			s, err := c.open()
			if err != nil {
				return err
			}
			var scope client.DoctorScope
			if v := c.fs.Lookup("machine").Value.String(); v != "" {
				m, err := c.findMachine(s, v)
				if err != nil {
					return err
				}
				scope.Machines = &[]string{m.Id}
			}
			run, err := c.doctorRun(s, scope)
			if err != nil {
				return err
			}
			problem := false
			for _, f := range run.Findings {
				if f.Severity == client.SeverityWarn || f.Severity == client.SeverityFail {
					problem = true
				}
			}
			if c.g.json {
				if err := output.JSON(c.env.Stdout, run); err != nil {
					return err
				}
			} else {
				var rows [][]string
				for _, f := range run.Findings {
					rows = append(rows, []string{string(f.Severity), f.Subject.Name, f.Title})
				}
				if len(rows) > 0 {
					output.Table(c.env.Stdout, output.MaxWidth, []string{"RESULT", "SUBJECT", "FINDING"}, rows)
				}
				for _, f := range run.Findings {
					if f.Severity == client.SeverityWarn || f.Severity == client.SeverityFail {
						fix := f.Fix.Text
						if f.Fix.Command != nil {
							fix += " Run: " + *f.Fix.Command
						}
						_, _ = fmt.Fprintf(c.env.Stderr, "Fix for %s: %s\n", f.Title, fix)
					}
				}
				if !problem {
					_, _ = fmt.Fprintln(c.env.Stderr, "All checks passed.")
				}
			}
			if problem {
				return &exitError{Code: ExitFindings, Message: "The doctor found a problem. The fixes are above."}
			}
			return nil
		},
	}
}

// doctorRun starts a doctor run and waits for it to leave the running state.
func (c *call) doctorRun(s *session, scope client.DoctorScope) (*client.DoctorRun, error) {
	r, err := s.client.RunDoctorWithResponse(c.env.Context, scope)
	if err != nil {
		return nil, s.transport(err)
	}
	if err := apiError(r.StatusCode(), r.Body); err != nil {
		return nil, err
	}
	run := r.JSON202
	if run == nil {
		return nil, fail(ExitFailed, "The controller sent no doctor run.")
	}
	for i := 0; run.State == client.DoctorRunStateRunning && i < 600; i++ {
		if err := c.sleep(time.Second); err != nil {
			return nil, err
		}
		g, err := s.client.GetDoctorRunWithResponse(c.env.Context, run.Id)
		if err != nil {
			return nil, s.transport(err)
		}
		if err := apiError(g.StatusCode(), g.Body); err != nil {
			return nil, err
		}
		if g.JSON200 != nil {
			run = g.JSON200
		}
	}
	return run, nil
}

func cmdBackup() *command {
	return &command{
		name: "backup", summary: "back up the controller's configuration", args: "",
		run: func(c *call, _ []string) error {
			s, err := c.open()
			if err != nil {
				return err
			}
			r, err := s.client.RunBackupWithResponse(c.env.Context)
			if err != nil {
				return s.transport(err)
			}
			if err := apiError(r.StatusCode(), r.Body); err != nil {
				return err
			}
			if r.JSON202 == nil {
				return fail(ExitFailed, "The controller sent no backup.")
			}
			if c.g.json {
				return output.JSON(c.env.Stdout, r.JSON202)
			}
			_, _ = fmt.Fprintf(c.env.Stderr, "Backup %s started on the controller (file %s).\n", r.JSON202.Id, r.JSON202.File)
			return nil
		},
	}
}

func cmdRestore() *command {
	return &command{
		name: "restore", summary: "restore the controller from a backup (owner only)", args: "BACKUP [--yes]",
		flags: func(c *call, fs *flag.FlagSet) { yesFlag(fs) },
		run: func(c *call, args []string) error {
			if len(args) != 1 {
				return fail(ExitUsage, "Usage: crsp restore BACKUP [--yes]  (an id or file name from the controller)")
			}
			s, err := c.open()
			if err != nil {
				return err
			}
			l, err := s.client.ListBackupsWithResponse(c.env.Context)
			if err != nil {
				return s.transport(err)
			}
			if err := apiError(l.StatusCode(), l.Body); err != nil {
				return err
			}
			var b *client.Backup
			if l.JSON200 != nil {
				for i := range l.JSON200.Items {
					it := &l.JSON200.Items[i]
					if it.Id == args[0] || strings.EqualFold(it.File, args[0]) {
						b = it
					}
				}
			}
			if b == nil {
				return fail(ExitNotFound, "No backup %q on the controller.", args[0])
			}
			when := b.CreatedAt
			if err := c.confirm("This replaces the controller's configuration and history with the backup made "+output.Time(&when)+":", []string{"backup " + b.Id + " (" + b.File + ")"}, c.yesFlag()); err != nil {
				return err
			}
			yes := true
			r, err := s.client.RestoreBackupWithResponse(c.env.Context, b.Id, &client.RestoreBackupParams{Confirm: &yes})
			if err != nil {
				return s.transport(err)
			}
			if err := apiError(r.StatusCode(), r.Body); err != nil {
				return err
			}
			if c.g.json {
				return output.JSON(c.env.Stdout, r.JSON202)
			}
			_, _ = fmt.Fprintf(c.env.Stderr, "Restoring from backup %s.\n", b.Id)
			return nil
		},
	}
}
