// Package records is the controller's memory of the image registry (docs/features/registry R5.29 to R5.36, REG4):
// image rows, layer accounting, pull and push records with per-day totals, the Locator that answers EnsureImage,
// and the read models the API serves. It reads and writes only through the generated queries of
// queries/registry_records.sql (and reads the rows of the other tasks' queries); it never touches the registry
// storage.
package records

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/store"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/store/sqlcgen"
)

// Retention of the records (R5.30).
const (
	PullRetention  = 30 * 24 * time.Hour
	DailyRetention = 180 * 24 * time.Hour
	// pruneEvery is how often the recorder prunes while reports arrive.
	pruneEvery = time.Hour
	// timeLayout is the stored form of a time: UTC, fixed width, so strings sort like times.
	timeLayout = "2006-01-02T15:04:05.000Z"
	dayLayout  = "2006-01-02"
)

// MachineInfo is what the machines area knows about a machine and an image.
type MachineInfo struct {
	Name string
	// Holds says the machine's engine has the image now (agent inventory).
	Holds bool
	// InUseByRunner says a runner of the machine uses the image.
	InUseByRunner bool
}

// MachineDirectory names machines and says what they hold. Nil gives the machine id as the name and false for the
// rest.
type MachineDirectory interface {
	Machine(ctx context.Context, machineID string, img registry.Image) MachineInfo
}

// LayerSource lists the layers of a manifest in the registry storage (REG1 implements it with the Distribution
// storage). The promoted hook of REG4 reads it.
type LayerSource interface {
	Layers(ctx context.Context, name, manifestDigest string) ([]registry.Layer, error)
}

// Config is what the wiring gives the Service. Every field is optional.
type Config struct {
	// Now is the clock; nil uses time.Now.
	Now func() time.Time
	// Loc is the controller's local zone, for the day of a daily total; nil uses time.Local.
	Loc *time.Location
	// Host is host:port of the registry on the controller's HTTPS listener.
	Host string
	// DefaultFolder is shown when the settings keep no folder (<data_dir>/registry).
	DefaultFolder string
	// Running says whether the registry runs and, when not, why in plain words. Nil: it runs when Host is set.
	Running  func(ctx context.Context) (bool, string)
	Machines MachineDirectory
	Logger   *slog.Logger
}

// Service keeps the records and answers the reads.
type Service struct {
	q         *sqlcgen.Queries
	cfg       Config
	lastPrune atomic.Int64 // unix seconds
}

// New makes the Service over the generated queries (store.Store.RegistryQueries).
func New(q *sqlcgen.Queries, cfg Config) *Service {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Loc == nil {
		cfg.Loc = time.Local
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Service{q: q, cfg: cfg}
}

var (
	_ registry.Locator  = (*Service)(nil)
	_ registry.Recorder = (*Service)(nil)
)

func (s *Service) now() time.Time { return s.cfg.Now() }

// day is the controller-local calendar day of t.
func (s *Service) day(t time.Time) string { return t.In(s.cfg.Loc).Format(dayLayout) }

func stamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(timeLayout)
}

func parseStamp(v string) time.Time {
	if v == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, v)
	if err != nil {
		return time.Time{}
	}
	return t
}

func noRows(err error) bool { return store.IsNoRows(err) }

// toImage turns a row into the contract type.
func toImage(r sqlcgen.RegistryImage) registry.Image {
	return registry.Image{
		ID: r.ID, Kind: registry.Kind(r.Kind), Name: r.Name, Key: registry.Key(r.ImageTag), Profile: r.ProfileID,
		Arch: r.Arch, State: registry.State(r.State), Manifest: r.ManifestDigest, StackHash: r.StackHash,
		BaseName: r.BaseName, BaseDigest: r.BaseDigest, SourceRepository: r.SourceRepository, Commit: r.CommitSha,
		Branch: r.Branch, PushedBy: r.PushedBy, PushedAt: parseStamp(r.PushedAt), PromotedAt: parseStamp(r.PromotedAt),
		Replaces: r.ReplacesID, ReplacedBy: r.ReplacedByID, KeepUntil: parseStamp(r.KeepUntil),
		BranchGoneAt: parseStamp(r.BranchGoneAt), SizeBytes: r.SizeBytes, UniqueBytes: r.UniqueBytes,
		LastPullAt: parseStamp(r.LastPullAt), Scan: registry.ScanState(r.ScanState),
	}
}

// Image reads one image by id, or registry.ErrNotFound.
func (s *Service) Image(ctx context.Context, id string) (registry.Image, error) {
	r, err := s.q.GetRegistryImage(ctx, id)
	if noRows(err) {
		return registry.Image{}, registry.ErrNotFound
	}
	if err != nil {
		return registry.Image{}, err
	}
	return toImage(r), nil
}

// ImageByKey finds the promoted image of key k, or registry.ErrNotFound. The contract has no arch here, so it
// answers the newest promoted image of the key under any arch; the Locator asks with the arch.
func (s *Service) ImageByKey(ctx context.Context, k registry.Key) (registry.Image, error) {
	r, err := s.q.GetRegistryImageByTag(ctx, k.String())
	if noRows(err) {
		return registry.Image{}, registry.ErrNotFound
	}
	if err != nil {
		return registry.Image{}, err
	}
	return toImage(r), nil
}

// Layers lists the layers of an image in order with the kind each is charged to.
func (s *Service) Layers(ctx context.Context, imageID string) ([]registry.Layer, error) {
	rows, err := s.q.ListRegistryImageLayers(ctx, imageID)
	if err != nil {
		return nil, err
	}
	out := make([]registry.Layer, 0, len(rows))
	for _, r := range rows {
		out = append(out, registry.Layer{Digest: r.Digest, SizeBytes: r.SizeBytes, MediaType: r.MediaType,
			ChargedTo: registry.Kind(r.ChargedTo), Images: []string{imageID}})
	}
	return out, nil
}

// errBadRecord is wrapped by the refusals of a record that cannot be kept.
var errBadRecord = errors.New("registry records: the record is not valid")
