package core

import (
	"context"
	"fmt"
	"strings"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runnercache"
)

// OSDisk is registry.DiskInfo over the volume of a folder, the same reading
// the free-disk guard of the runner hooks uses.
type OSDisk struct{}

var _ registry.DiskInfo = OSDisk{}

// Free implements registry.DiskInfo.
func (OSDisk) Free(path string) (int64, error) {
	d, err := runnercache.OSDisk(path)
	return d.Free, err
}

// DiskGate is the PushGate of R5.8: a push is refused when the volume of the
// registry folder has less free space than the free-disk guard allows
// (runnercache.Guard, 10 percent unless set), or when the kind of the image is
// over its budget. The machine keeps its local image.
type DiskGate struct {
	// Folder is the registry folder whose volume is read.
	Folder string
	// MinFreePct is the guard threshold; 0 takes runnercache.DefaultMinFreePct.
	MinFreePct int
	// Disk reads a volume; nil reads the real one.
	Disk func(path string) (runnercache.Disk, error)
	// OverBudget says whether a kind has no room left (REG6's budgets); nil
	// means no budget check.
	OverBudget func(ctx context.Context, kind registry.Kind) (bool, error)
	// Refused is told of each refusal, so REG6 can open an over_budget
	// recommendation (retention.Service.PushRefused). Nil is skipped.
	Refused func(ctx context.Context, kind registry.Kind)
}

var _ PushGate = (*DiskGate)(nil)

// kindOfStaging reads the kind from crsp/incoming/<machine>/std/... or
// .../custom/...; "" when the name does not say.
func kindOfStaging(name string) registry.Kind {
	rest, ok := strings.CutPrefix(name, registry.IncomingPrefix)
	if !ok {
		return ""
	}
	_, rest, _ = strings.Cut(rest, "/")
	switch {
	case strings.HasPrefix(rest, "std/"):
		return registry.KindStandard
	case strings.HasPrefix(rest, "custom/"):
		return registry.KindCustom
	}
	return ""
}

// AllowPush implements PushGate.
func (g *DiskGate) AllowPush(ctx context.Context, name string) error {
	kind := kindOfStaging(name)
	refuse := func(format string, a ...any) error {
		if g.Refused != nil && kind != "" {
			g.Refused(ctx, kind)
		}
		return fmt.Errorf("%w: %s", registry.ErrOverBudget, fmt.Sprintf(format, a...))
	}
	read := g.Disk
	if read == nil {
		read = runnercache.OSDisk
	}
	d, err := read(g.Folder)
	if err != nil {
		return fmt.Errorf("read the disk of the registry folder: %w", err)
	}
	if held, why := runnercache.NewGuard(g.MinFreePct).Check(d); held {
		return refuse("%s", why)
	}
	if g.OverBudget != nil && kind != "" {
		over, err := g.OverBudget(ctx, kind)
		if err != nil {
			return err
		}
		if over {
			return refuse("the %s images are over their budget", kind)
		}
	}
	return nil
}
