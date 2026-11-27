package cli

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api/client"
)

// maxKeyFile bounds the key file the command reads; the controller refuses more.
const maxKeyFile = 16 << 10

// cmdApp is `crsp app` (github-integration R5.7).
func cmdApp() *command {
	return &command{
		name: "app", summary: "the GitHub App of this controller",
		subs: []*command{
			{name: "rotate-key", summary: "replace the App private key (owner only)", args: "--file PATH|- [--yes]",
				flags: func(c *call, fs *flag.FlagSet) {
					yesFlag(fs)
					fs.String("file", "", "the private key file from GitHub, or - to read it from standard input")
				},
				run: func(c *call, _ []string) error { return c.rotateKey() }},
		},
	}
}

func (c *call) rotateKey() error {
	path := c.fs.Lookup("file").Value.String()
	if path == "" {
		return fail(ExitUsage, "Usage: crsp app rotate-key --file PATH|- [--yes]")
	}
	r := c.env.Stdin
	if path != "-" {
		f, err := os.Open(path)
		if err != nil {
			return fail(ExitUsage, "Could not read the key file %s. Nothing changed.", path)
		}
		defer func() { _ = f.Close() }()
		r = f
	}
	raw, err := io.ReadAll(io.LimitReader(r, maxKeyFile+1))
	if err != nil || len(raw) == 0 || len(raw) > maxKeyFile {
		return fail(ExitUsage, "That is not a GitHub App private key file. Nothing changed.")
	}
	if err := c.confirm("This replaces the GitHub App private key.", []string{
		"the controller first checks the new key with GitHub for the same App",
		"if GitHub refuses it, the old key stays and keeps working",
		"the key is not shown, logged or written to the audit log",
	}, c.yesFlag()); err != nil {
		return err
	}
	s, err := c.open()
	if err != nil {
		return err
	}
	res, err := s.client.ReplaceAppPrivateKeyWithResponse(c.env.Context, client.AppKeyUpload{Pem: string(raw)})
	if err != nil {
		return s.transport(err)
	}
	if err := apiError(res.StatusCode(), res.Body); err != nil {
		return err
	}
	_, _ = fmt.Fprintln(c.env.Stderr, "The new App private key is in use. The old one is gone from this controller.")
	return nil
}
