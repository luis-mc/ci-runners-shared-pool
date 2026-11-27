package container

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
)

var keyRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)

// stageBuild writes the embedded image files to `<BuildRoot>/<key>` and puts
// the crsp binary beside the base Dockerfile, which copies it (runners R5.48).
// The folder is the build context and is removed by the caller after the build.
// An old folder of the same key is replaced, so a half-written one is never used.
func (b *Backend) stageBuild(ctx context.Context, key, arch string) (string, error) {
	if !keyRe.MatchString(key) {
		return "", fmt.Errorf("image key %q cannot name a build folder", key)
	}
	root := b.cfg.BuildRoot
	if root == "" {
		root = filepath.Join(os.TempDir(), "crsp-image-build")
	}
	dir := filepath.Join(root, key)
	if err := os.RemoveAll(dir); err != nil {
		return "", fmt.Errorf("clear the build folder: %w", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("make the build folder: %w", err)
	}
	fail := func(err error) (string, error) {
		_ = os.RemoveAll(dir)
		return "", err
	}
	err := fs.WalkDir(b.cfg.Assets, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		dst := filepath.Join(dir, filepath.FromSlash(p))
		if d.IsDir() {
			return os.MkdirAll(dst, 0o700)
		}
		data, err := fs.ReadFile(b.cfg.Assets, p)
		if err != nil {
			return err
		}
		mode := os.FileMode(0o600)
		if info, err := d.Info(); err == nil && info.Mode()&0o111 != 0 {
			mode = 0o700
		}
		return os.WriteFile(dst, data, mode)
	})
	if err != nil {
		return fail(fmt.Errorf("write the image files: %w", err))
	}
	// The binary is the image's own OS and architecture (OPS60, registry R5.82):
	// the agent's file on a Linux host of the same arch, else the Linux release
	// asset of the same version. A file of any other kind fails the build here,
	// before docker starts, with the reason.
	src := b.cfg.CrspHostPath
	if src == "" && b.cfg.LinuxCrsp != nil {
		if src, err = b.cfg.LinuxCrsp(ctx, arch); err != nil {
			return fail(err)
		}
	}
	if src == "" {
		if src, err = os.Executable(); err != nil {
			return fail(fmt.Errorf("find the crsp binary for the image: %w", err))
		}
	}
	if err := CheckLinuxBinary(src, arch); err != nil {
		return fail(err)
	}
	if err := copyFile(src, filepath.Join(dir, "linux-base", "crsp")); err != nil {
		return fail(fmt.Errorf("copy the crsp binary into the build folder: %w", err))
	}
	return dir, nil
}

func copyFile(src, dst string) (err error) {
	in, err := os.Open(src) //nolint:gosec // the agent's own binary
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o700) //nolint:gosec // path built from the build folder
	if err != nil {
		return err
	}
	defer func() {
		if cerr := out.Close(); err == nil {
			err = cerr
		}
	}()
	_, err = io.Copy(out, in)
	return err
}
