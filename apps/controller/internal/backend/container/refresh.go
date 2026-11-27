package container

import (
	"context"
	"fmt"
	"strings"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runners"
)

// RefreshResult says what a refresh did, in words a console can show.
type RefreshResult struct {
	// Swapped is true when the pool now runs the new image.
	Swapped bool
	// From and To are the image keys.
	From, To string
	// Message is the truthful one-line result.
	Message string
}

// Refresh moves the pool container of a repository to the image of spec:
// build aside, drain, swap (runners R5.35, R6.4).
//
//  1. Build the new image while the old one keeps serving jobs. A failed build
//     keeps the old image and the pool as they are: "the rebuild failed: ...".
//  2. Stop taking new runners (ErrDraining) and wait until no job runs. The
//     idle check runs again right before the swap, never earlier (drain.Act).
//     The wait has no end except the drain's own timeout, and a timeout swaps
//     nothing: no job is ever cut.
//  3. Swap: stop and remove the old container, start the new one. When the new
//     one cannot start, the old image is started again and the result says so.
//
// A pool container that is not running (Exited) is drained at once: no wait,
// no drain file, no poll (handover lesson 27).
func (b *Backend) Refresh(ctx context.Context, owner, repo string, spec backend.ImageSpec, drain runners.Drain) (RefreshResult, error) {
	pool := PoolName(owner, repo)
	res := RefreshResult{To: b.KeyFor(spec)}

	img, err := b.PrepareImage(ctx, spec)
	if err != nil {
		res.Message = "the rebuild failed: " + err.Error()
		return res, fmt.Errorf("the rebuild failed: %w", err)
	}
	exists, running, oldKey, err := b.poolState(ctx, pool)
	if err != nil {
		return res, err
	}
	res.From = oldKey
	if exists && oldKey == res.To {
		res.Message = "the pool already runs image " + res.To
		return res, nil
	}
	if !exists {
		// Nothing runs: the next Start creates the pool from the new image.
		res.Message = "no pool container yet; the next runner starts on image " + res.To
		return res, nil
	}

	if !running {
		// An Exited pool (after a Docker restart) has no supervisor that could
		// answer a drain and no job to wait for: it is drained at once
		// (handover lesson 27). Hold the lock so no runner starts mid-swap.
		b.mu.Lock()
		defer b.mu.Unlock()
		if drain.OnStep != nil {
			drain.OnStep("drain: the pool container is not running; treated as drained")
		}
		if err := b.swap(ctx, owner, repo, pool, oldKey, img.ID, &res); err != nil {
			if res.Message == "" {
				res.Message = "the pool was not changed: " + err.Error()
			}
			return res, err
		}
		return res, nil
	}

	b.mu.Lock()
	b.draining[pool] = true
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		delete(b.draining, pool)
		b.mu.Unlock()
	}()

	drain.Idle = func(ctx context.Context) (bool, string, error) {
		busy, err := b.busy(ctx, pool)
		if busy {
			return false, pool, err
		}
		return true, "", err
	}
	err = drain.Act(ctx, func(ctx context.Context) error {
		// Hold the lock so no runner starts between this check and the swap.
		b.mu.Lock()
		defer b.mu.Unlock()
		busy, err := b.busy(ctx, pool)
		if err != nil {
			return err
		}
		if busy {
			return fmt.Errorf("a job started in %s; nothing was stopped", pool)
		}
		return b.swap(ctx, owner, repo, pool, oldKey, img.ID, &res)
	})
	if err != nil {
		if res.Message == "" {
			res.Message = "the pool was not changed: " + err.Error()
		}
		return res, err
	}
	return res, nil
}

func (b *Backend) swap(ctx context.Context, owner, repo, pool, oldKey, newTag string, res *RefreshResult) error {
	uid, err := b.identity(ctx, pool)
	if err != nil {
		return err
	}
	base := PoolSpec{Owner: owner, Repo: repo, Limits: b.cfg.Limits, KVM: b.cfg.KVM, CrspHostPath: b.cfg.CrspHostPath, UID: uid}
	grace := fmt.Sprintf("%d", int(b.cfg.StopGrace.Seconds()))
	if err := b.step(ctx, "pool stop", []string{"stop", "-t", grace, pool}, ""); err != nil {
		return err
	}
	if err := b.step(ctx, "pool remove", []string{"rm", pool}, ""); err != nil {
		return err
	}
	b.unpin(pool)
	nu := base
	nu.ImageTag, nu.ImageKey = newTag, keyOf(newTag)
	err = b.pin(&nu)
	var args []string
	if err == nil {
		args, err = RunArgs(nu)
	}
	if err == nil {
		err = b.step(ctx, "pool create", args, "")
	}
	if err != nil {
		b.unpin(pool)
	}
	if err == nil {
		res.Swapped = true
		res.Message = "the pool now runs image " + nu.ImageKey
		b.log("swap", res.Message)
		return nil
	}
	// The new container did not start. Put the old image back.
	old := base
	old.ImageTag, old.ImageKey = ImageTag(oldKey), oldKey
	oerr := b.pin(&old)
	var oargs []string
	if oerr == nil {
		oargs, oerr = RunArgs(old)
	}
	if oerr == nil {
		oerr = b.step(ctx, "pool create", oargs, "")
	}
	if oerr != nil {
		b.unpin(pool)
		res.Message = "the swap failed and the old image did not start again: " + strings.TrimSpace(err.Error()) + "; " + oerr.Error()
		return fmt.Errorf("%s", res.Message)
	}
	res.Message = "the swap failed, the old image runs again: " + err.Error()
	return fmt.Errorf("%s", res.Message)
}
