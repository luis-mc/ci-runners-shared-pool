package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api/client"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/cli/output"
)

// sumsFile is the checksum file of a release, the one file that is not an installer.
const sumsFile = "SHA256SUMS"

// cmdAgentInstallers is `crsp machine agent-installers` (operations R5.132, CRSP347): list the agent installers
// this controller has, and import the files of a release that were downloaded by hand (an offline controller).
// The controller checks each file against SHA256SUMS and keeps nothing that does not match.
func cmdAgentInstallers() *command {
	return &command{
		name: "agent-installers", summary: "list the agent installers of this controller, or import downloaded ones",
		subs: []*command{
			{name: "list", summary: "list the agent installers of the release and which ones are stored",
				run: func(c *call, _ []string) error {
					s, err := c.open()
					if err != nil {
						return err
					}
					r, err := s.client.ListAgentInstallersWithResponse(c.env.Context)
					if err != nil {
						return s.transport(err)
					}
					if err := apiError(r.StatusCode(), r.Body); err != nil {
						return err
					}
					return c.printAgentInstallers(r.JSON200)
				}},
			{name: "import", summary: "store downloaded agent installers and SHA256SUMS; each is checked against SHA256SUMS", args: "FILE|FOLDER...",
				run: func(c *call, args []string) error {
					files, err := agentInstallerFiles(args)
					if err != nil {
						return err
					}
					s, err := c.open()
					if err != nil {
						return err
					}
					var last *client.AgentInstallers
					failed := 0
					for _, path := range files {
						name := filepath.Base(path)
						f, err := os.Open(path) //nolint:gosec // a file the user named
						if err != nil {
							return fail(ExitFailed, "Could not read %s: %v", path, err)
						}
						r, err := s.client.ImportAgentInstallerWithBodyWithResponse(c.env.Context, name, "application/octet-stream", f)
						_ = f.Close()
						if err != nil {
							return s.transport(err)
						}
						if err := apiError(r.StatusCode(), r.Body); err != nil {
							failed++
							_, _ = fmt.Fprintf(c.env.Stderr, "Not stored: %s. %s\n", name, err)
							continue
						}
						last = r.JSON200
						if !c.g.json {
							_, _ = fmt.Fprintf(c.env.Stderr, "Stored %s (checked against SHA256SUMS).\n", name)
						}
					}
					if last != nil {
						if err := c.printAgentInstallers(last); err != nil {
							return err
						}
					}
					if failed > 0 {
						return fail(ExitFailed, "%d of %d files were not stored. Nothing from them was kept.", failed, len(files))
					}
					return nil
				}},
		},
	}
}

// agentInstallerFiles turns the arguments into the files to send: a folder gives its SHA256SUMS and CrspAgent-*
// files. SHA256SUMS always goes first, because every installer is checked against it.
func agentInstallerFiles(args []string) ([]string, error) {
	if len(args) == 0 {
		return nil, fail(ExitUsage, "Usage: crsp machine agent-installers import FILE|FOLDER... (SHA256SUMS and the CrspAgent-* files of the release)")
	}
	var files []string
	for _, a := range args {
		st, err := os.Stat(a)
		if err != nil {
			return nil, fail(ExitNotFound, "No such file or folder: %s", a)
		}
		if !st.IsDir() {
			files = append(files, a)
			continue
		}
		entries, err := os.ReadDir(a)
		if err != nil {
			return nil, fail(ExitFailed, "Could not read the folder %s: %v", a, err)
		}
		for _, e := range entries {
			if !e.IsDir() && (e.Name() == sumsFile || strings.HasPrefix(e.Name(), "CrspAgent-")) {
				files = append(files, filepath.Join(a, e.Name()))
			}
		}
	}
	if len(files) == 0 {
		return nil, fail(ExitNotFound, "No SHA256SUMS or CrspAgent-* file in %s.", strings.Join(args, ", "))
	}
	sort.SliceStable(files, func(i, j int) bool {
		return filepath.Base(files[i]) == sumsFile && filepath.Base(files[j]) != sumsFile
	})
	return files, nil
}

func (c *call) printAgentInstallers(l *client.AgentInstallers) error {
	if l == nil {
		return nil
	}
	if c.g.json {
		return output.JSON(c.env.Stdout, l)
	}
	if !l.HaveSums {
		_, _ = fmt.Fprintf(c.env.Stderr, "This controller (version %s) has no SHA256SUMS yet. Download SHA256SUMS and the CrspAgent-* files of the release, then run `crsp machine agent-installers import FILE...`.\n", l.Version)
		return nil
	}
	var rows [][]string
	for _, in := range l.Installers {
		state := "missing"
		if in.Stored {
			state = "stored"
		}
		rows = append(rows, []string{in.Name, string(in.Os), string(in.Arch), state})
	}
	output.Table(c.env.Stdout, output.MaxWidth, []string{"NAME", "OS", "CPU", "STATE"}, rows)
	return nil
}
