package cli

import (
	"flag"
	"fmt"
	"strconv"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api/client"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/cli/output"
)

// machineSettingsCommand is `machine settings NAME [flags]` (caches R5.51 to R5.54): the
// Docker disk compact policy of one machine, readable and settable from the CLI
// with the same rights as the Machine page.
func machineSettingsCommand() *command {
	return &command{
		name: "settings", summary: "show or change the Docker disk compact policy of a machine",
		args: "NAME [--docker-compact on|off|auto] [--docker-compact-threshold GIB] [--docker-compact-idle MINUTES]",
		flags: func(c *call, fs *flag.FlagSet) {
			fs.String("docker-compact", "", "on, off or auto (auto is on for a Windows machine with a Docker Desktop disk file)")
			fs.Int("docker-compact-threshold", 0, "GiB reclaimable (file size minus used) before the compact runs; default 30")
			fs.Int("docker-compact-idle", 0, "minutes with no job running or queued before the compact runs; default 30")
		},
		run: func(c *call, args []string) error {
			if len(args) != 1 {
				return fail(ExitUsage, "Usage: crsp machine settings NAME [--docker-compact on|off|auto] [--docker-compact-threshold GIB] [--docker-compact-idle MINUTES]")
			}
			body, err := compactUpdate(c)
			if err != nil {
				return err
			}
			s, err := c.open()
			if err != nil {
				return err
			}
			m, err := c.findMachine(s, args[0])
			if err != nil {
				return err
			}
			if body != nil {
				r, err := s.client.UpdateMachineWithResponse(c.env.Context, m.Id, *body)
				if err != nil {
					return s.transport(err)
				}
				if err := apiError(r.StatusCode(), r.Body); err != nil {
					return err
				}
				if r.JSON200 != nil {
					m = *r.JSON200
				}
			}
			if c.g.json {
				return output.JSON(c.env.Stdout, m.Settings)
			}
			printCompact(c, m)
			return nil
		},
	}
}

// compactUpdate builds the request from the flags the user gave; nil when none.
func compactUpdate(c *call) (*client.MachineSettingsUpdate, error) {
	var u client.MachineSettingsUpdate
	set := false
	c.fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "docker-compact", "docker-compact-threshold", "docker-compact-idle":
			set = true
		}
	})
	if !set {
		return nil, nil
	}
	if v := c.fs.Lookup("docker-compact").Value.String(); v != "" {
		mode := client.DockerCompactMode(v)
		if !mode.Valid() {
			return nil, fail(ExitUsage, "--docker-compact is on, off or auto.")
		}
		u.DockerCompact = &mode
	}
	num := func(name string, max int) (*int, error) {
		fl := c.fs.Lookup(name)
		seen := false
		c.fs.Visit(func(f *flag.Flag) { seen = seen || f.Name == name })
		if !seen {
			return nil, nil
		}
		n, err := strconv.Atoi(fl.Value.String())
		if err != nil || n < 1 || n > max {
			return nil, fail(ExitUsage, "--%s is a whole number from 1 to %d.", name, max)
		}
		return &n, nil
	}
	var err error
	if u.DockerCompactThresholdGib, err = num("docker-compact-threshold", 100000); err != nil {
		return nil, err
	}
	if u.DockerCompactIdleMinutes, err = num("docker-compact-idle", 1440); err != nil {
		return nil, err
	}
	return &u, nil
}

func printCompact(c *call, m client.Machine) {
	st := m.Settings
	w := c.env.Stdout
	mode := "auto"
	if st.DockerCompact != nil {
		mode = string(*st.DockerCompact)
	}
	_, _ = fmt.Fprintf(w, "Docker disk compact on %s: %s\n", m.Name, mode)
	_, _ = fmt.Fprintf(w, "  reclaimable space to start: %d GiB\n", deref(st.DockerCompactThresholdGib))
	_, _ = fmt.Fprintf(w, "  idle before it runs: %d minutes\n", deref(st.DockerCompactIdleMinutes))
	_, _ = fmt.Fprintln(w, "  at most once per day")
	if d := m.DockerCompactStatus; d != nil {
		on := "off"
		if d.Enabled {
			on = "on"
		}
		_, _ = fmt.Fprintf(w, "  now: %s", on)
		if d.DiskPresent {
			_, _ = fmt.Fprintf(w, ", disk file %d GiB, reclaimable %d GiB", deref(d.FileBytes)>>30, deref(d.ReclaimableBytes)>>30)
		}
		if d.DiskPresent && !d.CanCompact {
			_, _ = fmt.Fprint(w, ", the agent has no administrator rights")
		}
		_, _ = fmt.Fprintln(w)
		if d.LastResult != nil {
			_, _ = fmt.Fprintf(w, "  last compact: %s (%s)\n", deref(d.LastResult), output.Time(d.LastAt))
		}
	}
}
