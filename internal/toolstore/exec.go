package toolstore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Fetcher downloads a file and reads the checksum a project publishes. The real one is HTTPFetcher
// (fetch.go); tests use FakeFetcher and never touch the network.
type Fetcher interface {
	// Download saves url to file and returns the final URL after redirects.
	Download(ctx context.Context, url, file string) (finalURL string, err error)
	// Text reads a small text file, such as a .sha256 file.
	Text(ctx context.Context, url string) (string, error)
}

// Runner runs a manager command with an environment. The real one is ExecRunner (fetch.go).
type Runner interface {
	Run(ctx context.Context, argv []string, env map[string]string, dir string) error
}

// ExecInstaller carries out a Recipe: it downloads and checks, unpacks and runs the tool's own manager.
type ExecInstaller struct {
	Fetch Fetcher
	Run   Runner
}

var _ Installer = (*ExecInstaller)(nil)

// Install runs the steps in order into dest. A download whose project publishes a checksum is checked and
// refused on a mismatch; one that publishes none is listed as unchecked in the result.
func (e *ExecInstaller) Install(ctx context.Context, r Recipe, dest string) (Result, error) {
	res := Result{Manager: r.Manager}
	for _, st := range r.Steps {
		switch st.Kind {
		case StepDownload:
			file := filepath.Join(dest, st.To)
			final, err := e.Fetch.Download(ctx, st.URL, file)
			if err != nil {
				return res, fmt.Errorf("download %s: %w", st.URL, err)
			}
			if st.ChecksumSuffix == "" {
				res.Unchecked = append(res.Unchecked, st.To)
			} else {
				sum, err := e.Fetch.Text(ctx, final+st.ChecksumSuffix)
				if err != nil {
					return res, fmt.Errorf("checksum of %s: %w", st.To, err)
				}
				if err := VerifySHA256(file, sum); err != nil {
					_ = os.Remove(file)
					return res, err
				}
			}
			if !strings.HasSuffix(st.To, ".zip") && !strings.HasSuffix(st.To, ".tar.gz") {
				_ = os.Chmod(file, 0o755)
			}
		case StepUnpack:
			file := filepath.Join(dest, st.File)
			if err := Unpack(file, dest, st.StripDirs); err != nil {
				return res, err
			}
			_ = os.Remove(file)
		case StepRun:
			if len(st.Argv) == 0 {
				return res, errors.New("empty command")
			}
			if err := e.Run.Run(ctx, st.Argv, st.Env, dest); err != nil {
				return res, fmt.Errorf("%s: %w", st.Argv[0], err)
			}
		default:
			return res, fmt.Errorf("unknown step %q", st.Kind)
		}
	}
	return res, nil
}
