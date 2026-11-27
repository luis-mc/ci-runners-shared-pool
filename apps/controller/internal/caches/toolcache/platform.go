package toolcache

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend/sys"
)

// SharedMode is the mode of the root and of every folder made in it on Linux
// and macOS: world-writable with the sticky bit (caches R5.18).
const SharedMode = os.ModeDir | os.ModeSticky | 0o777

// RunnersGroup is the local group every runner account belongs to on Windows
// (runners R5.14).
const RunnersGroup = "crsp-runners"

// Platform holds what differs by operating system.
type Platform interface {
	// Secure makes the root and sets who may write it.
	Secure(ctx context.Context, fsys FS, root string) error
	// Folder sets the mode of a folder the store made inside the root.
	Folder(fsys FS, dir string) error
	// Verify says why the root is not safe to share, or nil.
	Verify(fsys FS, root string) error
}

// DefaultRoot is the host folder for a system (goos is a runtime.GOOS value).
func DefaultRoot(goos string) string {
	switch goos {
	case "windows":
		return `C:\ProgramData\crsp\tool-cache`
	case "darwin":
		return "/Users/Shared/crsp/tool-cache"
	default:
		return "/var/lib/crsp/tool-cache"
	}
}

// PlatformFor picks the Platform of a system. run is used on Windows only.
func PlatformFor(goos string, run sys.Commands) Platform {
	if goos == "windows" {
		return Windows{Run: run}
	}
	return Unix{} // Linux and macOS
}

// Unix is the Linux and macOS platform: mode 1777 (caches R5.18).
type Unix struct{}

func (Unix) Secure(_ context.Context, f FS, root string) error {
	if err := f.MkdirAll(root, 0o755); err != nil {
		return err
	}
	return f.Chmod(root, SharedMode&^os.ModeDir)
}

func (Unix) Folder(f FS, dir string) error { return f.Chmod(dir, SharedMode&^os.ModeDir) }

func (Unix) Verify(f FS, root string) error {
	fi, err := f.Stat(root)
	if err != nil {
		return err
	}
	return checkUnixMode(fi.Mode())
}

func checkUnixMode(m fs.FileMode) error {
	if !m.IsDir() {
		return errors.New("the tool cache root is not a folder")
	}
	if m&os.ModeSticky == 0 || m.Perm() != 0o777 {
		return fmt.Errorf("the tool cache root has mode %v, not the shared mode with the sticky bit (1777)", m)
	}
	return nil
}

// Windows is the Windows platform: an access list (caches R5.19). The runner
// accounts may create and read; only the creator may change or delete.
type Windows struct{ Run sys.Commands }

// ACLPlan is the `icacls` call that sets the access list.
func (Windows) ACLPlan(root string) sys.Command {
	return sys.Command{Name: "icacls", Args: []string{
		root, "/inheritance:r",
		"/grant", "SYSTEM:(OI)(CI)(F)",
		"/grant", "Administrators:(OI)(CI)(F)",
		"/grant", "CREATOR OWNER:(OI)(CI)(IO)(F)",
		"/grant", RunnersGroup + ":(OI)(CI)(RX,WD,AD)",
	}}
}

func (w Windows) Secure(ctx context.Context, f FS, root string) error {
	if err := f.MkdirAll(root, 0o755); err != nil {
		return err
	}
	if w.Run == nil {
		return errors.New("no way to run icacls")
	}
	res, err := w.Run.Run(ctx, w.ACLPlan(root))
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("icacls exited %d: %s", res.ExitCode, res.Stderr)
	}
	return nil
}

// Folder does nothing: folders inherit the access list.
func (Windows) Folder(FS, string) error { return nil }

// Verify only checks that the root is a folder; the list is the agent's.
func (Windows) Verify(f FS, root string) error {
	fi, err := f.Stat(root)
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return errors.New("the tool cache root is not a folder")
	}
	return nil
}
