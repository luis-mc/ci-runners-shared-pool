package cli

import (
	"context"
	"flag"
	"fmt"
	"strings"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api/client"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/cli/output"
)

// releaseTimeout bounds the call that lifts the hold. It runs on a context that
// Ctrl-C does not cancel, so the hold is lifted on every way out.
const releaseTimeout = 30 * time.Second

// maintainResult is the --json document of `machine maintain`.
type maintainResult struct {
	Machine  string `json:"machine"`
	OK       bool   `json:"ok"`
	ExitCode int    `json:"exit_code"`
	Output   string `json:"output"`
	Message  string `json:"message,omitempty"`
	Released bool   `json:"released"`
}

func maintainCommand() *command {
	return &command{
		name: "maintain", summary: "hold new jobs, wait until idle, run a command on the machine, lift the hold",
		args: "NAME [--yes] [--command-timeout 30m] -- COMMAND [ARG...]", dashdash: true,
		flags: func(c *call, fs *flag.FlagSet) {
			yesFlag(fs)
			fs.Duration("command-timeout", 30*time.Minute, "how long the command may run on the machine")
		},
		run: func(c *call, args []string) error { return c.maintain(args) },
	}
}

// maintain is host maintenance without a cancel (cli R5.29, handover lesson 30):
// drain the machine (running jobs finish, queued jobs stay queued), run one
// command on it, and always lift the hold. It never stops or cancels a job.
func (c *call) maintain(args []string) error {
	if len(args) != 1 || len(c.after) == 0 || c.after[0] == "" {
		return fail(ExitUsage, "Usage: crsp machine maintain NAME [--yes] [--command-timeout 30m] -- COMMAND [ARG...]")
	}
	limit, _ := time.ParseDuration(c.fs.Lookup("command-timeout").Value.String())
	if limit < time.Second || limit > 24*time.Hour {
		return fail(ExitUsage, "--command-timeout must be between 1s and 24h.")
	}
	s, err := c.open()
	if err != nil {
		return err
	}
	m, err := c.findMachine(s, args[0])
	if err != nil {
		return err
	}
	switch {
	case machineState(m) == "offline":
		return fail(ExitConflict, "Machine %s is offline, so it cannot run the command. Nothing changed.", m.Name)
	case m.State != client.MachineStateActive:
		return fail(ExitConflict, "Machine %s is already %s. It is held by someone else, so crsp will not lift that hold. Resume it, or wait, and run this again. Nothing changed.", m.Name, m.State)
	}

	title := "This holds new jobs on machine " + m.Name + ", runs one command on it, and lifts the hold."
	items := []string{
		"machine " + m.Name,
		"command: " + strings.Join(c.after, " "),
		fmt.Sprintf("drain timeout: %d minutes; after it crsp keeps waiting, nothing is stopped", m.Settings.DrainTimeoutMinutes),
		"queued jobs stay queued and start again when the hold is lifted; no job is cancelled",
	}
	for _, j := range deref(m.RunningJobs) {
		items = append(items, "waits for running job: "+jobTitle(j))
	}
	if err := c.confirm(title, items, c.yesFlag()); err != nil {
		return err
	}

	res := maintainResult{Machine: m.Name, ExitCode: -1}
	reason := "maintenance"
	r, err := s.client.DrainMachineWithResponse(c.env.Context, m.Id, client.DrainRequest{Reason: &reason})
	if err != nil {
		// The call may have reached the controller: lift the hold to be sure.
		c.release(s, m, &res)
		return s.transport(err)
	}
	if err := apiError(r.StatusCode(), r.Body); err != nil {
		return err // refused: no hold of ours exists
	}
	defer func() {
		if !res.Released {
			c.release(s, m, &res)
		}
	}()
	if r.JSON202 == nil {
		return fail(ExitFailed, "The controller sent no change to follow. Lifting the hold.")
	}
	if err := c.waitChange(s, m, r.JSON202); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(c.env.Stderr, "Machine %s is drained. Running: %s\n", m.Name, c.after[0])

	secs := int(limit / time.Second)
	cr, err := s.client.RunMachineCommandWithResponse(c.env.Context, m.Id, client.HostCommandRequest{Command: c.after, TimeoutSeconds: &secs})
	if err != nil {
		return c.afterCancel(s.transport(err))
	}
	if err := apiError(cr.StatusCode(), cr.Body); err != nil {
		return err
	}
	if cr.JSON200 == nil {
		return fail(ExitFailed, "The controller sent no result for the command.")
	}
	res.OK, res.ExitCode, res.Output, res.Message = cr.JSON200.Ok, cr.JSON200.ExitCode, cr.JSON200.Output, deref(cr.JSON200.Message)
	if !c.g.json {
		_, _ = fmt.Fprint(c.env.Stdout, res.Output)
	}

	c.release(s, m, &res)
	if c.g.json {
		if err := output.JSON(c.env.Stdout, res); err != nil {
			return err
		}
	}
	switch {
	case !res.Released:
		return fail(ExitFailed, "The command %s, but the hold on %s could not be lifted. Run: crsp machine resume %s", outcomeWord(res), m.Name, m.Name)
	case !res.OK:
		return fail(ExitFailed, "The command %s on %s (exit code %d)%s. The hold is lifted; no job was stopped.", outcomeWord(res), m.Name, res.ExitCode, withText(res.Message))
	}
	_, _ = fmt.Fprintf(c.env.Stderr, "Done. Machine %s takes jobs again; queued jobs start on their own.\n", m.Name)
	return nil
}

// afterCancel words the error of a command call that was cut by Ctrl-C: the
// command may still run on the machine until its own timeout.
func (c *call) afterCancel(err error) error {
	if c.env.Context.Err() != nil {
		_, _ = fmt.Fprintln(c.env.Stderr, "Stopped waiting. The command may still run on the machine until its own timeout. No job was stopped.")
	}
	return err
}

// release lifts the hold on a context that Ctrl-C does not cancel. With confirm,
// a drain that still waits is cancelled; that stops no job.
func (c *call) release(s *session, m client.Machine, res *maintainResult) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(c.env.Context), releaseTimeout)
	defer cancel()
	yes := true
	r, err := s.client.ResumeMachineWithResponse(ctx, m.Id, &client.ResumeMachineParams{Confirm: &yes})
	if err == nil {
		err = apiError(r.StatusCode(), r.Body)
	}
	if err != nil {
		_, _ = fmt.Fprintf(c.env.Stderr, "Could not lift the hold on %s: %v. Run: crsp machine resume %s\n", m.Name, err, m.Name)
		return
	}
	res.Released = true
	_, _ = fmt.Fprintf(c.env.Stderr, "Hold lifted on %s. No queued or running job was cancelled.\n", m.Name)
}

func outcomeWord(r maintainResult) string {
	if r.ExitCode < 0 {
		return "did not finish"
	}
	return "failed"
}

func withText(t string) string {
	if t == "" {
		return ""
	}
	return ": " + t
}
