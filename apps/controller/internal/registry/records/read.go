package records

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/store/sqlcgen"
)

// SimilarPercent is the share of bytes above which two images of one profile and arch are "similar" (R5.28).
const SimilarPercent = 60.0

// PullDays is how many days the image page chart shows (R5.35).
const PullDays = 14

// WeekDays is the "last 7 days" of the overview (R5.33).
const WeekDays = 7

// Where is where the registry runs.
type Where struct {
	Host     string
	Folder   string
	ReadOnly bool
	Running  bool
	WhyNot   string
}

// Budget is a kind's budget with the overshoot flag.
type Budget struct {
	registry.Budget
	Over bool
}

// Week is the last 7 days of pulls.
type Week struct {
	Pulls        int
	BuildsAvoid  int
	SavedSeconds float64
	BytesPulled  int64
}

// Cleanup is one cleanup run.
type Cleanup struct {
	ID         int64
	Trigger    string
	State      string
	FreedBytes int64
	Error      string
	StartedAt  time.Time
	FinishedAt time.Time
}

// Overview is the registry overview (R5.33).
type Overview struct {
	Where           Where
	Budgets         []Budget
	Week            Week
	Recommendations []registry.Recommendation
	// RecommendedImages are the images the recommendations name, by image id, so a screen needs no second call.
	RecommendedImages map[string]ImageSummary
	Standard          int
	Custom            int
	Builds            int
	Pulls             int
	Pushes            int
	LastCleanup       *Cleanup
	NextCleanupAt     time.Time
}

// ImageSummary is one row of the images list.
type ImageSummary struct {
	registry.Image
	Pulls7Days  int
	UpdateFound bool
	// UnusedDays is set while an open "unused" recommendation names the image: whole days since the last pull, or
	// since the push when it was never pulled.
	UnusedDays *int
}

// ImageFilter narrows the images list (R5.34). Zero fields match all.
type ImageFilter = registry.ImageFilter

// LineageEntry is one image of the same repository, profile and arch.
type LineageEntry struct {
	ImageID  string
	Key      registry.Key
	State    registry.State
	Branch   string
	PushedAt time.Time
	// Relation is replaces, replaced_by or same_profile, towards the image asked for.
	Relation string
}

// Layer is one layer with the number of other images that hold it.
type Layer struct {
	registry.Layer
	SharedWith int
}

// PullDay is one day of the chart.
type PullDay struct {
	Day   string // YYYY-MM-DD
	Pulls int
}

// Machine is a machine that pulled the image.
type Machine struct {
	ID            string
	Name          string
	Holds         bool
	LastUsedAt    time.Time
	InUseByRunner bool
}

// Scan is the scan of an image (REG8 writes it).
type Scan struct {
	State     registry.ScanState
	Packages  int
	Critical  int
	High      int
	Medium    int
	Low       int
	ScannedAt time.Time
	Error     string
}

// Similar is an image that shares over 60% of the bytes of this one.
type Similar struct {
	ImageID       string
	SharedPercent float64
}

// ImageDetail is the image page (R5.35).
type ImageDetail struct {
	ImageSummary
	// InUse says a runner uses the image now; SharedBytes are the bytes other images hold too.
	InUse       bool
	SharedBytes int64
	// VariantGoneDays is the setting of the days a variant lives after its branch is gone; 0 for other states.
	VariantGoneDays int
	Lineage         []LineageEntry
	Layers          []Layer
	PullsByDay      []PullDay
	PullsText       string
	SavedSeconds    float64
	Machines        []Machine
	Pointers        []registry.Pointer
	Scan            Scan
	Similar         []Similar
}

// Pull is a pull in progress or waiting for the controller (R5.36).
type Pull struct {
	ID          string
	MachineID   string
	MachineName string
	Key         registry.Key
	ImageID     string
	Repository  string
	Source      registry.Source
	State       string
	BytesDone   int64
	BytesTotal  int64
	// SecondsLeft is from the machine's measured pull speed; nil with no history (UX-29).
	SecondsLeft *float64
	StartedAt   time.Time
	Error       string
}

func (s *Service) machineInfo(ctx context.Context, id string, img registry.Image) MachineInfo {
	var mi MachineInfo
	if s.cfg.Machines != nil {
		mi = s.cfg.Machines.Machine(ctx, id, img)
	}
	if mi.Name == "" {
		mi.Name = id
	}
	return mi
}

// Overview reads the overview (R5.33).
func (s *Service) Overview(ctx context.Context) (Overview, error) {
	now := s.now()
	set, err := s.q.GetRegistrySettingsRow(ctx)
	if err != nil {
		return Overview{}, fmt.Errorf("read the registry settings: %w", err)
	}
	var ov Overview
	ov.Where = Where{Host: s.cfg.Host, Folder: set.Folder, ReadOnly: set.ReadOnly != 0}
	if ov.Where.Folder == "" {
		ov.Where.Folder = s.cfg.DefaultFolder
	}
	if s.cfg.Running != nil {
		ov.Where.Running, ov.Where.WhyNot = s.cfg.Running(ctx)
	} else {
		ov.Where.Running = s.cfg.Host != ""
		if !ov.Where.Running {
			ov.Where.WhyNot = "The registry has no address on this controller yet."
		}
	}

	used, err := s.q.SumRegistryUsedByKind(ctx)
	if err != nil {
		return Overview{}, err
	}
	shared, err := s.q.SumRegistrySharedLayersByKind(ctx)
	if err != nil {
		return Overview{}, err
	}
	usedBy, sharedBy := map[string]int64{}, map[string]int64{}
	for _, r := range used {
		usedBy[r.ChargedTo] = r.Bytes
	}
	for _, r := range shared {
		sharedBy[r.ChargedTo] = r.Bytes
	}
	for _, k := range []struct {
		kind  registry.Kind
		limit int64
	}{{registry.KindStandard, set.StandardBudget}, {registry.KindCustom, set.CustomBudget}} {
		b := registry.Budget{Kind: k.kind, LimitBytes: k.limit, UsedBytes: usedBy[string(k.kind)], SharedBytes: sharedBy[string(k.kind)]}
		ov.Budgets = append(ov.Budgets, Budget{Budget: b, Over: b.UsedBytes > b.LimitBytes})
	}

	since := s.day(now.AddDate(0, 0, -(WeekDays - 1)))
	sum, err := s.q.SumRegistryPullDailySince(ctx, since)
	if err != nil {
		return Overview{}, err
	}
	bytes, err := s.q.SumRegistryBytesPulledSince(ctx, stamp(startOfDay(now.In(s.cfg.Loc).AddDate(0, 0, -(WeekDays-1)))))
	if err != nil {
		return Overview{}, err
	}
	ov.Week = Week{Pulls: int(asInt(sum.Pulls)), BuildsAvoid: int(asInt(sum.BuildsSaved)), SavedSeconds: asFloat(sum.SavedSeconds), BytesPulled: bytes}

	recs, err := s.q.ListOpenRegistryRecommendations(ctx)
	if err != nil {
		return Overview{}, err
	}
	ov.Recommendations = orderRecommendations(recs)

	imgs, err := s.q.ListRegistryImages(ctx, sqlcgen.ListRegistryImagesParams{})
	if err != nil {
		return Overview{}, err
	}
	x, err := s.listExtras(ctx)
	if err != nil {
		return Overview{}, err
	}
	named := map[string]bool{}
	for _, r := range ov.Recommendations {
		if r.ImageID != "" {
			named[r.ImageID] = true
		}
	}
	ov.RecommendedImages = map[string]ImageSummary{}
	for _, im := range imgs {
		if named[im.ID] {
			ov.RecommendedImages[im.ID] = x.summary(toImage(im), now)
		}
		if !promoted(registry.State(im.State)) {
			continue
		}
		if im.Kind == string(registry.KindCustom) {
			ov.Custom++
		} else {
			ov.Standard++
		}
	}
	builds, err := s.q.ListImageBuildsInFlight(ctx)
	if err != nil {
		return Overview{}, err
	}
	pulls, err := s.q.ListRegistryPullsInFlight(ctx)
	if err != nil {
		return Overview{}, err
	}
	pushes, err := s.q.ListRegistryPushesInFlight(ctx)
	if err != nil {
		return Overview{}, err
	}
	ov.Builds, ov.Pulls, ov.Pushes = len(builds), len(pulls), len(pushes)

	if c, err := s.q.LastRegistryCleanup(ctx); err == nil {
		ov.LastCleanup = &Cleanup{ID: c.ID, Trigger: c.Trigger, State: c.State, FreedBytes: c.FreedBytes, Error: c.Error,
			StartedAt: parseStamp(c.StartedAt), FinishedAt: parseStamp(c.FinishedAt)}
	} else if !noRows(err) {
		return Overview{}, err
	}
	ov.NextCleanupAt = nextCleanup(now.In(s.cfg.Loc), time.Weekday(set.CleanupWeekday), int(set.CleanupHour))
	return ov, nil
}

func startOfDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

func promoted(st registry.State) bool {
	return st == registry.StateCurrent || st == registry.StateVariant || st == registry.StateSuperseded
}

// nextCleanup is the next weekday and local hour after now.
func nextCleanup(now time.Time, wd time.Weekday, hour int) time.Time {
	t := time.Date(now.Year(), now.Month(), now.Day(), hour, 0, 0, 0, now.Location())
	for i := 0; i < 8; i++ {
		c := t.AddDate(0, 0, i)
		if c.Weekday() == wd && c.After(now) {
			return c
		}
	}
	return t
}

// orderRecommendations puts the owner's-yes rules first, then crsp's by date (R5.33).
func orderRecommendations(rows []sqlcgen.RegistryRecommendation) []registry.Recommendation {
	out := make([]registry.Recommendation, 0, len(rows))
	for _, r := range rows {
		out = append(out, registry.Recommendation{ID: r.ID, Rule: registry.Rule(r.Rule), ImageID: r.ImageID,
			OtherID: r.OtherImageID, Kind: registry.Kind(r.Kind), FreesBytes: r.FreesBytes, Decider: registry.Decider(r.Decider),
			DueAt: parseStamp(r.DueAt), CreatedAt: parseStamp(r.CreatedAt)})
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if (a.Decider == registry.DecidedByOwner) != (b.Decider == registry.DecidedByOwner) {
			return a.Decider == registry.DecidedByOwner
		}
		if a.Decider == registry.DecidedByCrsp && !a.DueAt.Equal(b.DueAt) {
			return a.DueAt.Before(b.DueAt)
		}
		return a.CreatedAt.Before(b.CreatedAt)
	})
	return out
}

// extras are the figures of a list row that come from other tables.
type extras struct {
	pulls   map[string]int
	updated map[string]bool
	// unused holds the images an open "unused" recommendation names.
	unused map[string]bool
}

// summary builds the list row of an image.
func (x extras) summary(img registry.Image, now time.Time) ImageSummary {
	out := ImageSummary{Image: img, Pulls7Days: x.pulls[img.ID], UpdateFound: x.updated[img.ID]}
	if x.unused[img.ID] {
		since := img.LastPullAt
		if since.IsZero() {
			since = img.PushedAt
		}
		if !since.IsZero() {
			d := int(now.Sub(since) / (24 * time.Hour))
			if d < 0 {
				d = 0
			}
			out.UnusedDays = &d
		}
	}
	return out
}

// listExtras reads the pulls of the last 7 days per image id, the replace recommendations by the image they would
// replace, and the images an open "unused" recommendation names.
func (s *Service) listExtras(ctx context.Context) (extras, error) {
	rows, err := s.q.SumRegistryPullDailyByImageSince(ctx, s.day(s.now().AddDate(0, 0, -(WeekDays-1))))
	if err != nil {
		return extras{}, err
	}
	x := extras{pulls: map[string]int{}, updated: map[string]bool{}, unused: map[string]bool{}}
	for _, r := range rows {
		x.pulls[r.ImageID] = int(r.Pulls)
	}
	recs, err := s.q.ListOpenRegistryRecommendations(ctx)
	if err != nil {
		return extras{}, err
	}
	for _, r := range recs {
		if r.Rule == string(registry.RuleReplace) && r.OtherImageID != "" {
			x.updated[r.OtherImageID] = true
		}
		if r.Rule == string(registry.RuleUnused) && r.ImageID != "" {
			x.unused[r.ImageID] = true
		}
	}
	return x, nil
}

// Images lists images newest first (R5.34). Deleted images are never listed; staged and refused ones only when the
// state filter asks for them (they belong to the image page).
func (s *Service) Images(ctx context.Context, f ImageFilter) ([]ImageSummary, error) {
	p := sqlcgen.ListRegistryImagesParams{Kind: string(f.Kind), Arch: f.Arch, ProfileID: f.Profile}
	if len(f.States) == 1 {
		p.State = string(f.States[0])
	}
	rows, err := s.q.ListRegistryImages(ctx, p)
	if err != nil {
		return nil, err
	}
	var viaPointer map[string]bool
	if f.Repository != "" {
		ptrs, err := s.q.ListRegistryPointersOfRepository(ctx, f.Repository)
		if err != nil {
			return nil, err
		}
		viaPointer = map[string]bool{}
		for _, ptr := range ptrs {
			viaPointer[ptr.ImageID] = true
		}
	}
	x, err := s.listExtras(ctx)
	if err != nil {
		return nil, err
	}
	now := s.now()
	out := make([]ImageSummary, 0, len(rows))
	for _, r := range rows {
		st := registry.State(r.State)
		if len(f.States) == 0 && !promoted(st) {
			continue
		}
		if len(f.States) > 1 && !containsState(f.States, st) {
			continue
		}
		if f.Repository != "" && r.SourceRepository != f.Repository && !viaPointer[r.ID] {
			continue
		}
		out = append(out, x.summary(toImage(r), now))
	}
	return out, nil
}

func containsState(l []registry.State, s registry.State) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

// ImageDetail reads the image page (R5.35), or registry.ErrNotFound.
func (s *Service) ImageDetail(ctx context.Context, id string) (ImageDetail, error) {
	row, err := s.q.GetRegistryImage(ctx, id)
	if noRows(err) {
		return ImageDetail{}, registry.ErrNotFound
	}
	if err != nil {
		return ImageDetail{}, err
	}
	img := toImage(row)
	x, err := s.listExtras(ctx)
	if err != nil {
		return ImageDetail{}, err
	}
	d := ImageDetail{ImageSummary: x.summary(img, s.now())}
	if d.SharedBytes = img.SizeBytes - img.UniqueBytes; d.SharedBytes < 0 {
		d.SharedBytes = 0
	}
	if img.State == registry.StateVariant {
		set, err := s.q.GetRegistrySettingsRow(ctx)
		if err != nil {
			return ImageDetail{}, fmt.Errorf("read the registry settings: %w", err)
		}
		d.VariantGoneDays = int(set.VariantGoneDays)
	}

	lin, err := s.q.ListRegistryImagesOfLineage(ctx, sqlcgen.ListRegistryImagesOfLineageParams{
		Kind: row.Kind, ProfileID: row.ProfileID, Arch: row.Arch, SourceRepository: row.SourceRepository})
	if err != nil {
		return ImageDetail{}, err
	}
	d.Lineage = []LineageEntry{}
	for _, l := range lin {
		rel := "same_profile"
		switch l.ID {
		case img.Replaces:
			rel = "replaces"
		case img.ReplacedBy:
			rel = "replaced_by"
		}
		d.Lineage = append(d.Lineage, LineageEntry{ImageID: l.ID, Key: registry.Key(l.ImageTag), State: registry.State(l.State),
			Branch: l.Branch, PushedAt: parseStamp(l.PushedAt), Relation: rel})
	}

	layers, err := s.q.ListRegistryImageLayers(ctx, id)
	if err != nil {
		return ImageDetail{}, err
	}
	d.Layers = []Layer{}
	mine := map[string]int64{}
	for _, l := range layers {
		shared := int(l.Holders) - 1
		if shared < 0 {
			shared = 0
		}
		d.Layers = append(d.Layers, Layer{Layer: registry.Layer{Digest: l.Digest, SizeBytes: l.SizeBytes, MediaType: l.MediaType,
			ChargedTo: registry.Kind(l.ChargedTo), Images: []string{id}}, SharedWith: shared})
		mine[l.Digest] = l.SizeBytes
	}

	if err := s.fillPulls(ctx, &d); err != nil {
		return ImageDetail{}, err
	}

	ms, err := s.q.ListRegistryImageMachines(ctx, id)
	if err != nil {
		return ImageDetail{}, err
	}
	d.Machines = []Machine{}
	for _, m := range ms {
		mi := s.machineInfo(ctx, m.MachineID, img)
		last, _ := m.LastAt.(string)
		d.Machines = append(d.Machines, Machine{ID: m.MachineID, Name: mi.Name, Holds: mi.Holds,
			InUseByRunner: mi.InUseByRunner, LastUsedAt: parseStamp(last)})
		d.InUse = d.InUse || mi.InUseByRunner
	}

	ptrs, err := s.q.ListRegistryPointersTo(ctx, id)
	if err != nil {
		return ImageDetail{}, err
	}
	d.Pointers = []registry.Pointer{}
	for _, p := range ptrs {
		d.Pointers = append(d.Pointers, registry.Pointer{Repository: p.Repository, Profile: p.ProfileID, Arch: p.Arch,
			ImageID: p.ImageID, Previous: p.PreviousImageID, MovedAt: parseStamp(p.MovedAt), MovedBy: p.MovedBy})
	}

	d.Scan = Scan{State: img.Scan}
	if sc, err := s.q.GetRegistryScan(ctx, id); err == nil {
		d.Scan = Scan{State: registry.ScanState(sc.State), Packages: int(sc.Packages), Critical: int(sc.Critical), High: int(sc.High),
			Medium: int(sc.Medium), Low: int(sc.Low), ScannedAt: parseStamp(sc.FinishedAt), Error: sc.Error}
		// The image row's scan state is what lists show; the scan row has the same word, or "not scanned" before a row.
		if d.Scan.State == "" {
			d.Scan.State = img.Scan
		}
	} else if !noRows(err) {
		return ImageDetail{}, err
	}

	d.Similar, err = s.similar(ctx, row, mine)
	if err != nil {
		return ImageDetail{}, err
	}
	return d, nil
}

// fillPulls fills the 14-day chart, its text form and the time saved.
func (s *Service) fillPulls(ctx context.Context, d *ImageDetail) error {
	now := s.now().In(s.cfg.Loc)
	from := now.AddDate(0, 0, -(PullDays - 1))
	rows, err := s.q.ListRegistryPullDaily(ctx, sqlcgen.ListRegistryPullDailyParams{ImageID: d.ID, Day: from.Format(dayLayout)})
	if err != nil {
		return err
	}
	byDay := map[string]int{}
	for _, r := range rows {
		byDay[r.Day] = int(r.Pulls)
		d.SavedSeconds += r.SavedSeconds
	}
	total, bestDay, best := 0, "", 0
	d.PullsByDay = make([]PullDay, 0, PullDays)
	for i := 0; i < PullDays; i++ {
		day := from.AddDate(0, 0, i).Format(dayLayout)
		n := byDay[day]
		d.PullsByDay = append(d.PullsByDay, PullDay{Day: day, Pulls: n})
		total += n
		if n > best {
			best, bestDay = n, day
		}
	}
	if total == 0 {
		d.PullsText = "Not pulled in the last 14 days."
	} else {
		t, _ := time.ParseInLocation(dayLayout, bestDay, s.cfg.Loc)
		d.PullsText = fmt.Sprintf("Pulled %s in the last 14 days; most on %s (%d).", times(total), t.Format("Mon 2 Jan"), best)
	}
	return nil
}

func times(n int) string {
	if n == 1 {
		return "1 time"
	}
	return fmt.Sprintf("%d times", n)
}

// similar finds the images of the same profile and arch that share over 60% of this image's bytes (R5.28). It is
// information only.
func (s *Service) similar(ctx context.Context, row sqlcgen.RegistryImage, mine map[string]int64) ([]Similar, error) {
	out := []Similar{}
	if row.SizeBytes <= 0 || len(mine) == 0 {
		return out, nil
	}
	others, err := s.q.ListRegistryImages(ctx, sqlcgen.ListRegistryImagesParams{ProfileID: row.ProfileID, Arch: row.Arch})
	if err != nil {
		return nil, err
	}
	for _, o := range others {
		if o.ID == row.ID || !promoted(registry.State(o.State)) {
			continue
		}
		ls, err := s.q.ListRegistryImageLayers(ctx, o.ID)
		if err != nil {
			return nil, err
		}
		var shared int64
		for _, l := range ls {
			if _, ok := mine[l.Digest]; ok {
				shared += l.SizeBytes
			}
		}
		pct := float64(shared) / float64(row.SizeBytes) * 100
		if pct > SimilarPercent {
			out = append(out, Similar{ImageID: o.ID, SharedPercent: pct})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SharedPercent > out[j].SharedPercent })
	return out, nil
}

// Pulls lists the pulls in progress and those waiting for the controller (R5.36), oldest first. The time left comes
// from the pull speed of the machine's last 5 pulls and is absent with no history.
func (s *Service) Pulls(ctx context.Context, machineID string) ([]Pull, error) {
	rows, err := s.q.ListRegistryPullsInFlight(ctx)
	if err != nil {
		return nil, err
	}
	speed := map[string]float64{}
	out := []Pull{}
	for _, r := range rows {
		if machineID != "" && r.MachineID != machineID {
			continue
		}
		p := Pull{ID: r.PullID, MachineID: r.MachineID, Key: registry.Key(r.ImageTag), ImageID: r.ImageID, Repository: r.Repository,
			Source: registry.Source(r.Source), State: r.State, BytesDone: r.BytesDone, BytesTotal: r.BytesTotal,
			StartedAt: parseStamp(r.StartedAt), Error: r.Error}
		p.MachineName = s.machineInfo(ctx, r.MachineID, registry.Image{ID: r.ImageID}).Name
		v, seen := speed[r.MachineID]
		if !seen {
			h, err := s.q.MachinePullSpeed(ctx, r.MachineID)
			if err != nil {
				return nil, err
			}
			if h.Seconds > 0 && h.Bytes > 0 {
				v = float64(h.Bytes) / h.Seconds
			}
			speed[r.MachineID] = v
		}
		if r.State == "pulling" && v > 0 && r.BytesTotal > r.BytesDone {
			left := float64(r.BytesTotal-r.BytesDone) / v
			p.SecondsLeft = &left
		}
		out = append(out, p)
	}
	return out, nil
}
