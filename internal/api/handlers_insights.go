package api

// CI run insights routes (docs/features/ci-run-insights R5.42). Every service is optional: a nil one answers 501
// on its routes, like an unbuilt operation. The handlers only read and map; the analysers, the store and the
// action plans live in internal/insights and are wired in cmd/crsp/insights_wiring.go.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/api/gen"
	"github.com/luis-mc/ci-runners-shared-pool/internal/audit"
	"github.com/luis-mc/ci-runners-shared-pool/internal/insights"
	"github.com/luis-mc/ci-runners-shared-pool/internal/insights/actions"
	"github.com/luis-mc/ci-runners-shared-pool/internal/insights/capacity"
	"github.com/luis-mc/ci-runners-shared-pool/internal/insights/digest"
	"github.com/luis-mc/ci-runners-shared-pool/internal/insights/guardrails"
	"github.com/luis-mc/ci-runners-shared-pool/internal/insights/runs"
	"github.com/luis-mc/ci-runners-shared-pool/internal/insights/tests"
	"github.com/luis-mc/ci-runners-shared-pool/internal/insights/timeline"
	"github.com/luis-mc/ci-runners-shared-pool/internal/insights/waits"
	"github.com/luis-mc/ci-runners-shared-pool/internal/insights/workflows"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

const (
	defaultHoldCap     = 120 * time.Second
	defaultInsightPage = 50
	maxInsightPage     = 100
	maxTimelineSpan    = 24 * time.Hour
	sizingExtraSlots   = 3 // estimates for +0 to +3 slots
	sizingMethod       = "Allen-Cunneen approximation of a G/G/c queue with Sakasegawa's wait formula; an estimate, not a promise."
	digestsShown       = 12
)

// InsightsArea holds the services of the insights routes. Every field may be nil.
type InsightsArea struct {
	Findings   insights.FindingStore
	Dismiss    insights.Dismisser // hides a finding for 7 days (D17)
	Plans      actions.Planner
	Waits      waits.Reader
	Timeline   timeline.Builder
	Demand     capacity.DemandReader
	Estimator  capacity.Estimator
	Tests      tests.Reader
	Guardrails guardrails.Store
	Digests    digest.Reader
	Runs       runs.Reader
	Workflows  workflows.Store
	Resources  capacity.Resources
	// HoldCap is the hold cap setting (R5.14); nil or 0 uses 120 s.
	HoldCap func(ctx context.Context) time.Duration
	// Backlog is how many jobs wait for the GitHub hourly limit and until when (R6.1); nil says nothing.
	Backlog func() (jobs int, until time.Time)
	// Now is the clock; nil uses time.Now.
	Now func() time.Time
}

func (a *InsightsArea) now() time.Time {
	if a.Now != nil {
		return a.Now().UTC()
	}
	return time.Now().UTC()
}

func (a *InsightsArea) holdCap(ctx context.Context) time.Duration {
	if a.HoldCap != nil {
		if d := a.HoldCap(ctx); d > 0 {
			return d
		}
	}
	return defaultHoldCap
}

// insightFail is an error answer of an insights route.
type insightFail struct {
	status int
	body   gen.ErrorEnvelope
}

func insightNotBuilt() insightFail {
	return insightFail{http.StatusNotImplemented, notImplemented()}
}

func insightBad(msg string) insightFail {
	return insightFail{http.StatusBadRequest, errBody(CodeBadRequest, msg)}
}

func insightMissing(msg string) insightFail {
	return insightFail{http.StatusNotFound, errBody(CodeNotFound, msg)}
}

func insightConflict(msg string) insightFail {
	return insightFail{http.StatusConflict, errBody(CodeConflict, msg)}
}

// insightsOK is true when the area and the service the route needs are there.
func (s *server) insights(have func(*InsightsArea) bool) (*InsightsArea, bool) {
	a := s.opts.Insights
	if a == nil || !have(a) {
		return nil, false
	}
	return a, true
}

func (s *server) insightErr(op string, err error) insightFail {
	s.opts.Logger.Error("insights request failed", "op", op, "err", err)
	return insightFail{http.StatusInternalServerError, errBody(CodeInternal, "internal error")}
}

func pageOf(p *int) (int, bool) {
	if p == nil {
		return defaultInsightPage, true
	}
	if *p < 1 || *p > maxInsightPage {
		return 0, false
	}
	return *p, true
}

func dateOf(t time.Time) openapi_types.Date { return openapi_types.Date{Time: t.UTC()} }

func insightFinding(f insights.StoredFinding) gen.InsightFinding {
	out := gen.InsightFinding{
		Id:               f.ID,
		Kind:             string(f.Kind),
		Scope:            gen.InsightFindingScope(f.Scope),
		Area:             gen.InsightArea(f.Area),
		Title:            f.Title,
		Fix:              f.Fix,
		MinutesSavedWeek: f.MinutesSavedWeek,
		FirstSeen:        f.FirstSeen.UTC(),
		LastSeen:         f.LastSeen.UTC(),
		Evidence:         map[string]interface{}{},
	}
	if len(f.Evidence) > 0 {
		var ev map[string]interface{}
		if json.Unmarshal(f.Evidence, &ev) == nil && ev != nil {
			out.Evidence = ev
		}
	}
	if f.RepositoryID != 0 {
		out.RepositoryId = ptr(f.RepositoryID)
	}
	if f.Repository != "" {
		out.Repository = ptr(f.Repository)
	}
	if f.Subject != "" {
		out.Subject = ptr(f.Subject)
	}
	if f.Snippet != "" {
		out.Snippet = ptr(f.Snippet)
	}
	if f.Guardrail != "" {
		g := gen.Guardrail(f.Guardrail)
		out.Guardrail = &g
	}
	if f.Action != insights.ActionNone {
		k := gen.InsightActionKind(f.Action)
		out.ActionKind = &k
	}
	if !f.ResolvedAt.IsZero() {
		out.ResolvedAt = tptr(f.ResolvedAt.UTC())
	}
	return out
}

// withDismissed adds the time a hidden finding shows again; a time that has passed says nothing.
func withDismissed(out gen.InsightFinding, f insights.StoredFinding, now time.Time) gen.InsightFinding {
	if f.DismissedUntil.After(now) {
		out.DismissedUntil = tptr(f.DismissedUntil.UTC())
	}
	return out
}

// DismissInsightFinding implements POST /api/v1/insights/findings/{findingId}/dismiss (D17): hide for 7 days.
func (s *server) DismissInsightFinding(ctx context.Context, req gen.DismissInsightFindingRequestObject) (gen.DismissInsightFindingResponseObject, error) {
	fail := func(f insightFail) (gen.DismissInsightFindingResponseObject, error) {
		return gen.DismissInsightFindingdefaultJSONResponse{StatusCode: f.status, Body: f.body}, nil
	}
	a, ok := s.insights(func(a *InsightsArea) bool { return a.Dismiss != nil && a.Findings != nil })
	if !ok {
		return fail(insightNotBuilt())
	}
	if _, err := a.Dismiss.Dismiss(ctx, req.FindingId, a.now()); err != nil {
		switch {
		case errors.Is(err, insights.ErrNotFound):
			return fail(insightMissing("no such finding"))
		case errors.Is(err, insights.ErrResolved):
			return fail(insightConflict(err.Error()))
		}
		return fail(s.insightErr("dismissInsightFinding", err))
	}
	f, err := a.Findings.Get(ctx, req.FindingId)
	if err != nil {
		return fail(s.insightErr("dismissInsightFinding", err))
	}
	return gen.DismissInsightFinding200JSONResponse(withDismissed(insightFinding(f), f, a.now())), nil
}

// ListInsightFindings implements GET /api/v1/insights/findings (R5.11).
func (s *server) ListInsightFindings(ctx context.Context, req gen.ListInsightFindingsRequestObject) (gen.ListInsightFindingsResponseObject, error) {
	fail := func(f insightFail) (gen.ListInsightFindingsResponseObject, error) {
		return gen.ListInsightFindingsdefaultJSONResponse{StatusCode: f.status, Body: f.body}, nil
	}
	a, ok := s.insights(func(a *InsightsArea) bool { return a.Findings != nil })
	if !ok {
		return fail(insightNotBuilt())
	}
	p := req.Params
	limit, ok := pageOf(p.Limit)
	if !ok {
		return fail(insightBad("limit must be 1 to 100"))
	}
	f := insights.Filter{Limit: limit}
	if p.RepositoryId != nil {
		f.RepositoryID = *p.RepositoryId
	}
	if p.Area != nil {
		f.Area = insights.Area(*p.Area)
	}
	if p.Guardrail != nil {
		f.Guardrail = insights.Guardrail(*p.Guardrail)
	}
	if p.State != nil {
		switch string(*p.State) {
		case "open":
		case "resolved":
			f.Resolved = true
		default:
			return fail(insightBad("state must be open or resolved"))
		}
	}
	if p.Cursor != nil {
		f.Cursor = *p.Cursor
	}
	items, total, next, err := a.Findings.List(ctx, f)
	if err != nil {
		return fail(s.insightErr("listInsightFindings", err))
	}
	out := gen.InsightFindingList{Items: make([]gen.InsightFinding, 0, len(items)), Total: total}
	for _, it := range items {
		out.Items = append(out.Items, insightFinding(it))
	}
	if next != "" {
		out.NextCursor = &next
	}
	out.NotReadYet = backlog(a)
	return gen.ListInsightFindings200JSONResponse(out), nil
}

func backlog(a *InsightsArea) *gen.InsightBacklog {
	if a.Backlog == nil {
		return nil
	}
	jobs, until := a.Backlog()
	if jobs <= 0 {
		return nil
	}
	b := gen.InsightBacklog{Jobs: jobs}
	if !until.IsZero() {
		b.Until = tptr(until.UTC())
	}
	return &b
}

// GetInsightFinding implements GET /api/v1/insights/findings/{findingId} (R5.7).
func (s *server) GetInsightFinding(ctx context.Context, req gen.GetInsightFindingRequestObject) (gen.GetInsightFindingResponseObject, error) {
	fail := func(f insightFail) (gen.GetInsightFindingResponseObject, error) {
		return gen.GetInsightFindingdefaultJSONResponse{StatusCode: f.status, Body: f.body}, nil
	}
	a, ok := s.insights(func(a *InsightsArea) bool { return a.Findings != nil })
	if !ok {
		return fail(insightNotBuilt())
	}
	f, err := a.Findings.Get(ctx, req.FindingId)
	if err != nil {
		if errors.Is(err, ErrInsightNotFound) {
			return fail(insightMissing("no such finding"))
		}
		return fail(s.insightErr("getInsightFinding", err))
	}
	return gen.GetInsightFinding200JSONResponse(withDismissed(insightFinding(f), f, a.now())), nil
}

// ErrInsightNotFound is what a store returns when a finding or plan id is not there. The wiring maps its
// store's not-found error to it.
var ErrInsightNotFound = errors.New("not found")

func insightPlan(p actions.Plan) gen.InsightPlan {
	out := gen.InsightPlan{
		Id:         p.ID,
		FindingId:  p.FindingID,
		ActionKind: gen.InsightActionKind(p.Action),
		State:      gen.InsightPlanState(p.State),
		CreatedAt:  p.CreatedAt.UTC(),
		Changes:    make([]gen.InsightPlanChange, 0, len(p.Changes)),
		Results:    make([]gen.InsightPlanResult, 0, len(p.Results)),
	}
	for _, c := range p.Changes {
		ch := gen.InsightPlanChange{Target: c.Target, Field: c.Field, From: c.From, To: c.To, Words: c.Words}
		if c.TargetName != "" {
			ch.TargetName = ptr(c.TargetName)
		}
		if len(c.JobsWaitedFor) > 0 {
			w := c.JobsWaitedFor
			ch.JobsWaitedFor = &w
		}
		out.Changes = append(out.Changes, ch)
	}
	for _, r := range p.Results {
		res := gen.InsightPlanResult{Target: r.Target, Field: r.Field, State: gen.InsightPlanResultState(r.State)}
		if r.Error != "" {
			res.Error = ptr(r.Error)
		}
		out.Results = append(out.Results, res)
	}
	if p.Estimate != "" {
		out.Estimate = ptr(p.Estimate)
	}
	if p.CreatedBy != "" {
		out.CreatedBy = ptr(p.CreatedBy)
	}
	if !p.StartedAt.IsZero() {
		out.StartedAt = tptr(p.StartedAt.UTC())
	}
	if !p.FinishedAt.IsZero() {
		out.FinishedAt = tptr(p.FinishedAt.UTC())
	}
	return out
}

// ErrPlanNotDraft is what Planner.Start returns for a plan that is not in draft; the wiring maps its error to it.
var ErrPlanNotDraft = errors.New("the plan is not a draft")

// ErrFindingResolved is what Planner.Plan returns for a resolved finding; the wiring maps its error to it.
var ErrFindingResolved = errors.New("the finding is resolved")

func (s *server) planErr(op string, err error) insightFail {
	switch {
	case errors.Is(err, ErrInsightNotFound):
		return insightMissing("no such finding or plan")
	case errors.Is(err, actions.ErrNoAction):
		return insightConflict(err.Error())
	case errors.Is(err, ErrFindingResolved), errors.Is(err, ErrPlanNotDraft):
		return insightConflict(err.Error())
	}
	return s.insightErr(op, err)
}

// planActor is who a plan is made or started by: the signed-in login.
func (s *server) planActor(ctx context.Context) string {
	if p, ok := PrincipalFrom(ctx); ok && p.Login != "" {
		return p.Login
	}
	return audit.ActorFrom(ctx).Login
}

// CreateInsightPlan implements POST /api/v1/insights/findings/{findingId}/plans (R5.10). Nothing changes yet.
func (s *server) CreateInsightPlan(ctx context.Context, req gen.CreateInsightPlanRequestObject) (gen.CreateInsightPlanResponseObject, error) {
	fail := func(f insightFail) (gen.CreateInsightPlanResponseObject, error) {
		return gen.CreateInsightPlandefaultJSONResponse{StatusCode: f.status, Body: f.body}, nil
	}
	a, ok := s.insights(func(a *InsightsArea) bool { return a.Plans != nil })
	if !ok {
		return fail(insightNotBuilt())
	}
	p, err := a.Plans.Plan(ctx, req.FindingId, s.planActor(ctx))
	if err != nil {
		return fail(s.planErr("createInsightPlan", err))
	}
	return gen.CreateInsightPlan201JSONResponse(insightPlan(p)), nil
}

// GetInsightPlan implements GET /api/v1/insights/plans/{planId}.
func (s *server) GetInsightPlan(ctx context.Context, req gen.GetInsightPlanRequestObject) (gen.GetInsightPlanResponseObject, error) {
	fail := func(f insightFail) (gen.GetInsightPlanResponseObject, error) {
		return gen.GetInsightPlandefaultJSONResponse{StatusCode: f.status, Body: f.body}, nil
	}
	a, ok := s.insights(func(a *InsightsArea) bool { return a.Plans != nil })
	if !ok {
		return fail(insightNotBuilt())
	}
	p, err := a.Plans.Get(ctx, req.PlanId)
	if err != nil {
		return fail(s.planErr("getInsightPlan", err))
	}
	return gen.GetInsightPlan200JSONResponse(insightPlan(p)), nil
}

// StartInsightPlan implements POST /api/v1/insights/plans/{planId}/start (R5.10, R6.6).
func (s *server) StartInsightPlan(ctx context.Context, req gen.StartInsightPlanRequestObject) (gen.StartInsightPlanResponseObject, error) {
	fail := func(f insightFail) (gen.StartInsightPlanResponseObject, error) {
		return gen.StartInsightPlandefaultJSONResponse{StatusCode: f.status, Body: f.body}, nil
	}
	a, ok := s.insights(func(a *InsightsArea) bool { return a.Plans != nil })
	if !ok {
		return fail(insightNotBuilt())
	}
	p, err := a.Plans.Start(ctx, req.PlanId, s.planActor(ctx))
	if err != nil {
		return fail(s.planErr("startInsightPlan", err))
	}
	return gen.StartInsightPlan202JSONResponse(insightPlan(p)), nil
}

// GetInsightSummary implements GET /api/v1/insights/summary.
func (s *server) GetInsightSummary(ctx context.Context, _ gen.GetInsightSummaryRequestObject) (gen.GetInsightSummaryResponseObject, error) {
	fail := func(f insightFail) (gen.GetInsightSummaryResponseObject, error) {
		return gen.GetInsightSummarydefaultJSONResponse{StatusCode: f.status, Body: f.body}, nil
	}
	a, ok := s.insights(func(a *InsightsArea) bool { return a.Findings != nil })
	if !ok {
		return fail(insightNotBuilt())
	}
	sum, err := a.Findings.Summary(ctx)
	if err != nil {
		return fail(s.insightErr("getInsightSummary", err))
	}
	return gen.GetInsightSummary200JSONResponse{Open: sum.Open, MinutesSavedWeek: sum.MinutesSavedWeek, EnoughData: ptrOf(sum.EnoughData)}, nil
}

func durStats(st waits.Stats) gen.DurationStats {
	out := gen.DurationStats{TotalSeconds: int64(st.Total / time.Second), Known: ptrOf(st.Known)}
	if st.Known > 0 {
		out.P50Seconds = ptr(int(st.P50 / time.Second))
		out.P95Seconds = ptr(int(st.P95 / time.Second))
	}
	return out
}

// GetWaitSplit implements GET /api/v1/insights/waits (R5.12 to R5.14).
func (s *server) GetWaitSplit(ctx context.Context, req gen.GetWaitSplitRequestObject) (gen.GetWaitSplitResponseObject, error) {
	fail := func(f insightFail) (gen.GetWaitSplitResponseObject, error) {
		return gen.GetWaitSplitdefaultJSONResponse{StatusCode: f.status, Body: f.body}, nil
	}
	a, ok := s.insights(func(a *InsightsArea) bool { return a.Waits != nil })
	if !ok {
		return fail(insightNotBuilt())
	}
	days := 7
	if req.Params.Days != nil {
		days = int(*req.Params.Days)
	}
	if days != 7 && days != 14 && days != 30 {
		return fail(insightBad("days must be 7, 14 or 30"))
	}
	group := waits.ByDay
	if req.Params.Group != nil {
		switch g := waits.Group(*req.Params.Group); g {
		case waits.ByRepository, waits.ByJobKey, waits.ByDay:
			group = g
		default:
			return fail(insightBad("group must be repository, job_key or day"))
		}
	}
	var repo int64
	if req.Params.RepositoryId != nil {
		repo = *req.Params.RepositoryId
	}
	hold := a.holdCap(ctx)
	rows, err := a.Waits.SplitBy(ctx, days, group, repo, hold)
	if err != nil {
		return fail(s.insightErr("getWaitSplit", err))
	}
	out := gen.WaitSplit{Days: days, Group: gen.WaitSplitGroup(group), HoldCapSeconds: int(hold / time.Second), Rows: make([]gen.WaitSplitRow, 0, len(rows))}
	for _, r := range rows {
		row := gen.WaitSplitRow{Key: r.Key, Jobs: r.Jobs, Wait: durStats(r.Wait), Hold: durStats(r.Hold), Run: durStats(r.Run), HoldsOverCap: ptrOf(r.HoldsOverCap)}
		if len(r.HoldReasons) > 0 {
			m := make(map[string]int, len(r.HoldReasons))
			for k, v := range r.HoldReasons {
				m[string(k)] = v
			}
			row.HoldReasons = &m
		}
		out.Rows = append(out.Rows, row)
	}
	return gen.GetWaitSplit200JSONResponse(out), nil
}

// GetPoolTimeline implements GET /api/v1/insights/timeline (R5.21): at most 24 hours.
func (s *server) GetPoolTimeline(ctx context.Context, req gen.GetPoolTimelineRequestObject) (gen.GetPoolTimelineResponseObject, error) {
	fail := func(f insightFail) (gen.GetPoolTimelineResponseObject, error) {
		return gen.GetPoolTimelinedefaultJSONResponse{StatusCode: f.status, Body: f.body}, nil
	}
	a, ok := s.insights(func(a *InsightsArea) bool { return a.Timeline != nil })
	if !ok {
		return fail(insightNotBuilt())
	}
	to := a.now()
	if req.Params.To != nil {
		to = req.Params.To.UTC()
	}
	from := to.Add(-maxTimelineSpan)
	if req.Params.From != nil {
		from = req.Params.From.UTC()
	}
	if !from.Before(to) {
		return fail(insightBad("from must be before to"))
	}
	if to.Sub(from) > maxTimelineSpan {
		return fail(insightBad("the timeline shows at most 24 hours"))
	}
	lanes, err := a.Timeline.Timeline(ctx, insights.Window{From: from, To: to})
	if err != nil {
		return fail(s.insightErr("getPoolTimeline", err))
	}
	out := gen.PoolTimeline{From: from, To: to, Lanes: make([]gen.TimelineLane, 0, len(lanes))}
	for _, l := range lanes {
		lane := gen.TimelineLane{RunnerName: l.RunnerName, Origin: laneOrigin(l.Origin), Segments: make([]gen.TimelineSegment, 0, len(l.Segments))}
		if l.MachineName != "" {
			lane.MachineName = ptr(l.MachineName)
		}
		if len(l.Labels) > 0 {
			ls := l.Labels
			lane.Labels = &ls
		}
		for _, sg := range l.Segments {
			lane.Segments = append(lane.Segments, timelineSegment(sg))
		}
		out.Lanes = append(out.Lanes, lane)
	}
	return gen.GetPoolTimeline200JSONResponse(out), nil
}

// laneOrigin keeps the contract's three origins; a lane with none known is shown as other.
func laneOrigin(o insights.Origin) gen.TimelineLaneOrigin {
	switch o {
	case insights.OriginCrsp, insights.OriginHosted:
		return gen.TimelineLaneOrigin(o)
	}
	return gen.TimelineLaneOrigin(insights.OriginOther)
}

func timelineSegment(sg timeline.Segment) gen.TimelineSegment {
	out := gen.TimelineSegment{Kind: gen.TimelineSegmentKind(sg.Kind), Start: sg.Start.UTC(), End: sg.End.UTC()}
	if sg.JobID != 0 {
		out.JobId = ptr(sg.JobID)
	}
	if sg.RunID != 0 {
		out.RunId = ptr(sg.RunID)
	}
	if sg.FollowUp {
		out.FollowUp = ptr(true)
	}
	if sg.Repository != "" {
		out.Repository = ptr(sg.Repository)
	}
	if sg.JobIdent != "" {
		out.JobIdent = ptr(sg.JobIdent)
	}
	if sg.Event != "" {
		out.Event = ptr(sg.Event)
	}
	if len(sg.Markers) > 0 {
		ms := make([]gen.TimelineSegmentMarkers, 0, len(sg.Markers))
		for _, m := range sg.Markers {
			ms = append(ms, gen.TimelineSegmentMarkers(m))
		}
		out.Markers = &ms
	}
	if len(sg.WaitingJobIDs) > 0 {
		w := sg.WaitingJobIDs
		out.WaitingJobIds = &w
	}
	return out
}

func secsOf(d time.Duration) float32 { return float32(d.Seconds()) }

// GetPoolSizing implements GET /api/v1/insights/pool-sizing (R5.15 to R5.17).
func (s *server) GetPoolSizing(ctx context.Context, req gen.GetPoolSizingRequestObject) (gen.GetPoolSizingResponseObject, error) {
	fail := func(f insightFail) (gen.GetPoolSizingResponseObject, error) {
		return gen.GetPoolSizingdefaultJSONResponse{StatusCode: f.status, Body: f.body}, nil
	}
	a, ok := s.insights(func(a *InsightsArea) bool { return a.Demand != nil && a.Estimator != nil })
	if !ok {
		return fail(insightNotBuilt())
	}
	days := 7
	if req.Params.Days != nil {
		days = int(*req.Params.Days)
	}
	if days != 7 && days != 14 && days != 30 {
		return fail(insightBad("days must be 7, 14 or 30"))
	}
	to := a.now()
	loads, err := a.Demand.Demand(ctx, insights.Window{From: to.AddDate(0, 0, -days), To: to})
	if err != nil {
		return fail(s.insightErr("getPoolSizing", err))
	}
	out := gen.PoolSizing{Days: days, Method: sizingMethod, LabelSets: make([]gen.LabelSetDemand, 0, len(loads))}
	for _, l := range loads {
		out.LabelSets = append(out.LabelSets, labelSetDemand(a.Estimator, l))
	}
	return gen.GetPoolSizing200JSONResponse(out), nil
}

func labelSetDemand(est capacity.Estimator, l capacity.LabelSetLoad) gen.LabelSetDemand {
	d := gen.LabelSetDemand{
		Labels:      l.Labels,
		Slots:       l.Slots,
		Utilisation: float32(l.Utilisation),
		JobsPerHour: float32(l.JobsPerHour),
		Estimates:   make([]gen.SlotEstimate, 0, sizingExtraSlots+1),
	}
	if d.Labels == nil {
		d.Labels = []string{}
	}
	if l.MeanService > 0 {
		d.MeanServiceSeconds = ptr(secsOf(l.MeanService))
	}
	if l.CVArrivals > 0 {
		d.CvArrivals = ptr(float32(l.CVArrivals))
	}
	if l.CVService > 0 {
		d.CvService = ptr(float32(l.CVService))
	}
	if l.P50Wait > 0 {
		d.P50WaitSeconds = ptr(secsOf(l.P50Wait))
	}
	if l.P95Wait > 0 {
		d.P95WaitSeconds = ptr(secsOf(l.P95Wait))
	}
	if len(l.P95WaitByDay) > 0 {
		days := make([]gen.DayValue, 0, len(l.P95WaitByDay))
		for _, dv := range l.P95WaitByDay {
			if t, err := time.Parse("2006-01-02", dv.Day); err == nil {
				days = append(days, gen.DayValue{Day: dateOf(t), Value: float32(dv.Value)})
			}
		}
		d.P95WaitByDay = &days
	}
	for extra := 0; extra <= sizingExtraSlots; extra++ {
		e := est.Estimate(l, extra)
		se := gen.SlotEstimate{ExtraSlots: extra, Utilisation: ptrOf(float32(e.Utilisation))}
		if !e.Saturated {
			se.MeanWaitSeconds = ptr(secsOf(e.MeanWait))
			se.P95WaitSeconds = ptr(secsOf(e.P95Wait))
		}
		if e.Saturated && d.MinSlotsNeeded == nil && e.MinSlots > 0 {
			d.MinSlotsNeeded = ptr(e.MinSlots)
		}
		d.Estimates = append(d.Estimates, se)
	}
	return d
}

// ListTestInsights implements GET /api/v1/insights/tests (R5.33 to R5.37).
func (s *server) ListTestInsights(ctx context.Context, req gen.ListTestInsightsRequestObject) (gen.ListTestInsightsResponseObject, error) {
	fail := func(f insightFail) (gen.ListTestInsightsResponseObject, error) {
		return gen.ListTestInsightsdefaultJSONResponse{StatusCode: f.status, Body: f.body}, nil
	}
	a, ok := s.insights(func(a *InsightsArea) bool { return a.Tests != nil })
	if !ok {
		return fail(insightNotBuilt())
	}
	p := req.Params
	view := tests.Slowest
	if p.View != nil {
		switch v := tests.View(*p.View); v {
		case tests.Slowest, tests.Flaky, tests.Duplicated, tests.NoAssertions:
			view = v
		default:
			return fail(insightBad("view must be slowest, flaky, duplicated or no_assertions"))
		}
	}
	days := 7
	if p.Days != nil {
		days = int(*p.Days)
	}
	if days != 7 && days != 14 && days != 30 {
		return fail(insightBad("days must be 7, 14 or 30"))
	}
	limit, ok := pageOf(p.Limit)
	if !ok {
		return fail(insightBad("limit must be 1 to 100"))
	}
	var repo int64
	if p.RepositoryId != nil {
		repo = *p.RepositoryId
	}
	var cursor string
	if p.Cursor != nil {
		cursor = *p.Cursor
	}
	items, total, next, found, err := a.Tests.Tests(ctx, view, repo, days, limit, cursor)
	if err != nil {
		return fail(s.insightErr("listTestInsights", err))
	}
	out := gen.TestInsightList{Items: make([]gen.TestInsight, 0, len(items)), Total: total, ReportsFound: found}
	for _, it := range items {
		out.Items = append(out.Items, testInsight(it))
	}
	if next != "" {
		out.NextCursor = &next
	}
	return gen.ListTestInsights200JSONResponse(out), nil
}

func testInsight(it tests.Insight) gen.TestInsight {
	t := gen.TestInsight{TestId: it.TestID, Name: it.Name, Repository: it.Repository, Runs: it.Runs,
		Failures: ptrOf(it.Failures), FlakyRuns: ptrOf(it.FlakyRuns), Assertions: ptrOf(it.Assertions)}
	if it.Suite != "" {
		t.Suite = ptr(it.Suite)
	}
	if it.File != "" {
		t.File = ptr(it.File)
	}
	if len(it.JobIdents) > 0 {
		j := it.JobIdents
		t.JobIdents = &j
	}
	if it.Kind != "" {
		k := gen.TestInsightKind(it.Kind)
		t.Kind = &k
	}
	if it.P50 > 0 {
		t.P50Ms = ptr(int(it.P50 / time.Millisecond))
	}
	if it.P95 > 0 {
		t.P95Ms = ptr(int(it.P95 / time.Millisecond))
	}
	if it.OverLimit > 0 {
		t.OverLimitSeconds = ptr(int(it.OverLimit / time.Second))
	}
	if !it.LastSeen.IsZero() {
		t.LastSeen = tptr(it.LastSeen.UTC())
	}
	return t
}

// ListGuardrailStatus implements GET /api/v1/insights/guardrails (R5.31, R5.32).
func (s *server) ListGuardrailStatus(ctx context.Context, req gen.ListGuardrailStatusRequestObject) (gen.ListGuardrailStatusResponseObject, error) {
	fail := func(f insightFail) (gen.ListGuardrailStatusResponseObject, error) {
		return gen.ListGuardrailStatusdefaultJSONResponse{StatusCode: f.status, Body: f.body}, nil
	}
	a, ok := s.insights(func(a *InsightsArea) bool { return a.Guardrails != nil })
	if !ok {
		return fail(insightNotBuilt())
	}
	var repo int64
	if req.Params.RepositoryId != nil {
		repo = *req.Params.RepositoryId
	}
	rows, err := a.Guardrails.Status(ctx, repo)
	if err != nil {
		return fail(s.insightErr("listGuardrailStatus", err))
	}
	out := gen.GuardrailStatusList{Items: make([]gen.GuardrailStatus, 0, len(rows))}
	for _, r := range rows {
		gs := gen.GuardrailStatus{Repository: r.Repository, RepositoryId: r.RepositoryID, Guardrails: make([]gen.GuardrailState, 0, len(r.Guardrails))}
		for _, e := range r.Guardrails {
			st := gen.GuardrailState{Guardrail: gen.Guardrail(e.Guardrail), State: gen.GuardrailStateState(e.State), Source: gen.GuardrailStateSource(e.Source)}
			if e.State == guardrails.Broken || e.Count > 0 {
				st.Count = ptrOf(e.Count)
			}
			if e.Reason != "" {
				st.Reason = ptr(e.Reason)
			}
			if !e.ReportedAt.IsZero() {
				st.ReportedAt = tptr(e.ReportedAt.UTC())
			}
			gs.Guardrails = append(gs.Guardrails, st)
		}
		if len(r.KnownCountByDay) > 0 {
			days := make([]gen.DayValue, 0, len(r.KnownCountByDay))
			for _, dc := range r.KnownCountByDay {
				if t, err := time.Parse("2006-01-02", dc.Day); err == nil {
					days = append(days, gen.DayValue{Day: dateOf(t), Value: float32(dc.Count)})
				}
			}
			gs.KnownCountByDay = &days
		}
		out.Items = append(out.Items, gs)
	}
	return gen.ListGuardrailStatus200JSONResponse(out), nil
}

// ListInsightDigests implements GET /api/v1/insights/digests (R5.38): the files stay local.
func (s *server) ListInsightDigests(ctx context.Context, _ gen.ListInsightDigestsRequestObject) (gen.ListInsightDigestsResponseObject, error) {
	fail := func(f insightFail) (gen.ListInsightDigestsResponseObject, error) {
		return gen.ListInsightDigestsdefaultJSONResponse{StatusCode: f.status, Body: f.body}, nil
	}
	a, ok := s.insights(func(a *InsightsArea) bool { return a.Digests != nil })
	if !ok {
		return fail(insightNotBuilt())
	}
	ds, err := a.Digests.Last(ctx, digestsShown)
	if err != nil {
		return fail(s.insightErr("listInsightDigests", err))
	}
	out := gen.InsightDigestList{Items: make([]gen.InsightDigest, 0, len(ds))}
	for _, d := range ds {
		out.Items = append(out.Items, gen.InsightDigest{Week: string(d.Week), Text: d.Text, WrittenAt: d.WrittenAt.UTC()})
	}
	return gen.ListInsightDigests200JSONResponse(out), nil
}

// GetRunSavings implements GET /api/v1/runs/{runId}/savings (R5.23 to R5.27).
func (s *server) GetRunSavings(ctx context.Context, req gen.GetRunSavingsRequestObject) (gen.GetRunSavingsResponseObject, error) {
	fail := func(f insightFail) (gen.GetRunSavingsResponseObject, error) {
		return gen.GetRunSavingsdefaultJSONResponse{StatusCode: f.status, Body: f.body}, nil
	}
	a, ok := s.insights(func(a *InsightsArea) bool { return a.Runs != nil })
	if !ok {
		return fail(insightNotBuilt())
	}
	attempt := 1
	if req.Params.Attempt != nil {
		if *req.Params.Attempt < 1 {
			return fail(insightBad("attempt must be 1 or more"))
		}
		attempt = *req.Params.Attempt
	}
	runID, perr := strconv.ParseInt(string(req.RunId), 10, 64)
	if perr != nil || runID < 1 {
		return fail(insightBad("the run id is not valid"))
	}
	sv, ws, reason, err := a.Runs.RunSavings(ctx, runID, attempt)
	if err != nil {
		if errors.Is(err, ErrInsightNotFound) {
			return fail(insightMissing("no such run"))
		}
		return fail(s.insightErr("getRunSavings", err))
	}
	if len(sv) > 3 {
		sv = sv[:3]
	}
	out := gen.RunSavings{Savings: make([]gen.RunSaving, 0, len(sv)), Waste: make([]gen.RunWaste, 0, len(ws))}
	for _, v := range sv {
		c := gen.RunSavingChange(v.Change)
		out.Savings = append(out.Savings, gen.RunSaving{Job: v.Job, Change: &c, Minutes: float32(v.Sooner.Minutes()), Words: v.Words})
	}
	for _, w := range ws {
		out.Waste = append(out.Waste, gen.RunWaste{Kind: gen.RunWasteKind(w.Kind), Minutes: float32(w.Minutes), Words: w.Words})
	}
	if reason != "" {
		out.Reason = ptr(reason)
	}
	return gen.GetRunSavings200JSONResponse(out), nil
}

// ListWorkflowChecks implements GET /api/v1/repositories/{repositoryId}/workflow-checks (R5.29, R5.30).
func (s *server) ListWorkflowChecks(ctx context.Context, req gen.ListWorkflowChecksRequestObject) (gen.ListWorkflowChecksResponseObject, error) {
	fail := func(f insightFail) (gen.ListWorkflowChecksResponseObject, error) {
		return gen.ListWorkflowChecksdefaultJSONResponse{StatusCode: f.status, Body: f.body}, nil
	}
	a, ok := s.insights(func(a *InsightsArea) bool { return a.Workflows != nil })
	if !ok {
		return fail(insightNotBuilt())
	}
	var (
		sha string
		res []workflows.Result
		err error
	)
	if req.Params.HeadSha != nil && *req.Params.HeadSha != "" {
		sha = *req.Params.HeadSha
		res, err = a.Workflows.ForCommit(ctx, req.RepositoryId, sha)
	} else {
		sha, res, err = a.Workflows.Latest(ctx, req.RepositoryId)
	}
	if err != nil {
		return fail(s.insightErr("listWorkflowChecks", err))
	}
	out := gen.WorkflowCheckList{Items: make([]gen.WorkflowCheck, 0, len(res))}
	if sha != "" {
		out.HeadSha = ptr(sha)
	}
	for _, r := range res {
		c := gen.WorkflowCheck{CheckId: gen.WorkflowCheckCheckId(r.Check), Status: gen.WorkflowCheckStatus(r.Status)}
		if r.Job != "" {
			c.Job = ptr(r.Job)
		}
		if r.Line > 0 {
			c.Line = ptr(r.Line)
		}
		if r.Detail != "" {
			c.Detail = ptr(r.Detail)
		}
		if r.Guardrail != "" {
			g := gen.Guardrail(r.Guardrail)
			c.Guardrail = &g
		}
		out.Items = append(out.Items, c)
	}
	return gen.ListWorkflowChecks200JSONResponse(out), nil
}

// GetJobResources implements GET /api/v1/jobs/{jobId}/resources (R5.19, R5.20).
func (s *server) GetJobResources(ctx context.Context, req gen.GetJobResourcesRequestObject) (gen.GetJobResourcesResponseObject, error) {
	fail := func(f insightFail) (gen.GetJobResourcesResponseObject, error) {
		return gen.GetJobResourcesdefaultJSONResponse{StatusCode: f.status, Body: f.body}, nil
	}
	a, ok := s.insights(func(a *InsightsArea) bool { return a.Resources != nil })
	if !ok {
		return fail(insightNotBuilt())
	}
	samples, sum, err := a.Resources.Job(ctx, req.JobId)
	if err != nil {
		if errors.Is(err, ErrInsightNotFound) {
			return fail(insightMissing("no such job"))
		}
		return fail(s.insightErr("getJobResources", err))
	}
	out := gen.JobResources{Samples: make([]gen.ResourceSample, 0, len(samples))}
	for _, sm := range samples {
		out.Samples = append(out.Samples, gen.ResourceSample{At: sm.At.UTC(), CpuCores: float32(sm.CPUMillis) / 1000, MemoryBytes: sm.MemoryBytes, Known: ptrOf(sm.Known)})
	}
	if sum.UnknownReason != "" {
		out.UnknownReason = ptr(sum.UnknownReason)
	} else if sum.Samples > 0 {
		out.CpuP95Cores = ptr(float32(sum.CPUP95Millis) / 1000)
		out.CpuPeakCores = ptr(float32(sum.CPUPeakMillis) / 1000)
		out.MemoryP95Bytes = ptr(sum.MemP95)
		out.MemoryPeakBytes = ptr(sum.MemPeak)
	}
	return gen.GetJobResources200JSONResponse(out), nil
}
