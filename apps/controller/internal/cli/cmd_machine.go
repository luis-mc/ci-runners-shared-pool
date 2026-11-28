package cli

import (
	"flag"
	"fmt"
	"mime"
	"regexp"
	"strings"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api/client"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/cli/output"
)

func (c *call) machines(s *session) ([]client.Machine, error) {
	r, err := s.client.ListMachinesWithResponse(c.env.Context)
	if err != nil {
		return nil, s.transport(err)
	}
	if err := apiError(r.StatusCode(), r.Body); err != nil {
		return nil, err
	}
	if r.JSON200 == nil {
		return nil, nil
	}
	return r.JSON200.Items, nil
}

func (c *call) findMachine(s *session, nameOrID string) (client.Machine, error) {
	ms, err := c.machines(s)
	if err != nil {
		return client.Machine{}, err
	}
	for _, m := range ms {
		if strings.EqualFold(m.Name, nameOrID) || m.Id == nameOrID {
			return m, nil
		}
	}
	return client.Machine{}, fail(ExitNotFound, "No machine %q. Run crsp machine list.", nameOrID)
}

func machineState(m client.Machine) string {
	if !m.Online && m.State == client.MachineStateActive {
		return "offline"
	}
	return string(m.State)
}

func backendNames(m client.Machine) string {
	var n []string
	for _, b := range m.Backends {
		if b.Present && b.Usable && b.Allowed {
			n = append(n, string(b.Name))
		}
	}
	return strings.Join(n, ",")
}

func jobTitle(j client.Job) string {
	return j.Repository + " " + deref(j.Workflow) + " " + j.JobName
}

func cmdMachine() *command {
	return &command{
		name: "machine", summary: "machines in the pool",
		subs: []*command{
			{name: "list", summary: "list machines with load and slots",
				run: func(c *call, _ []string) error {
					s, err := c.open()
					if err != nil {
						return err
					}
					ms, err := c.machines(s)
					if err != nil {
						return err
					}
					if c.g.json {
						return output.JSON(c.env.Stdout, client.MachineList{Items: nonNil(ms)})
					}
					if len(ms) == 0 {
						_, _ = fmt.Fprintln(c.env.Stderr, "No machines yet. Run `crsp machine add` to add one.")
						return nil
					}
					var rows [][]string
					for _, m := range ms {
						mem, cpu := "-", "-"
						if m.Load != nil {
							mem, cpu = output.Percent(m.Load.MemoryPct), output.Percent(m.Load.CpuPct)
						}
						free := m.Settings.JobSlots - m.SlotsUsed
						if free < 0 {
							free = 0
						}
						rows = append(rows, []string{m.Name, machineState(m), string(m.Os), backendNames(m), fmt.Sprint(m.SlotsUsed), fmt.Sprint(free), mem, cpu})
					}
					output.Table(c.env.Stdout, output.MaxWidth, []string{"NAME", "STATE", "OS", "BACKENDS", "USED", "FREE", "MEM", "CPU"}, rows)
					return nil
				}},
			{name: "add", summary: "print a join command with a one-time code", args: "[--name NAME] [--expires 15m]",
				flags: func(c *call, fs *flag.FlagSet) {
					fs.String("name", "", "name to give the machine when it joins")
					fs.Duration("expires", 15*time.Minute, "how long the code is valid")
				},
				run: func(c *call, _ []string) error {
					s, err := c.open()
					if err != nil {
						return err
					}
					d, _ := time.ParseDuration(c.fs.Lookup("expires").Value.String())
					mins := int((d + time.Minute - 1) / time.Minute)
					if mins < 1 || mins > 1440 {
						return fail(ExitUsage, "--expires must be between 1m and 24h.")
					}
					r, err := s.client.CreateJoinCodeWithResponse(c.env.Context, client.CreateJoinCodeRequest{TtlMinutes: &mins})
					if err != nil {
						return s.transport(err)
					}
					if err := apiError(r.StatusCode(), r.Body); err != nil {
						return err
					}
					if r.JSON201 == nil {
						return fail(ExitFailed, "The controller sent no join code.")
					}
					if c.g.json {
						return output.JSON(c.env.Stdout, r.JSON201)
					}
					_, _ = fmt.Fprintf(c.env.Stderr, "Run one of these on the new machine. The code works once and is valid until %s.\n", output.Time(&r.JSON201.JoinCode.ExpiresAt))
					if n := c.fs.Lookup("name").Value.String(); n != "" {
						_, _ = fmt.Fprintf(c.env.Stderr, "Name it %q when the agent asks.\n", n)
					}
					for _, it := range r.JSON201.Commands.Items {
						_, _ = fmt.Fprintf(c.env.Stdout, "%s: %s\n", it.Os, it.Command)
					}
					return nil
				}},
			{name: "join-file", summary: "download an installer file with a one-time join code built in", args: "--os windows|linux|macos [--out FILE] [--expires 15m]",
				flags: func(c *call, fs *flag.FlagSet) {
					fs.String("os", "", "the machine's system: windows, linux or macos")
					fs.String("out", "", "where to save the file (default: its own name in this folder)")
					fs.Duration("expires", 15*time.Minute, "how long the code is valid")
				},
				run: func(c *call, _ []string) error {
					osName := c.fs.Lookup("os").Value.String()
					if osName != "windows" && osName != "linux" && osName != "macos" {
						return fail(ExitUsage, "Usage: crsp machine join-file --os windows|linux|macos [--out FILE] [--expires 15m]")
					}
					d, _ := time.ParseDuration(c.fs.Lookup("expires").Value.String())
					mins := int((d + time.Minute - 1) / time.Minute)
					if mins < 1 || mins > 1440 {
						return fail(ExitUsage, "--expires must be between 1m and 24h.")
					}
					s, err := c.open()
					if err != nil {
						return err
					}
					r, err := s.client.CreateJoinFileWithResponse(c.env.Context, client.CreateJoinFileRequest{Os: client.CreateJoinFileRequestOs(osName), TtlMinutes: &mins})
					if err != nil {
						return s.transport(err)
					}
					if err := apiError(r.StatusCode(), r.Body); err != nil {
						return err
					}
					path := c.fs.Lookup("out").Value.String()
					if path == "" {
						path = joinFileName(r.HTTPResponse.Header.Get("Content-Disposition"), osName)
					}
					// Owner only on every system: the file holds a one-time code.
					if err := writeOwnerOnly(path, r.Body, osName != "windows"); err != nil {
						return fail(ExitFailed, "Could not save the file: %v", err)
					}
					if c.g.json {
						return output.JSON(c.env.Stdout, map[string]string{"file": path, "join_code_id": r.HTTPResponse.Header.Get("X-Join-Code-Id")})
					}
					_, _ = fmt.Fprintf(c.env.Stderr, "Copy this file to the new machine and open it there. Its code works once and expires in %d minutes. Delete the file after you use it.\n", mins)
					_, _ = fmt.Fprintln(c.env.Stdout, path)
					return nil
				}},
			cmdAgentInstallers(),
			{name: "drain", summary: "stop giving a machine new jobs; wait for running jobs", args: "NAME [--yes]",
				flags: drainFlags,
				run:   func(c *call, args []string) error { return c.drainOrRemove(args, false) }},
			{name: "resume", summary: "let a drained machine take jobs again", args: "NAME",
				run: func(c *call, args []string) error {
					if len(args) != 1 {
						return fail(ExitUsage, "Usage: crsp machine resume NAME")
					}
					s, err := c.open()
					if err != nil {
						return err
					}
					m, err := c.findMachine(s, args[0])
					if err != nil {
						return err
					}
					r, err := s.client.ResumeMachineWithResponse(c.env.Context, m.Id, nil)
					if err != nil {
						return s.transport(err)
					}
					if err := apiError(r.StatusCode(), r.Body); err != nil {
						return err
					}
					if c.g.json {
						return output.JSON(c.env.Stdout, r.JSON200)
					}
					_, _ = fmt.Fprintf(c.env.Stderr, "Machine %s takes jobs again.\n", m.Name)
					return nil
				}},
			maintainCommand(),
			machineSettingsCommand(),
			machineLeftoversCommand(),
			{name: "remove", summary: "drain and remove a machine", args: "NAME [--yes]",
				flags: drainFlags,
				run:   func(c *call, args []string) error { return c.drainOrRemove(args, true) }},
		},
	}
}

func drainFlags(c *call, fs *flag.FlagSet) {
	yesFlag(fs)
}

// stillDrainingEvery is how often a wait past the drain timeout repeats its warning.
const stillDrainingEvery = 5 * time.Minute

// drainOrRemove drains or removes a machine without cutting a running job
// (cli R5.14 to R5.16).
func (c *call) drainOrRemove(args []string, remove bool) error {
	verb := "drain"
	if remove {
		verb = "remove"
	}
	if len(args) != 1 {
		return fail(ExitUsage, "Usage: crsp machine %s NAME [--yes]", verb)
	}
	s, err := c.open()
	if err != nil {
		return err
	}
	m, err := c.findMachine(s, args[0])
	if err != nil {
		return err
	}
	title := "This " + verb + "s machine " + m.Name + "."
	items := []string{"machine " + m.Name}
	if remove {
		title = "This removes machine " + m.Name + "."
	}
	for _, j := range deref(m.RunningJobs) {
		items = append(items, "waits for running job: "+jobTitle(j))
	}
	if err := c.confirm(title, items, c.yesFlag()); err != nil {
		return err
	}
	var change *client.MachineChange
	if remove {
		yes := true
		r, err := s.client.RemoveMachineWithResponse(c.env.Context, m.Id, &client.RemoveMachineParams{Confirm: &yes})
		if err != nil {
			return s.transport(err)
		}
		if err := apiError(r.StatusCode(), r.Body); err != nil {
			return err
		}
		change = r.JSON202
	} else {
		r, err := s.client.DrainMachineWithResponse(c.env.Context, m.Id, client.DrainRequest{})
		if err != nil {
			return s.transport(err)
		}
		if err := apiError(r.StatusCode(), r.Body); err != nil {
			return err
		}
		change = r.JSON202
	}
	if change == nil {
		return fail(ExitFailed, "The controller sent no change to follow.")
	}
	if err := c.waitChange(s, m, change); err != nil {
		return err
	}
	if c.g.json {
		return output.JSON(c.env.Stdout, change)
	}
	past := "drained"
	if remove {
		past = "removed"
	}
	_, _ = fmt.Fprintf(c.env.Stderr, "Machine %s %s.\n", m.Name, past)
	return nil
}

// waitChange follows a drain change until it is done. One drain timeout per
// machine, set in its settings: when it passes, the command keeps waiting and
// warns; it never stops a job (cli R5.16).
func (c *call) waitChange(s *session, m client.Machine, change *client.MachineChange) error {
	timeout := time.Duration(m.Settings.DrainTimeoutMinutes) * time.Minute
	started := c.env.Now()
	var lastWarn time.Time
	lastWait := -1
	for change.State != client.MachineChangeStateDone {
		switch change.State {
		case client.MachineChangeStateCancelled:
			return fail(ExitFailed, "The change was cancelled. Nothing more changed.")
		case client.MachineChangeStateNeedsYou:
			return fail(ExitFailed, "The change needs you and did not run. Open the console, or run crsp doctor. Running jobs were not stopped.")
		}
		if n := len(change.WaitingFor); n != lastWait {
			lastWait = n
			var left string
			if change.DeadlineAt != nil {
				left = fmt.Sprintf(" (about %d min left)", int(change.DeadlineAt.Sub(c.env.Now()).Minutes())+1)
			}
			_, _ = fmt.Fprintf(c.env.Stderr, "Waiting for %d jobs to end on %s%s.\n", n, m.Name, left)
			for _, j := range change.WaitingFor {
				_, _ = fmt.Fprintf(c.env.Stderr, "  - %s\n", jobTitle(j))
			}
		}
		if waited := c.env.Now().Sub(started); timeout > 0 && waited >= timeout && len(change.WaitingFor) > 0 && c.env.Now().Sub(lastWarn) >= stillDrainingEvery {
			lastWarn = c.env.Now()
			_, _ = fmt.Fprintf(c.env.Stderr, "Still draining %s: %d jobs running, waited %s. Nothing is stopped; press Ctrl-C to stop waiting (the drain goes on).\n", m.Name, len(change.WaitingFor), waited.Round(time.Minute))
		}
		if err := c.sleep(2 * time.Second); err != nil {
			return err
		}
		g, err := s.client.GetMachineChangeWithResponse(c.env.Context, m.Id, change.Id)
		if err != nil {
			return s.transport(err)
		}
		if err := apiError(g.StatusCode(), g.Body); err != nil {
			return err
		}
		if g.JSON200 != nil {
			change = g.JSON200
		}
	}
	return nil
}

// joinFileName reads the file name the controller chose (it says the OS and the
// expiry). Anything that is not a plain name falls back to a fixed one, so the
// answer never decides a folder.
func joinFileName(contentDisposition, osName string) string {
	if _, params, err := mime.ParseMediaType(contentDisposition); err == nil {
		if n := params["filename"]; joinFileNameRe.MatchString(n) {
			return n
		}
	}
	ext := map[string]string{"windows": "cmd", "linux": "sh", "macos": "command"}[osName]
	return "crsp-join-" + osName + "." + ext
}

var joinFileNameRe = regexp.MustCompile(`^crsp-join-[a-z]+-until-[0-9TZ]+\.(cmd|sh|command)$`)
