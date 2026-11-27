package mirror

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/audit"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry"
)

// View is the mirror settings with what the console shows beside them.
type View struct {
	registry.MirrorSettings
	// UsedBytes is what the mirror folder holds; the folder is the setting, or the default (R5.64).
	UsedBytes int64
	// Path is where engines reach the mirror on the controller's listener.
	Path string
}

// Service reads and changes the mirror settings: it validates, saves, applies
// them to the running proxies and writes the audit entry. The API serves it.
type Service struct {
	Store  registry.MirrorSettingsStore
	Mirror *Mirror
	Audit  audit.Recorder
	// DataDir gives the default folder.
	DataDir string
}

// Load applies the saved settings at start-up.
func (s *Service) Load(ctx context.Context) error {
	cur, err := s.Store.MirrorSettings(ctx)
	if err != nil {
		return err
	}
	return s.Mirror.Apply(ctx, cur)
}

// Get returns the settings in use and the bytes the mirror folder holds.
func (s *Service) Get(ctx context.Context) (View, error) {
	cur, err := s.Store.MirrorSettings(ctx)
	if err != nil {
		return View{}, err
	}
	return s.view(cur), nil
}

func (s *Service) view(cur registry.MirrorSettings) View {
	cur.Folder = Folder(cur, s.DataDir)
	if cur.Upstreams == nil {
		cur.Upstreams = []string{}
	}
	return View{MirrorSettings: cur, UsedBytes: DirSize(cur.Folder), Path: strings.TrimSuffix(PathPrefix, "/")}
}

// Put validates and saves the settings, then applies them. A setting that cannot
// run is refused before anything is saved (ErrInvalid), so the saved settings
// are always ones that run.
func (s *Service) Put(ctx context.Context, actor audit.Actor, in registry.MirrorSettings) (View, error) {
	in, err := Validate(in)
	if err != nil {
		return View{}, err
	}
	// A folder equal to the default is saved as empty: it then follows the data directory.
	if in.Folder == Folder(registry.MirrorSettings{}, s.DataDir) {
		in.Folder = ""
	}
	if err := s.Mirror.Apply(ctx, in); err != nil {
		return View{}, err
	}
	if err := s.Store.PutMirrorSettings(ctx, in, actor.Login); err != nil {
		return View{}, err
	}
	if s.Audit != nil {
		d := map[string]any{"enabled": in.Enabled, "upstreams": strings.Join(in.Upstreams, ","), "budget_bytes": in.BudgetBytes,
			"tag_recheck_minutes": int64(in.TagRecheck / time.Minute)}
		if err := s.Audit.Record(ctx, actor, "registry.mirror.update", audit.Target{Type: "registry_mirror", ID: "1"}, d); err != nil {
			return View{}, fmt.Errorf("audit the mirror settings: %w", err)
		}
	}
	return s.view(in), nil
}
