package cli

import (
	"fmt"
	"strings"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api/client"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/cli/output"
)

// leftoversCheck is the doctor check that lists the runner leftovers of a machine.
const leftoversCheck = "machine.runner_leftovers"

const leftoversUsage = "Usage: crsp machine leftovers MACHINE | crsp machine leftovers remove MACHINE NAME... [--yes]"

// machineLeftoversCommand is `machine leftovers MACHINE` and
// `machine leftovers remove MACHINE NAME...` (cli R5.63, doctor R5.48, D104).
func machineLeftoversCommand() *command {
	return &command{
		name: "leftovers", summary: "list or remove runner leftovers no controller owns",
		args:  "MACHINE | remove MACHINE NAME... [--yes]",
		flags: drainFlags,
		run: func(c *call, args []string) error {
			if len(args) > 0 && args[0] == "remove" {
				if len(args) < 3 {
					return fail(ExitUsage, leftoversUsage)
				}
				return c.leftoversRemove(args[1], args[2:])
			}
			if len(args) != 1 {
				return fail(ExitUsage, leftoversUsage)
			}
			return c.leftoversList(args[0])
		},
	}
}

// machineLeftovers reads the leftovers of the latest doctor finding of a machine.
func (c *call) machineLeftovers(s *session, m client.Machine) ([]client.RunnerLeftover, error) {
	r, err := s.client.ListDoctorFindingsWithResponse(c.env.Context, &client.ListDoctorFindingsParams{})
	if err != nil {
		return nil, s.transport(err)
	}
	if err := apiError(r.StatusCode(), r.Body); err != nil {
		return nil, err
	}
	if r.JSON200 == nil {
		return nil, nil
	}
	var out []client.RunnerLeftover
	for _, f := range r.JSON200.Items {
		if f.Check == leftoversCheck && f.Subject.Id == m.Id {
			out = append(out, deref(f.Items)...)
		}
	}
	return out, nil
}

func (c *call) leftoversList(name string) error {
	s, err := c.open()
	if err != nil {
		return err
	}
	m, err := c.findMachine(s, name)
	if err != nil {
		return err
	}
	items, err := c.machineLeftovers(s, m)
	if err != nil {
		return err
	}
	if c.g.json {
		return output.JSON(c.env.Stdout, map[string]any{"items": nonNil(items)})
	}
	if len(items) == 0 {
		_, _ = fmt.Fprintf(c.env.Stderr, "No runner leftovers on %s in the latest doctor scan. Run crsp doctor --machine %s to scan again.\n", m.Name, m.Name)
		return nil
	}
	var rows [][]string
	for _, it := range items {
		size := "-"
		if it.SizeBytes != nil {
			size = fmt.Sprintf("%d MiB", *it.SizeBytes>>20)
		}
		rows = append(rows, []string{string(it.Kind), it.Name, size, it.Why})
	}
	output.Table(c.env.Stdout, output.MaxWidth, []string{"KIND", "NAME", "SIZE", "WHY"}, rows)
	_, _ = fmt.Fprintf(c.env.Stderr, "Remove with: crsp machine leftovers remove %s NAME...\n", m.Name)
	return nil
}

func (c *call) leftoversRemove(machine string, names []string) error {
	s, err := c.open()
	if err != nil {
		return err
	}
	m, err := c.findMachine(s, machine)
	if err != nil {
		return err
	}
	found, err := c.machineLeftovers(s, m)
	if err != nil {
		return err
	}
	var req client.RemoveRunnerLeftoversRequest
	var shown []string
	for _, n := range names {
		hit := false
		for _, it := range found {
			if it.Name != n {
				continue
			}
			hit = true
			req.Items = append(req.Items, struct {
				Kind client.RunnerLeftoverKind `json:"kind"`
				Name string                    `json:"name"`
			}{Kind: it.Kind, Name: it.Name})
			shown = append(shown, string(it.Kind)+" "+it.Name)
		}
		if !hit {
			return fail(ExitNotFound, "%q is not in the latest doctor scan of %s. Run crsp machine leftovers %s.", n, m.Name, m.Name)
		}
	}
	if err := c.confirm("This removes these runner leftovers from machine "+m.Name+".", shown, c.yesFlag()); err != nil {
		return err
	}
	r, err := s.client.RemoveRunnerLeftoversWithResponse(c.env.Context, m.Id, req)
	if err != nil {
		return s.transport(err)
	}
	if err := apiError(r.StatusCode(), r.Body); err != nil {
		return err
	}
	if r.JSON200 == nil {
		return fail(ExitFailed, "The controller sent no result.")
	}
	bad := 0
	for _, it := range r.JSON200.Results {
		if it.Outcome == client.RemoveRunnerLeftoversResultResultsOutcomeRefused || it.Outcome == client.RemoveRunnerLeftoversResultResultsOutcomeFailed {
			bad++
		}
	}
	if c.g.json {
		if err := output.JSON(c.env.Stdout, r.JSON200); err != nil {
			return err
		}
	} else {
		for _, it := range r.JSON200.Results {
			line := fmt.Sprintf("%s %s %s", strings.ReplaceAll(string(it.Outcome), "_", " "), it.Kind, it.Name)
			if it.Error != nil && *it.Error != "" {
				line += ": " + *it.Error
			}
			_, _ = fmt.Fprintln(c.env.Stdout, line)
		}
	}
	if bad > 0 {
		return fail(ExitFailed, "%d of %d leftovers were not removed.", bad, len(r.JSON200.Results))
	}
	return nil
}
