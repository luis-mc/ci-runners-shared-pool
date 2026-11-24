package container

import (
	"context"
	"fmt"
	"runtime"

	"github.com/luis-mc/ci-runners-shared-pool/internal/backend/sys"
	"github.com/luis-mc/ci-runners-shared-pool/internal/runnercache"
)

// identity returns the persistent uid/gid of a pool container (runners R5.72).
// The same pool name gets the same id after a restart or a new container. With
// no id source it is 0, and RunArgs uses JobUID.
func (b *Backend) identity(ctx context.Context, pool string) (int, error) {
	if b.cfg.IDs == nil {
		return 0, nil
	}
	id, err := b.cfg.IDs.ID(ctx, pool)
	if err != nil {
		return 0, fmt.Errorf("pool identity: %w", err)
	}
	return id, nil
}

// ownLane gives the lane folders to the pool's uid/gid, so caches and work
// files keep their owner when the container is made again (runners R5.72).
// Without an id source nothing is changed.
func (b *Backend) ownLane(ctx context.Context, uid int, lane runnercache.LaneDirs) error {
	if b.cfg.IDs == nil || uid == 0 {
		return nil
	}
	// OPS89: a Windows host has no chown and NTFS has no uid; the bind mount
	// through the engine (Docker Desktop/WSL2) is owned as the engine decides.
	if b.hostOS() == "windows" {
		return nil
	}
	own := fmt.Sprintf("%d:%d", uid, uid)
	for _, dir := range lane.Folders() {
		if dir == "" {
			continue
		}
		res, err := b.d.Cmd.Run(ctx, sys.Command{Name: "chown", Args: []string{"-R", own, dir}})
		if err != nil {
			return fmt.Errorf("own lane %s: %w", dir, err)
		}
		if res.ExitCode != 0 {
			return fmt.Errorf("own lane %s: chown exited %d: %s", dir, res.ExitCode, b.redact(res.Stderr))
		}
	}
	return nil
}

// hostOS is the system the agent runs on; tests set Config.HostOS.
func (b *Backend) hostOS() string {
	if b.cfg.HostOS != "" {
		return b.cfg.HostOS
	}
	return runtime.GOOS
}
