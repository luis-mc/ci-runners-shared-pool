package windows

import (
	"context"
	"fmt"
	"strings"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend/sys"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runners"
)

// The runner listener reads the contents of every folder above its own, up to
// the drive, and stops with "Permission to read the directory contents is
// required for ... and each directory up the hierarchy" when one is denied.
// The data folder of crsp belongs to SYSTEM and Administrators only, and the
// runners folder inherits that, so a runner service started as its own account
// "started" and stopped again in the same second (runners R5.156).

// listRightDirs are the folders above a runner folder that the runner accounts
// must be able to list: the runners root and the folder that holds it. The
// folders above those (C:\ProgramData, C:\) are readable by every user.
func listRightDirs(root string) []string {
	root = strings.TrimRight(root, `\`)
	dirs := []string{root}
	if i := strings.LastIndex(root, `\`); i > 2 {
		dirs = append(dirs, root[:i])
	}
	return dirs
}

// ListRightPlan is one icacls call per folder: the runner group may list the
// folder and go through it, on that folder only. It is not inherited, so no
// runner reaches another runner's files or the controller's data from it.
func ListRightPlan(root string) []sys.Command {
	var cmds []sys.Command
	for _, d := range listRightDirs(root) {
		cmds = append(cmds, sys.Command{Name: "icacls", Args: []string{d, "/grant", runners.RunnersGroup + ":(RX)", "/C"}})
	}
	return cmds
}

// ensureListRight gives the runner group the right to list the runners folder
// and the folder above it. It is idempotent, and it needs the group to exist.
func (b *Backend) ensureListRight(ctx context.Context) error {
	for _, c := range ListRightPlan(b.cfg.Root) {
		res, err := b.d.Cmd.Run(ctx, c)
		if err == nil && res.ExitCode != 0 {
			err = fmt.Errorf("%s exited %d: %s", c.Name, res.ExitCode, firstLineOf(b.d, res.Stderr+res.Stdout))
		}
		if err != nil {
			return fmt.Errorf("let the runner accounts list %s: %w", c.Args[0], err)
		}
	}
	return nil
}
