package cli

import (
	"fmt"
	"os"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api/client"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/cli/output"
)

const dependenciesUsage = "Usage: crsp dependencies | crsp dependencies fetch ID | crsp dependencies import ID FILE"

// cmdDependencies is `crsp dependencies` (dependency-setup DEP15): what the
// controller keeps for machines without internet, with the same two actions as
// the console: let the controller download one (it checks the SHA-256), or give
// it a file you downloaded yourself.
func cmdDependencies() *command {
	return &command{
		name: "dependencies", summary: "what the controller keeps for machines without internet; fetch or import one",
		args: "[fetch ID | import ID FILE]",
		subs: []*command{
			{name: "fetch", summary: "download one dependency to the controller and check it", args: "ID",
				run: func(c *call, args []string) error {
					if len(args) != 1 {
						return fail(ExitUsage, dependenciesUsage)
					}
					s, err := c.open()
					if err != nil {
						return err
					}
					r, err := s.client.FetchDependencyWithResponse(c.env.Context, args[0])
					if err != nil {
						return s.transport(err)
					}
					if err := apiError(r.StatusCode(), r.Body); err != nil {
						return err
					}
					return c.printStoreEntry(r.JSON200, "fetched")
				}},
			{name: "import", summary: "give the controller a file you downloaded yourself", args: "ID FILE",
				run: func(c *call, args []string) error {
					if len(args) != 2 {
						return fail(ExitUsage, dependenciesUsage)
					}
					f, err := os.Open(args[1]) //nolint:gosec // the file the user named
					if err != nil {
						return fail(ExitUsage, "cannot read %s: %v", args[1], err)
					}
					defer func() { _ = f.Close() }()
					s, err := c.open()
					if err != nil {
						return err
					}
					r, err := s.client.ImportDependencyWithBodyWithResponse(c.env.Context, args[0], "application/octet-stream", f)
					if err != nil {
						return s.transport(err)
					}
					if err := apiError(r.StatusCode(), r.Body); err != nil {
						return err
					}
					return c.printStoreEntry(r.JSON200, "imported")
				}},
		},
		run: func(c *call, args []string) error {
			if len(args) != 0 {
				return fail(ExitUsage, dependenciesUsage)
			}
			s, err := c.open()
			if err != nil {
				return err
			}
			r, err := s.client.ListDependencyStoreWithResponse(c.env.Context)
			if err != nil {
				return s.transport(err)
			}
			if err := apiError(r.StatusCode(), r.Body); err != nil {
				return err
			}
			if r.JSON200 == nil {
				return nil
			}
			if c.g.json {
				return output.JSON(c.env.Stdout, map[string]any{"items": nonNil(r.JSON200.Items)})
			}
			var rows [][]string
			for _, e := range r.JSON200.Items {
				rows = append(rows, []string{e.Id, e.Name, storeStateWords(e), deref(e.Source)})
			}
			output.Table(c.env.Stdout, output.MaxWidth, []string{"ID", "NAME", "STATE", "FROM"}, rows)
			return nil
		},
	}
}

// storeStateWords says the state in plain words.
func storeStateWords(e client.DependencyStoreEntry) string {
	switch e.State {
	case client.DependencyStoreEntryStateReady:
		if e.Origin != nil && *e.Origin == client.Imported {
			return "ready (imported)"
		}
		return "ready"
	case client.DependencyStoreEntryStateMissing:
		return "not stored yet"
	case client.DependencyStoreEntryStateFailed:
		return "failed: " + deref(e.Error)
	case client.DependencyStoreEntryStateNotPinned:
		return "no checksum"
	}
	return string(e.State)
}

func (c *call) printStoreEntry(e *client.DependencyStoreEntry, verb string) error {
	if e == nil {
		return nil
	}
	if c.g.json {
		return output.JSON(c.env.Stdout, e)
	}
	_, _ = fmt.Fprintf(c.env.Stdout, "%s %s. The controller serves it to its machines.\n", e.Name, verb)
	return nil
}
