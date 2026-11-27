package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api/gen"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry/records"
)

// RegistryReadService is what the registry read routes need (registry R5.33 to R5.36, REG4). *records.Service
// implements it.
type RegistryReadService interface {
	Overview(ctx context.Context) (records.Overview, error)
	Images(ctx context.Context, f records.ImageFilter) ([]records.ImageSummary, error)
	ImageDetail(ctx context.Context, id string) (records.ImageDetail, error)
	Pulls(ctx context.Context, machineID string) ([]records.Pull, error)
}

var _ RegistryReadService = (*records.Service)(nil)

func registryNotBuilt() gen.ErrorEnvelope { return notImplemented() }

func registryFail(s *server, err error) (int, gen.ErrorEnvelope) {
	if errors.Is(err, registry.ErrNotFound) {
		return http.StatusNotFound, gen.ErrorEnvelope{Error: gen.ErrorBody{Code: CodeNotFound, Message: "no such image"}}
	}
	s.opts.Logger.Error("registry read failed", "err", err)
	return http.StatusInternalServerError, gen.ErrorEnvelope{Error: gen.ErrorBody{Code: CodeInternal, Message: "internal error"}}
}

func optTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

func optStr(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}

func optRelation(v string) *gen.RegistryLineageEntryRelation {
	if v == "" {
		return nil
	}
	r := gen.RegistryLineageEntryRelation(v)
	return &r
}

func toGenRecommendation(r registry.Recommendation) gen.RegistryRecommendation {
	out := gen.RegistryRecommendation{Id: r.ID, Rule: gen.RegistryRule(r.Rule), Decider: gen.RegistryDecider(r.Decider),
		FreesBytes: r.FreesBytes, CreatedAt: r.CreatedAt, ImageId: optStr(r.ImageID), OtherImageId: optStr(r.OtherID), DueAt: optTime(r.DueAt)}
	if r.Kind != "" {
		k := gen.RegistryImageKind(r.Kind)
		out.Kind = &k
	}
	return out
}

func toGenRegistryBudget(b records.Budget) gen.RegistryBudget {
	return gen.RegistryBudget{Kind: gen.RegistryImageKind(b.Kind), LimitBytes: b.LimitBytes, UsedBytes: b.UsedBytes,
		SharedBytes: b.SharedBytes, Over: b.Over}
}

func toGenRegistrySummary(i records.ImageSummary) gen.RegistryImageSummary {
	out := gen.RegistryImageSummary{Id: i.ID, Kind: gen.RegistryImageKind(i.Kind), Name: i.Name, Key: i.Key.String(), Profile: i.Profile,
		Arch: gen.RegistryArch(i.Arch), State: gen.RegistryImageState(i.State), ManifestDigest: i.Manifest,
		SourceRepository: optStr(i.SourceRepository), Branch: optStr(i.Branch), SizeBytes: i.SizeBytes, UniqueBytes: i.UniqueBytes,
		Pulls7Days: i.Pulls7Days, LastPullAt: optTime(i.LastPullAt), KeepUntil: optTime(i.KeepUntil),
		ScanState: gen.RegistryScanState(i.Scan), PushedAt: i.PushedAt}
	if i.UpdateFound {
		t := true
		out.UpdateFound = &t
	}
	out.UnusedDays = i.UnusedDays
	return out
}

// GetRegistryOverview reads the overview (R5.33).
func (s *server) GetRegistryOverview(ctx context.Context, _ gen.GetRegistryOverviewRequestObject) (gen.GetRegistryOverviewResponseObject, error) {
	if s.opts.RegistryRead == nil {
		return gen.GetRegistryOverviewdefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: registryNotBuilt()}, nil
	}
	ov, err := s.opts.RegistryRead.Overview(ctx)
	if err != nil {
		code, body := registryFail(s, err)
		return gen.GetRegistryOverviewdefaultJSONResponse{StatusCode: code, Body: body}, nil
	}
	out := gen.GetRegistryOverview200JSONResponse{
		Where: gen.RegistryWhere{Host: ov.Where.Host, Folder: ov.Where.Folder, ReadOnly: ov.Where.ReadOnly, Running: ov.Where.Running,
			WhyNot: optStr(ov.Where.WhyNot)},
		Budgets:         []gen.RegistryBudget{},
		Last7Days:       gen.RegistryWeek{Pulls: ov.Week.Pulls, BuildsAvoided: ov.Week.BuildsAvoid, SavedSeconds: float32(ov.Week.SavedSeconds), BytesPulled: ov.Week.BytesPulled},
		Recommendations: []gen.RegistryRecommendation{},
		NextCleanupAt:   optTime(ov.NextCleanupAt),
	}
	for _, b := range ov.Budgets {
		out.Budgets = append(out.Budgets, toGenRegistryBudget(b))
	}
	for _, r := range ov.Recommendations {
		g := toGenRecommendation(r)
		if img, ok := ov.RecommendedImages[r.ImageID]; ok {
			sum := toGenRegistrySummary(img)
			g.Image = &sum
		}
		out.Recommendations = append(out.Recommendations, g)
	}
	out.Images.Standard, out.Images.Custom = ov.Standard, ov.Custom
	out.InFlight.Builds, out.InFlight.Pulls, out.InFlight.Pushes = ov.Builds, ov.Pulls, ov.Pushes
	if c := ov.LastCleanup; c != nil {
		out.LastCleanup = &gen.RegistryCleanup{Id: c.ID, Trigger: gen.RegistryCleanupTrigger(c.Trigger), State: gen.RegistryCleanupState(c.State),
			FreedBytes: c.FreedBytes, Error: optStr(c.Error), StartedAt: c.StartedAt, FinishedAt: optTime(c.FinishedAt)}
	}
	return out, nil
}

// ListRegistryImages lists images (R5.34).
func (s *server) ListRegistryImages(ctx context.Context, req gen.ListRegistryImagesRequestObject) (gen.ListRegistryImagesResponseObject, error) {
	if s.opts.RegistryRead == nil {
		return gen.ListRegistryImagesdefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: registryNotBuilt()}, nil
	}
	p := req.Params
	if bad := badRegistryFilter(p); bad != "" {
		return gen.ListRegistryImagesdefaultJSONResponse{StatusCode: http.StatusBadRequest,
			Body: gen.ErrorEnvelope{Error: gen.ErrorBody{Code: CodeBadRequest, Message: bad}}}, nil
	}
	var f records.ImageFilter
	if p.Kind != nil {
		f.Kind = registry.Kind(*p.Kind)
	}
	if p.State != nil {
		f.States = []registry.State{registry.State(*p.State)}
	}
	if p.Repository != nil {
		f.Repository = *p.Repository
	}
	if p.Arch != nil {
		f.Arch = string(*p.Arch)
	}
	if p.Profile != nil {
		f.Profile = *p.Profile
	}
	items, err := s.opts.RegistryRead.Images(ctx, f)
	if err != nil {
		code, body := registryFail(s, err)
		return gen.ListRegistryImagesdefaultJSONResponse{StatusCode: code, Body: body}, nil
	}
	out := gen.ListRegistryImages200JSONResponse{Items: []gen.RegistryImageSummary{}}
	for _, i := range items {
		out.Items = append(out.Items, toGenRegistrySummary(i))
	}
	return out, nil
}

// badRegistryFilter says what is wrong with the list filters, or "" when they are fine.
func badRegistryFilter(p gen.ListRegistryImagesParams) string {
	if p.Kind != nil && *p.Kind != gen.RegistryImageKind(registry.KindStandard) && *p.Kind != gen.RegistryImageKind(registry.KindCustom) {
		return "kind is standard or custom"
	}
	if p.State != nil {
		switch registry.State(*p.State) {
		case registry.StateStaged, registry.StateRefused, registry.StateCurrent, registry.StateVariant, registry.StateSuperseded, registry.StateDeleted:
		default:
			return "state is not one of the image states"
		}
	}
	if p.Arch != nil && *p.Arch != "amd64" && *p.Arch != "arm64" {
		return "arch is amd64 or arm64"
	}
	return ""
}

// GetRegistryImage reads the image page (R5.35).
func (s *server) GetRegistryImage(ctx context.Context, req gen.GetRegistryImageRequestObject) (gen.GetRegistryImageResponseObject, error) {
	if s.opts.RegistryRead == nil {
		return gen.GetRegistryImagedefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: registryNotBuilt()}, nil
	}
	d, err := s.opts.RegistryRead.ImageDetail(ctx, req.RegistryImageId)
	if err != nil {
		code, body := registryFail(s, err)
		return gen.GetRegistryImagedefaultJSONResponse{StatusCode: code, Body: body}, nil
	}
	sum := toGenRegistrySummary(d.ImageSummary)
	out := gen.GetRegistryImage200JSONResponse{
		Id: sum.Id, Kind: sum.Kind, Name: sum.Name, Key: sum.Key, Profile: sum.Profile, Arch: sum.Arch, State: sum.State,
		ManifestDigest: sum.ManifestDigest, SourceRepository: sum.SourceRepository, Branch: sum.Branch, SizeBytes: sum.SizeBytes,
		UniqueBytes: sum.UniqueBytes, Pulls7Days: sum.Pulls7Days, LastPullAt: sum.LastPullAt, KeepUntil: sum.KeepUntil,
		ScanState: sum.ScanState, PushedAt: sum.PushedAt, UpdateFound: sum.UpdateFound, UnusedDays: sum.UnusedDays,
		StackHash: optStr(d.StackHash), BaseName: optStr(d.BaseName), BaseDigest: optStr(d.BaseDigest), Commit: optStr(d.Commit),
		PushedBy: optStr(d.PushedBy), PromotedAt: optTime(d.PromotedAt), Replaces: optStr(d.Replaces), ReplacedBy: optStr(d.ReplacedBy),
		Lineage: []gen.RegistryLineageEntry{}, Layers: []gen.RegistryLayer{}, PullsByDay: []gen.RegistryPullDay{},
		Machines: []gen.RegistryImageMachine{}, Pointers: []gen.RegistryPointer{},
	}
	inUse, shared := d.InUse, d.SharedBytes
	out.InUse, out.SharedBytes = &inUse, &shared
	if d.VariantGoneDays > 0 || d.State == registry.StateVariant {
		days := d.VariantGoneDays
		out.VariantGoneDays = &days
	}
	for _, l := range d.Lineage {
		out.Lineage = append(out.Lineage, gen.RegistryLineageEntry{ImageId: l.ImageID, Key: l.Key.String(), State: gen.RegistryImageState(l.State),
			Branch: optStr(l.Branch), PushedAt: l.PushedAt, Relation: optRelation(l.Relation)})
	}
	for _, l := range d.Layers {
		out.Layers = append(out.Layers, gen.RegistryLayer{Digest: l.Digest, SizeBytes: l.SizeBytes, MediaType: optStr(l.MediaType), SharedWith: l.SharedWith})
	}
	for _, p := range d.PullsByDay {
		day, err := time.Parse("2006-01-02", p.Day)
		if err != nil {
			return nil, err
		}
		out.PullsByDay = append(out.PullsByDay, gen.RegistryPullDay{Day: openapi_types.Date{Time: day}, Pulls: p.Pulls})
	}
	out.PullsText = optStr(d.PullsText)
	saved := float32(d.SavedSeconds)
	out.SavedSeconds = &saved
	for _, m := range d.Machines {
		gm := gen.RegistryImageMachine{MachineId: m.ID, MachineName: m.Name, Holds: m.Holds, LastUsedAt: optTime(m.LastUsedAt)}
		if m.InUseByRunner {
			t := true
			gm.InUseByRunner = &t
		}
		out.Machines = append(out.Machines, gm)
	}
	for _, p := range d.Pointers {
		out.Pointers = append(out.Pointers, gen.RegistryPointer{Repository: p.Repository, Profile: p.Profile, Arch: gen.RegistryArch(p.Arch),
			ImageId: p.ImageID, PreviousImageId: optStr(p.Previous), MovedAt: p.MovedAt, MovedBy: p.MovedBy})
	}
	sc := gen.RegistryScan{State: gen.RegistryScanState(d.Scan.State), Error: optStr(d.Scan.Error), ScannedAt: optTime(d.Scan.ScannedAt)}
	if d.Scan.State == registry.ScanDone {
		p, c, h, m, l := d.Scan.Packages, d.Scan.Critical, d.Scan.High, d.Scan.Medium, d.Scan.Low
		sc.Packages, sc.Critical, sc.High, sc.Medium, sc.Low = &p, &c, &h, &m, &l
	}
	out.Scan = &sc
	similar := make([]struct {
		ImageId       string  `json:"image_id"`
		SharedPercent float32 `json:"shared_percent"`
	}, 0, len(d.Similar))
	for _, x := range d.Similar {
		similar = append(similar, struct {
			ImageId       string  `json:"image_id"`
			SharedPercent float32 `json:"shared_percent"`
		}{x.ImageID, float32(x.SharedPercent)})
	}
	out.Similar = &similar
	return out, nil
}

// ListRegistryPulls lists pulls in progress and waiting for the controller (R5.36).
func (s *server) ListRegistryPulls(ctx context.Context, req gen.ListRegistryPullsRequestObject) (gen.ListRegistryPullsResponseObject, error) {
	if s.opts.RegistryRead == nil {
		return gen.ListRegistryPullsdefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: registryNotBuilt()}, nil
	}
	machine := ""
	if req.Params.MachineId != nil {
		machine = *req.Params.MachineId
	}
	pulls, err := s.opts.RegistryRead.Pulls(ctx, machine)
	if err != nil {
		code, body := registryFail(s, err)
		return gen.ListRegistryPullsdefaultJSONResponse{StatusCode: code, Body: body}, nil
	}
	out := gen.ListRegistryPulls200JSONResponse{Items: []gen.RegistryPull{}}
	for _, p := range pulls {
		g := gen.RegistryPull{Id: p.ID, MachineId: p.MachineID, MachineName: optStr(p.MachineName), Key: p.Key.String(), ImageId: optStr(p.ImageID),
			Repository: optStr(p.Repository), Source: gen.RegistryPullSource(p.Source), State: gen.RegistryPullState(p.State),
			BytesDone: p.BytesDone, BytesTotal: p.BytesTotal, StartedAt: p.StartedAt, Error: optStr(p.Error)}
		if p.SecondsLeft != nil {
			v := float32(*p.SecondsLeft)
			g.SecondsLeft = &v
		}
		out.Items = append(out.Items, g)
	}
	return out, nil
}
