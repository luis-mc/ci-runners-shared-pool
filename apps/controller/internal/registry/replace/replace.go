// Package replace runs replace plans and rollbacks of registry images
// (registry R5.53 to R5.58). A plan moves the pointer of each named repository
// from the old image to the new one, asks the machines that held the old image
// to pull the new one, and keeps the old image until keep_until.
package replace

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry"
)

// Errors the API maps: ErrInvalid to 400, ErrConflict to 409. registry.ErrNotFound is 404.
var (
	ErrInvalid  = errors.New("replace: invalid request")
	ErrConflict = errors.New("replace: conflict")
)

func conflict(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrConflict, fmt.Sprintf(format, a...))
}

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, a...))
}

// Message is the part of a replace error that is safe and useful to show.
func Message(err error) string {
	for _, e := range []error{ErrConflict, ErrInvalid} {
		if errors.Is(err, e) {
			return strings.TrimPrefix(err.Error(), e.Error()+": ")
		}
	}
	return err.Error()
}

// Plans keeps the plans (registry_replace_plans, R5.58) and the two image
// state changes a plan makes. *store.Store implements it.
type Plans interface {
	InsertReplacePlan(ctx context.Context, p registry.ReplacePlan) error
	GetReplacePlan(ctx context.Context, id string) (registry.ReplacePlan, error)
	SaveReplacePlan(ctx context.Context, p registry.ReplacePlan) error
	MarkReplacePlanRolledBack(ctx context.Context, id, by string, at time.Time) (bool, error)
	ActiveReplacePlanForImage(ctx context.Context, imageID string) (registry.ReplacePlan, bool, error)
	ReplacePlansByState(ctx context.Context, state registry.PlanState) ([]registry.ReplacePlan, error)
	SupersedeRegistryImage(ctx context.Context, oldID, newID string, keepUntil time.Time) (bool, error)
	RestoreRegistryImage(ctx context.Context, id string) (bool, error)
}

// Warmer asks a machine to pull an image now (R5.55). It returns when the ask
// is made, not when the pull ends. A machine that cannot be asked pulls on its
// next job, so a Warm error never fails a plan.
type Warmer interface {
	Warm(ctx context.Context, machineID string, img registry.Image) error
}

// Holders names the machines that hold an image (REG4 keeps the pull records).
type Holders interface {
	MachinesHolding(ctx context.Context, imageID string) ([]string, error)
}

// Config wires the service. Images, Pointers and Plans are required.
type Config struct {
	Images   registry.ImageStore
	Pointers registry.PointerStore
	Plans    Plans
	Settings registry.SettingsStore // default keep days; nil uses the default
	Stacks   registry.StackSource   // stack files for compare; nil shows none
	Warmer   Warmer                 // nil warms nobody
	Holders  Holders                // nil: no machine is known to hold it
	// Pulls7 counts the pulls of an image in 7 days for the API summary (REG4); nil gives 0.
	Pulls7 func(ctx context.Context, imageID string) int
	Now    func() time.Time
	Logger *slog.Logger
}

// Service implements registry.Replacer.
type Service struct {
	cfg  Config
	mu   sync.Mutex
	jobs map[string]*job
}

type job struct {
	stop chan struct{}
	done chan struct{}
	once sync.Once
}

var _ registry.Replacer = (*Service)(nil)

// New returns a service.
func New(cfg Config) *Service {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Service{cfg: cfg, jobs: map[string]*job{}}
}

func newID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return "rpl_" + hex.EncodeToString(b[:])
}

// ImageView is an image with its pull count, for the API.
type ImageView struct {
	registry.Image
	Pulls7 int
}

// View reads one image.
func (s *Service) View(ctx context.Context, id string) (ImageView, error) {
	img, err := s.cfg.Images.Image(ctx, id)
	if err != nil {
		return ImageView{}, err
	}
	v := ImageView{Image: img}
	if s.cfg.Pulls7 != nil {
		v.Pulls7 = s.cfg.Pulls7(ctx, id)
	}
	return v, nil
}

// StackChange is one stack file that differs between the two images.
type StackChange struct {
	Path   string
	Change string // added, removed or changed
	Before string // sha256 of the old file
	After  string // sha256 of the new file
}

// Comparison is what R5.53 shows: the new image against the one it replaces.
type Comparison struct {
	New, Old        ImageView
	StackFiles      []StackChange
	BaseChanged     bool
	SharedBytes     int64
	FreesBytes      int64
	Repositories    []string
	KeepDaysDefault int
}

// KeepDaysDefault is the days an old image is kept when a plan names none.
func (s *Service) KeepDaysDefault(ctx context.Context) int { return s.keepDaysDefault(ctx) }

func (s *Service) keepDaysDefault(ctx context.Context) int {
	if s.cfg.Settings != nil {
		if st, err := s.cfg.Settings.Settings(ctx); err == nil {
			return st.KeepOldDays
		}
	}
	return registry.DefaultKeepOldDays
}

// oldOf finds the image newImg replaces. An image that replaces nothing, or
// whose predecessor is gone, is registry.ErrNotFound (404).
func (s *Service) oldOf(ctx context.Context, newImg registry.Image) (registry.Image, error) {
	if newImg.Replaces == "" {
		return registry.Image{}, registry.ErrNotFound
	}
	old, err := s.cfg.Images.Image(ctx, newImg.Replaces)
	if err != nil {
		return registry.Image{}, err
	}
	if old.State == registry.StateDeleted {
		return registry.Image{}, registry.ErrNotFound
	}
	return old, nil
}

// repositoriesOf splits the repositories that point at old into those a plan
// for newImg moves and those that keep old. A custom image moves only the
// pointer of the repository it was built for (D7); a standard image moves all.
func (s *Service) repositoriesOf(ctx context.Context, old, newImg registry.Image) (moving, staying []string, err error) {
	ptrs, err := s.cfg.Pointers.PointersTo(ctx, old.ID)
	if err != nil {
		return nil, nil, err
	}
	seen := map[string]bool{}
	moving, staying = []string{}, []string{}
	for _, p := range ptrs {
		if seen[p.Repository] {
			continue
		}
		seen[p.Repository] = true
		if newImg.Kind == registry.KindCustom && p.Repository != newImg.SourceRepository {
			staying = append(staying, p.Repository)
		} else {
			moving = append(moving, p.Repository)
		}
	}
	sort.Strings(moving)
	sort.Strings(staying)
	return moving, staying, nil
}

// Compare implements R5.53.
func (s *Service) Compare(ctx context.Context, newImageID string) (Comparison, error) {
	newImg, err := s.cfg.Images.Image(ctx, newImageID)
	if err != nil {
		return Comparison{}, err
	}
	old, err := s.oldOf(ctx, newImg)
	if err != nil {
		return Comparison{}, err
	}
	c := Comparison{
		New: s.view(ctx, newImg), Old: s.view(ctx, old),
		StackFiles:      []StackChange{},
		BaseChanged:     old.BaseDigest != newImg.BaseDigest,
		KeepDaysDefault: s.keepDaysDefault(ctx),
	}
	moving, staying, err := s.repositoriesOf(ctx, old, newImg)
	if err != nil {
		return Comparison{}, err
	}
	c.Repositories = moving
	if c.StackFiles, err = s.stackChanges(ctx, old, newImg); err != nil {
		return Comparison{}, err
	}
	oldLayers, err := s.cfg.Images.Layers(ctx, old.ID)
	if err != nil {
		return Comparison{}, err
	}
	newLayers, err := s.cfg.Images.Layers(ctx, newImg.ID)
	if err != nil {
		return Comparison{}, err
	}
	inNew := map[string]bool{}
	for _, l := range newLayers {
		inNew[l.Digest] = true
	}
	// Bytes freed once the old image goes: layers no other image holds. Another
	// repository that still points at the old image keeps all of it (D7).
	keepsAll := len(staying) > 0
	for _, l := range oldLayers {
		if inNew[l.Digest] {
			c.SharedBytes += l.SizeBytes
			continue
		}
		if !keepsAll && heldOnlyBy(l, old.ID) {
			c.FreesBytes += l.SizeBytes
		}
	}
	return c, nil
}

func heldOnlyBy(l registry.Layer, id string) bool {
	for _, o := range l.Images {
		if o != id {
			return false
		}
	}
	return true
}

func (s *Service) view(ctx context.Context, img registry.Image) ImageView {
	v := ImageView{Image: img}
	if s.cfg.Pulls7 != nil {
		v.Pulls7 = s.cfg.Pulls7(ctx, img.ID)
	}
	return v
}

func (s *Service) stackChanges(ctx context.Context, old, newImg registry.Image) ([]StackChange, error) {
	out := []StackChange{}
	repo := newImg.SourceRepository
	if s.cfg.Stacks == nil || repo == "" || old.Commit == "" || newImg.Commit == "" {
		return out, nil
	}
	before, err := s.cfg.Stacks.StackFiles(ctx, repo, old.Commit)
	if err != nil {
		return nil, fmt.Errorf("stack files at the old commit: %w", err)
	}
	after, err := s.cfg.Stacks.StackFiles(ctx, repo, newImg.Commit)
	if err != nil {
		return nil, fmt.Errorf("stack files at the new commit: %w", err)
	}
	sum := func(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
	bm := map[string]string{}
	for _, f := range before {
		bm[f.Path] = sum(f.Content)
	}
	am := map[string]string{}
	for _, f := range after {
		am[f.Path] = sum(f.Content)
	}
	for p, b := range bm {
		a, ok := am[p]
		switch {
		case !ok:
			out = append(out, StackChange{Path: p, Change: "removed", Before: b})
		case a != b:
			out = append(out, StackChange{Path: p, Change: "changed", Before: b, After: a})
		}
	}
	for p, a := range am {
		if _, ok := bm[p]; !ok {
			out = append(out, StackChange{Path: p, Change: "added", After: a})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// CreatePlan implements R5.54: a draft; nothing changes until Start.
func (s *Service) CreatePlan(ctx context.Context, newImageID string, keepDays int, by string) (registry.ReplacePlan, error) {
	if keepDays < 0 || keepDays > 365 {
		return registry.ReplacePlan{}, invalid("keep days must be between 0 and 365")
	}
	newImg, err := s.cfg.Images.Image(ctx, newImageID)
	if err != nil {
		return registry.ReplacePlan{}, err
	}
	if newImg.State != registry.StateCurrent {
		return registry.ReplacePlan{}, conflict("only a current image can replace another")
	}
	old, err := s.oldOf(ctx, newImg)
	if err != nil {
		return registry.ReplacePlan{}, err
	}
	for _, id := range []string{newImg.ID, old.ID} {
		if p, ok, err := s.cfg.Plans.ActiveReplacePlanForImage(ctx, id); err != nil {
			return registry.ReplacePlan{}, err
		} else if ok {
			return registry.ReplacePlan{}, conflict("plan %s already names this image", p.ID)
		}
	}
	repos, _, err := s.repositoriesOf(ctx, old, newImg)
	if err != nil {
		return registry.ReplacePlan{}, err
	}
	if len(repos) == 0 {
		return registry.ReplacePlan{}, conflict("no repository uses the old image")
	}
	machines := []string{}
	if s.cfg.Holders != nil {
		if machines, err = s.cfg.Holders.MachinesHolding(ctx, old.ID); err != nil {
			return registry.ReplacePlan{}, err
		}
		machines = append([]string{}, machines...)
		sort.Strings(machines)
	}
	p := registry.ReplacePlan{
		ID: newID(), NewImageID: newImg.ID, OldImageID: old.ID, KeepDays: keepDays,
		Repositories: repos, Machines: machines, State: registry.PlanDraft,
		StepsTotal: len(repos) + len(machines), CreatedBy: by,
	}
	if err := s.cfg.Plans.InsertReplacePlan(ctx, p); err != nil {
		return registry.ReplacePlan{}, err
	}
	return s.cfg.Plans.GetReplacePlan(ctx, p.ID)
}

// Plan reads a plan. It survives a reload and a restart (R5.58).
func (s *Service) Plan(ctx context.Context, id string) (registry.ReplacePlan, error) {
	return s.cfg.Plans.GetReplacePlan(ctx, id)
}

// Start implements R5.55. A draft or stopped plan runs from its saved step; the
// call returns at once and the steps go on in the background.
func (s *Service) Start(ctx context.Context, id string) (registry.ReplacePlan, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.cfg.Plans.GetReplacePlan(ctx, id)
	if err != nil {
		return p, err
	}
	if p.State != registry.PlanDraft && p.State != registry.PlanStopped {
		return p, conflict("a plan that is %s cannot start", p.State)
	}
	if _, running := s.jobs[id]; running {
		return p, conflict("the plan is still stopping")
	}
	newImg, err := s.cfg.Images.Image(ctx, p.NewImageID)
	if err != nil {
		return p, err
	}
	if newImg.State != registry.StateCurrent {
		return p, conflict("the new image is no longer current")
	}
	p.State, p.Error = registry.PlanRunning, ""
	if p.StartedAt.IsZero() {
		p.StartedAt = s.cfg.Now()
	}
	if err := s.cfg.Plans.SaveReplacePlan(ctx, p); err != nil {
		return p, err
	}
	j := &job{stop: make(chan struct{}), done: make(chan struct{})}
	s.jobs[id] = j
	go s.run(p, newImg, j)
	return p, nil
}

// Stop implements R5.55: the running plan ends after its current step.
// Pointers already moved stay moved; Rollback undoes them.
func (s *Service) Stop(ctx context.Context, id string) (registry.ReplacePlan, error) {
	p, err := s.cfg.Plans.GetReplacePlan(ctx, id)
	if err != nil {
		return p, err
	}
	s.mu.Lock()
	j, ok := s.jobs[id]
	s.mu.Unlock()
	if p.State != registry.PlanRunning || !ok {
		return p, conflict("a plan that is %s cannot be stopped", p.State)
	}
	j.once.Do(func() { close(j.stop) })
	return p, nil
}

// Wait blocks until the plan's background run ends, or ctx ends. It returns at
// once for a plan that is not running here.
func (s *Service) Wait(ctx context.Context, id string) {
	s.mu.Lock()
	j, ok := s.jobs[id]
	s.mu.Unlock()
	if !ok {
		return
	}
	select {
	case <-j.done:
	case <-ctx.Done():
	}
}

// Recover marks every plan the database says is running, but that no run here
// owns, as stopped, so a controller restart never leaves a plan that looks busy
// (R5.58). Start continues it from its saved step. Call it once at startup.
func (s *Service) Recover(ctx context.Context) error {
	plans, err := s.cfg.Plans.ReplacePlansByState(ctx, registry.PlanRunning)
	if err != nil {
		return err
	}
	for _, p := range plans {
		s.mu.Lock()
		_, mine := s.jobs[p.ID]
		s.mu.Unlock()
		if mine {
			continue
		}
		p.State, p.Error = registry.PlanStopped, "the controller restarted; start again to go on"
		if err := s.cfg.Plans.SaveReplacePlan(ctx, p); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) finish(p registry.ReplacePlan, j *job) {
	s.mu.Lock()
	delete(s.jobs, p.ID)
	s.mu.Unlock()
	close(j.done)
}

func (s *Service) run(p registry.ReplacePlan, newImg registry.Image, j *job) {
	defer s.finish(p, j)
	ctx := context.Background()
	save := func() {
		if err := s.cfg.Plans.SaveReplacePlan(ctx, p); err != nil {
			s.cfg.Logger.Error("save replace plan", "plan", p.ID, "err", err)
		}
	}
	fail := func(err error) {
		p.State, p.Error, p.FinishedAt = registry.PlanFailed, err.Error(), s.cfg.Now()
		save()
	}
	stopped := func() bool {
		select {
		case <-j.stop:
			return true
		default:
			return false
		}
	}
	for p.StepsDone < p.StepsTotal {
		if stopped() {
			p.State = registry.PlanStopped
			save()
			return
		}
		i := p.StepsDone
		if i < len(p.Repositories) {
			if err := s.movePointer(ctx, p, newImg, p.Repositories[i]); err != nil {
				fail(err)
				return
			}
		} else if s.cfg.Warmer != nil {
			m := p.Machines[i-len(p.Repositories)]
			if err := s.cfg.Warmer.Warm(ctx, m, newImg); err != nil {
				// The machine pulls on its next job; the plan goes on.
				s.cfg.Logger.Warn("warm machine", "plan", p.ID, "machine", m, "err", err)
			}
		}
		p.StepsDone++
		save()
	}
	p.KeepUntil = s.cfg.Now().Add(time.Duration(p.KeepDays) * 24 * time.Hour)
	if _, err := s.cfg.Plans.SupersedeRegistryImage(ctx, p.OldImageID, p.NewImageID, p.KeepUntil); err != nil {
		fail(fmt.Errorf("mark the old image superseded: %w", err))
		return
	}
	p.State, p.FinishedAt = registry.PlanDone, s.cfg.Now()
	save()
}

// movePointer moves the pointer of one repository from the old image to the new
// one. A pointer that already points at the new image (a resumed plan) or at
// some other image (the owner moved it since) is left alone. Pointers of other
// repositories are never touched, so the old image stays for them (D7).
func (s *Service) movePointer(ctx context.Context, p registry.ReplacePlan, newImg registry.Image, repo string) error {
	ptr, err := s.cfg.Pointers.Pointer(ctx, repo, newImg.Profile, newImg.Arch)
	if errors.Is(err, registry.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read the pointer of %s: %w", repo, err)
	}
	if ptr.ImageID != p.OldImageID {
		return nil
	}
	ptr.ImageID, ptr.MovedBy, ptr.MovedAt = newImg.ID, p.CreatedBy, s.cfg.Now()
	if err := s.cfg.Pointers.MovePointer(ctx, ptr); err != nil {
		return fmt.Errorf("move the pointer of %s: %w", repo, err)
	}
	return nil
}

// Rollback implements R5.56: the pointers go back while keep_until has not
// passed; after it, ErrConflict (409).
func (s *Service) Rollback(ctx context.Context, id, by string) (registry.ReplacePlan, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.cfg.Plans.GetReplacePlan(ctx, id)
	if err != nil {
		return p, err
	}
	if p.State != registry.PlanDone {
		return p, conflict("only a finished replace can be rolled back, this one is %s", p.State)
	}
	if !s.cfg.Now().Before(p.KeepUntil) {
		return p, conflict("the old image was kept until %s and that time has passed", p.KeepUntil.UTC().Format(time.RFC3339))
	}
	old, err := s.cfg.Images.Image(ctx, p.OldImageID)
	if err != nil {
		return p, err
	}
	if old.State == registry.StateDeleted {
		return p, conflict("the old image is deleted")
	}
	newImg, err := s.cfg.Images.Image(ctx, p.NewImageID)
	if err != nil {
		return p, err
	}
	if _, err := s.cfg.Plans.RestoreRegistryImage(ctx, old.ID); err != nil {
		return p, err
	}
	for _, repo := range p.Repositories {
		ptr, err := s.cfg.Pointers.Pointer(ctx, repo, newImg.Profile, newImg.Arch)
		if errors.Is(err, registry.ErrNotFound) {
			continue
		}
		if err != nil {
			return p, err
		}
		if ptr.ImageID != p.NewImageID {
			continue // moved on since; not ours to move back
		}
		ptr.ImageID, ptr.MovedBy, ptr.MovedAt = old.ID, by, s.cfg.Now()
		if err := s.cfg.Pointers.MovePointer(ctx, ptr); err != nil {
			return p, fmt.Errorf("move the pointer of %s back: %w", repo, err)
		}
	}
	if _, err := s.cfg.Plans.MarkReplacePlanRolledBack(ctx, p.ID, by, s.cfg.Now()); err != nil {
		return p, err
	}
	return s.cfg.Plans.GetReplacePlan(ctx, id)
}
