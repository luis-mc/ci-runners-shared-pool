package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api/client"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/cli/output"
)

// maxConfigFile is the largest config file the controller reads (contract: 1 MiB).
const maxConfigFile = 1 << 20

// cmdConfig is `crsp config`: the setup as one file (operations R5.28 to R5.30).
func cmdConfig() *command {
	return &command{
		name: "config", summary: "the setup as one file (export, plan, apply, import --dry-run)",
		subs: []*command{
			{name: "export", summary: "write the setup as one file without any secret (admin)", args: "[--out FILE]",
				flags: func(c *call, fs *flag.FlagSet) {
					fs.String("out", "", "where to save the file (default: print it)")
				},
				run: func(c *call, _ []string) error { return c.configExport() }},
			{name: "plan", summary: "show what a config file would change; changes nothing; exits 8 when it has changes (admin)", args: "FILE",
				run: func(c *call, args []string) error { return c.configPlan(args, "plan") }},
			{name: "import", summary: "same as plan: `import FILE --dry-run` changes nothing; use apply to change (admin)", args: "FILE --dry-run",
				flags: func(c *call, fs *flag.FlagSet) {
					fs.Bool("dry-run", false, "show what would change; change nothing")
				},
				run: func(c *call, args []string) error {
					if !flagSet(c.fs, "dry-run") || c.fs.Lookup("dry-run").Value.String() != "true" {
						return fail(ExitUsage, "Usage: crsp config import FILE --dry-run. To change the setup run: crsp config apply FILE")
					}
					return c.configPlan(args, "import FILE --dry-run")
				}},
			{name: "apply", summary: "plan a config file, ask, then apply exactly that plan (owner)", args: "FILE [--yes]",
				flags: func(_ *call, fs *flag.FlagSet) { yesFlag(fs) },
				run:   func(c *call, args []string) error { return c.configApply(args) }},
		},
	}
}

// readConfigFile reads FILE, or stdin for "-".
func (c *call) readConfigFile(args []string, usage string) (string, error) {
	if len(args) != 1 || args[0] == "" {
		return "", fail(ExitUsage, "Usage: crsp config %s", usage)
	}
	var (
		b   []byte
		err error
	)
	if args[0] == "-" {
		b, err = io.ReadAll(io.LimitReader(c.stdin(), maxConfigFile+1))
	} else {
		var f *os.File
		if f, err = os.Open(args[0]); err == nil {
			b, err = io.ReadAll(io.LimitReader(f, maxConfigFile+1))
			_ = f.Close()
		}
	}
	if err != nil {
		return "", fail(ExitUsage, "Could not read the file: %v", err)
	}
	if len(b) > maxConfigFile {
		return "", fail(ExitUsage, "The file is larger than 1 MiB, so it is not a config file.")
	}
	return string(b), nil
}

// configExport saves the raw YAML: the generated client cannot parse it.
func (c *call) configExport() error {
	s, err := c.open()
	if err != nil {
		return err
	}
	r, err := s.client.ExportConfigFile(c.env.Context)
	if err != nil {
		return s.transport(err)
	}
	defer func() { _ = r.Body.Close() }()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return s.transport(err)
	}
	if err := apiError(r.StatusCode, body); err != nil {
		return err
	}
	path := c.fs.Lookup("out").Value.String()
	if path == "" {
		_, err = c.env.Stdout.Write(body)
		return err
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		return fail(ExitFailed, "Could not save the file: %v", err)
	}
	if c.g.json {
		return output.JSON(c.env.Stdout, map[string]string{"file": path})
	}
	_, _ = fmt.Fprintln(c.env.Stdout, path)
	return nil
}

// planFile sends the file to the controller. Nothing changes there.
func (c *call) planFile(s *session, text string) (*client.ConfigPlan, error) {
	r, err := s.client.PlanConfigWithResponse(c.env.Context, client.PlanConfigJSONRequestBody{File: text})
	if err != nil {
		return nil, s.transport(err)
	}
	if err := apiError(r.StatusCode(), r.Body); err != nil {
		return nil, err
	}
	if r.JSON201 == nil {
		return nil, fail(ExitFailed, "The controller sent no plan.")
	}
	return r.JSON201, nil
}

func changeLines(cs []client.ConfigChange) []string {
	var out []string
	for _, ch := range cs {
		name := ch.Section
		if ch.Name != "" {
			name += "/" + ch.Name
		}
		out = append(out, fmt.Sprintf("%s %s", ch.Kind, name))
		for _, f := range deref(ch.Fields) {
			out = append(out, fmt.Sprintf("  %s: %v -> %v", f.Field, f.Old, f.New))
		}
	}
	return out
}

func problemError(p *client.ConfigPlan) error {
	return fail(ExitFailed, "The file has problems, so it cannot be applied. Nothing changed.\n  %s", strings.Join(deref(p.Problems), "\n  "))
}

// configPlan lists what the file would change. Exit 8 when it has changes.
func (c *call) configPlan(args []string, usage string) error {
	text, err := c.readConfigFile(args, usage)
	if err != nil {
		return err
	}
	s, err := c.open()
	if err != nil {
		return err
	}
	p, err := c.planFile(s, text)
	if err != nil {
		return err
	}
	if c.g.json {
		if err := output.JSON(c.env.Stdout, p); err != nil {
			return err
		}
	}
	if len(deref(p.Problems)) > 0 {
		return problemError(p)
	}
	if len(p.Changes) == 0 {
		if !c.g.json {
			_, _ = fmt.Fprintln(c.env.Stdout, "No changes. The setup already matches the file.")
		}
		return nil
	}
	if !c.g.json {
		for _, l := range changeLines(p.Changes) {
			_, _ = fmt.Fprintln(c.env.Stdout, l)
		}
		if n := len(deref(p.Removals)); n > 0 {
			_, _ = fmt.Fprintf(c.env.Stderr, "%d %s. Applying needs a yes that names each one.\n", n, plural(n, "removal", "removals"))
		}
		_, _ = fmt.Fprintln(c.env.Stdout, "Nothing has changed yet.")
	}
	return &exitError{Code: ExitFindings, Message: "The file has changes. Nothing changed. Run: crsp config apply " + args[0]}
}

// configApply plans, asks (R5.14) and applies exactly that plan. Each removal
// is sent by name.
func (c *call) configApply(args []string) error {
	text, err := c.readConfigFile(args, "apply FILE [--yes]")
	if err != nil {
		return err
	}
	if args[0] == "-" && !c.yesFlag() {
		return fail(ExitUsage, "Reading the file from stdin leaves no way to ask. Add --yes, or give a file name.")
	}
	s, err := c.open()
	if err != nil {
		return err
	}
	p, err := c.planFile(s, text)
	if err != nil {
		return err
	}
	if len(deref(p.Problems)) > 0 {
		return problemError(p)
	}
	if len(p.Changes) == 0 || p.Id == nil {
		_, _ = fmt.Fprintln(c.env.Stderr, "No changes. The setup already matches the file.")
		return nil
	}
	removals := deref(p.Removals)
	title := "This changes the setup to match the file."
	if len(removals) > 0 {
		title = fmt.Sprintf("This changes the setup to match the file and removes %d %s: %s.", len(removals), plural(len(removals), "item", "items"), strings.Join(removals, ", "))
	}
	if err := c.confirm(title, changeLines(p.Changes), c.yesFlag()); err != nil {
		return err
	}
	r, err := s.client.ApplyConfigPlanWithResponse(c.env.Context, *p.Id, client.ConfigApplyRequest{ConfirmRemovals: &removals})
	if err != nil {
		return s.transport(err)
	}
	if err := apiError(r.StatusCode(), r.Body); err != nil {
		return err
	}
	if r.JSON200 == nil {
		return fail(ExitFailed, "The controller sent no result.")
	}
	if c.g.json {
		return output.JSON(c.env.Stdout, r.JSON200)
	}
	_, _ = fmt.Fprintf(c.env.Stdout, "Done. %d %s applied.\n", len(r.JSON200.Applied), plural(len(r.JSON200.Applied), "change", "changes"))
	return nil
}
