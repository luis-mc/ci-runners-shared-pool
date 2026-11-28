package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api/gen"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/jobsdata"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/scheduler"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/scheduler/slots"
)

// The jobs and slot routes: listJobs, getJob, listSlots, freeStaleSlots
// (observability R5.1, R5.7; scheduling R5.37, R5.58).

const (
	defaultJobLimit = 25
	maxJobLimit     = 100
	historyLen      = 30
)

func errBody(code, msg string) gen.ErrorEnvelope {
	return gen.ErrorEnvelope{Error: gen.ErrorBody{Code: code, Message: msg}}
}

// notWired is the answer while the scheduler is not set up in Options: the route exists, nothing serves it.
func notWired() gen.ErrorEnvelope { return notImplemented() }

// waitReason maps a scheduler wait reason to the contract's. A lost race means
// no slot was free, so it shows as free_slot.
func waitReason(r string) *gen.WaitReason {
	if r == "" {
		return nil
	}
	var out gen.WaitReason
	switch r {
	case "free_slot", "lost_race":
		out = gen.WaitReasonFreeSlot
	case "reserved":
		out = gen.WaitReasonReserved
	case "load_over_budget":
		out = gen.WaitReasonLoadOverBudget
	case "no_runner_with_labels":
		out = gen.WaitReasonNoRunnerWithLabels
	case "repository_parallel_limit":
		out = gen.WaitReasonRepositoryParallelLimit
	case "machine_drained":
		out = gen.WaitReasonMachineDrained
	case "load_not_measured":
		out = gen.WaitReasonLoadNotMeasured
	case "paused":
		out = gen.WaitReasonPaused
	case "on_battery":
		out = gen.WaitReasonOnBattery
	case "over_memory":
		out = gen.WaitReasonOverMemory
	default:
		out = gen.WaitReasonUnknown
	}
	return &out
}

func ptr[T comparable](v T) *T {
	var zero T
	if v == zero {
		return nil
	}
	return &v
}

func tptr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

func secs(a, b time.Time) *int {
	if a.IsZero() || b.IsZero() || b.Before(a) {
		return nil
	}
	n := int(b.Sub(a).Seconds())
	return &n
}

func jobResult(conclusion string) *gen.JobResult {
	var r gen.JobResult
	switch conclusion {
	case "success":
		r = gen.JobResultSuccess
	case "failure":
		r = gen.JobResultFailure
	case "cancelled":
		r = gen.JobResultCancelled
	case "timed_out":
		r = gen.JobResultTimedOut
	case "":
		return nil
	default:
		r = gen.JobResultLost
	}
	return &r
}

func isolation(v string) *gen.Isolation {
	if v == "" {
		return nil
	}
	i := gen.Isolation(v)
	return &i
}

func toJob(j slots.JobRecord) gen.Job {
	out := gen.Job{
		Id: j.ID, Repository: j.Repository, RepositoryId: ptr(j.RepositoryID), RunId: j.RunID, RunAttempt: j.RunAttempt,
		GithubJobId: j.ID, Workflow: ptr(j.Workflow), JobName: j.JobName, HeadSha: ptr(j.HeadSHA), Trigger: ptr(j.Trigger),
		Labels: j.Labels, State: gen.JobState(j.State), MachineId: ptr(j.MachineID), RunnerName: ptr(j.RunnerName), Backend: ptr(j.Backend), Isolation: isolation(j.Isolation),
		CreatedAt: tptr(j.CreatedAt), QueuedAt: tptr(j.QueuedAt), SlotAt: tptr(j.SlotAt), StartedAt: tptr(j.StartedAt),
		CompletedAt: tptr(j.CompletedAt), GithubWaitSeconds: secs(j.CreatedAt, j.StartedAt),
		SlotWaitSeconds: secs(j.QueuedAt, j.SlotAt), RunSeconds: secs(j.StartedAt, j.CompletedAt), Result: jobResult(j.Conclusion),
	}
	if out.Labels == nil {
		out.Labels = []string{}
	}
	if j.State == slots.JobQueued {
		out.WaitReason, out.WaitText = waitReason(j.WaitReason), ptr(j.WaitText)
	}
	if j.Repository != "" {
		out.HtmlUrl = ptr(jobPage(j))
	}
	return out
}

// jobQuery is the filter every Jobs route takes (listJobs, exportJobs, getJobStats), read from the query.
type jobQuery struct {
	State        *string
	Result       *string
	RepositoryID *int64
	MachineID    *string
	Backend      *string
	Runner       *string
	RunnerKind   *string
	JobType      *string
	JobKey       *string
	Branch       *string
	RunID        *int64
	From, To     *time.Time
}

// jobFilter validates a query and gives the filter of the store; msg is the 400 text, empty when it is valid.
func (q jobQuery) jobFilter() (f jobsdata.JobFilter, msg string) {
	if q.State != nil {
		switch jobsdata.State(*q.State) {
		case jobsdata.Queued, jobsdata.Running, jobsdata.Done:
			f.State = jobsdata.State(*q.State)
		default:
			return f, "state must be queued, running or done"
		}
	}
	if q.RunnerKind != nil {
		switch jobsdata.RunnerKind(*q.RunnerKind) {
		case jobsdata.Persistent, jobsdata.Ephemeral:
			f.RunnerKind = jobsdata.RunnerKind(*q.RunnerKind)
		default:
			return f, "runner_kind must be persistent or ephemeral"
		}
	}
	setIf(&f.RepositoryID, q.RepositoryID)
	setIf(&f.MachineID, q.MachineID)
	setIf(&f.Backend, q.Backend)
	setIf(&f.RunnerName, q.Runner)
	setIf(&f.JobType, q.JobType)
	setIf(&f.JobKey, q.JobKey)
	setIf(&f.Branch, q.Branch)
	setIf(&f.RunID, q.RunID)
	setIf(&f.From, q.From)
	setIf(&f.To, q.To)
	if q.Result != nil {
		f.Result = jobsdata.Result(*q.Result)
	}
	return f, ""
}

func setIf[T any](dst *T, src *T) {
	if src != nil {
		*dst = *src
	}
}

// legacy is the filter of the scheduler's job table, which has no runner, job key or branch.
func (q jobQuery) legacy() (slots.JobFilter, string) {
	var f slots.JobFilter
	if q.State != nil {
		switch slots.JobState(*q.State) {
		case slots.JobQueued, slots.JobRunning, slots.JobDone:
			f.State = slots.JobState(*q.State)
		default:
			return f, "state must be queued, running or done"
		}
	}
	setIf(&f.RepositoryID, q.RepositoryID)
	setIf(&f.MachineID, q.MachineID)
	setIf(&f.Backend, q.Backend)
	setIf(&f.From, q.From)
	setIf(&f.To, q.To)
	if q.Result != nil {
		f.Result = *q.Result
	}
	return f, ""
}

func queryOf(p gen.ListJobsParams) jobQuery {
	q := jobQuery{RepositoryID: p.RepositoryId, MachineID: p.MachineId, Backend: p.Backend, Runner: p.Runner, JobType: p.JobType, JobKey: p.JobIdent,
		Branch: p.Branch, RunID: p.RunId, From: p.From, To: p.To}
	if p.State != nil {
		q.State = ptrOf(string(*p.State))
	}
	if p.Result != nil {
		q.Result = ptrOf(string(*p.Result))
	}
	if p.RunnerKind != nil {
		q.RunnerKind = ptrOf(string(*p.RunnerKind))
	}
	return q
}

// pageLimit reads the page size: 25 by default, 1 to 100.
func pageLimit(p *int) (int, bool) {
	if p == nil {
		return defaultJobLimit, true
	}
	return *p, *p >= 1 && *p <= maxJobLimit
}

// cursorID reads a cursor such as "j123" or "r123".
func cursorID(c *string, prefix string) (int64, bool) {
	if c == nil || *c == "" {
		return 0, true
	}
	n, err := strconv.ParseInt(strings.TrimPrefix(*c, prefix), 10, 64)
	return n, err == nil && n >= 1
}

// ListJobs implements GET /api/v1/jobs: newest first, keyset-paged on the job id.
func (s *server) ListJobs(ctx context.Context, req gen.ListJobsRequestObject) (gen.ListJobsResponseObject, error) {
	fail := func(status int, code, msg string) (gen.ListJobsResponseObject, error) {
		return gen.ListJobsdefaultJSONResponse{StatusCode: status, Body: errBody(code, msg)}, nil
	}
	if s.opts.JobsData == nil && s.opts.Jobs == nil {
		return fail(http.StatusNotImplemented, CodeNotImplemented, "this operation is not built yet")
	}
	limit, ok := pageLimit(req.Params.Limit)
	if !ok {
		return fail(http.StatusBadRequest, CodeBadRequest, "limit must be 1 to 100")
	}
	after, ok := cursorID(req.Params.Cursor, "j")
	if !ok {
		return fail(http.StatusBadRequest, CodeBadRequest, "the cursor is not valid")
	}
	q := queryOf(req.Params)
	if s.opts.JobsData == nil {
		return s.listJobsLegacy(ctx, q, limit, after)
	}
	f, msg := q.jobFilter()
	if msg != "" {
		return fail(http.StatusBadRequest, CodeBadRequest, msg)
	}
	f.Limit, f.After = limit, after
	rows, total, err := s.opts.JobsData.ListJobs(ctx, f)
	if err != nil {
		s.opts.Logger.Error("list jobs", "err", err)
		return fail(http.StatusInternalServerError, CodeInternal, "internal error")
	}
	page := gen.JobPage{Items: []gen.Job{}, Stale: gen.Staleness{}, Total: &total}
	if len(rows) > limit {
		rows = rows[:limit]
		page.NextCursor = ptr("j" + strconv.FormatInt(rows[len(rows)-1].ID, 10))
	}
	infos := s.runnerInfos(ctx, jobIDs(rows))
	if f.State == jobsdata.Queued || f.State == jobsdata.Running {
		// Queue and Running list only jobs crsp can run (owner decision D7).
		rows = slices.DeleteFunc(rows, func(r jobsdata.Job) bool { return infos[r.ID].elsewhere() })
	}
	items, err := s.jobItems(ctx, rows)
	if err != nil {
		s.opts.Logger.Error("list jobs", "err", err)
		return fail(http.StatusInternalServerError, CodeInternal, "internal error")
	}
	for i := range items {
		if in, ok := infos[items[i].Id]; ok {
			items[i] = withRunnerInfo(items[i], in)
		}
	}
	page.Items = items
	return gen.ListJobs200JSONResponse(page), nil
}

// jobItems maps jobs with their forecast and queue place.
func (s *server) jobItems(ctx context.Context, rows []jobsdata.Job) ([]gen.Job, error) {
	fc := newForecaster(s.opts.JobsData, s.now())
	if err := fc.prepare(ctx, rows); err != nil {
		return nil, err
	}
	fcs, err := fc.forJobs(ctx, rows)
	if err != nil {
		return nil, err
	}
	qi, err := s.queueFor(ctx, fc, rows)
	if err != nil {
		return nil, err
	}
	flags := s.flagsFor(ctx, rows)
	items := make([]gen.Job, 0, len(rows))
	for _, r := range rows {
		q, hasQ := qi[r.ID]
		it := jobWithForecast(r, fcs[r.ID], q, hasQ)
		flags.markJob(&it)
		items = append(items, it)
	}
	return items, nil
}

// listJobsLegacy serves the page from the scheduler's table while the Jobs section data is not wired.
func (s *server) listJobsLegacy(ctx context.Context, q jobQuery, limit int, after int64) (gen.ListJobsResponseObject, error) {
	f, msg := q.legacy()
	if msg != "" {
		return gen.ListJobsdefaultJSONResponse{StatusCode: http.StatusBadRequest, Body: errBody(CodeBadRequest, msg)}, nil
	}
	f.Limit, f.After = limit, after
	rows, err := s.opts.Jobs.ListJobs(ctx, f)
	if err != nil {
		s.opts.Logger.Error("list jobs", "err", err)
		return gen.ListJobsdefaultJSONResponse{StatusCode: http.StatusInternalServerError, Body: errBody(CodeInternal, "internal error")}, nil
	}
	page := gen.JobPage{Items: []gen.Job{}, Stale: gen.Staleness{}}
	if len(rows) > f.Limit {
		rows = rows[:f.Limit]
		page.NextCursor = ptr("j" + strconv.FormatInt(rows[len(rows)-1].ID, 10))
	}
	for _, r := range rows {
		page.Items = append(page.Items, toJob(r))
	}
	return gen.ListJobs200JSONResponse(page), nil
}

// jobDetailBody is the job detail answer: the job's fields with the extra ones beside them (gen.JobDetail, which
// the generator flattens; a test keeps the two in step).
type jobDetailBody struct {
	gen.Job
	Steps    []gen.StepItem     `json:"steps"`
	History  []gen.HistoryPoint `json:"history"`
	Attempts []gen.AttemptRef   `json:"attempts"`
	RunEta   *gen.Eta           `json:"run_eta,omitempty"`
}

type jobDetailResponse jobDetailBody

func (r jobDetailResponse) VisitGetJobResponse(w http.ResponseWriter) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	return json.NewEncoder(w).Encode(jobDetailBody(r))
}

// GetJob implements GET /api/v1/jobs/{jobId}.
func (s *server) GetJob(ctx context.Context, req gen.GetJobRequestObject) (gen.GetJobResponseObject, error) {
	fail := func(status int, code, msg string) (gen.GetJobResponseObject, error) {
		return gen.GetJobdefaultJSONResponse{StatusCode: status, Body: errBody(code, msg)}, nil
	}
	if s.opts.JobsData == nil && s.opts.Jobs == nil {
		return fail(http.StatusNotImplemented, CodeNotImplemented, "this operation is not built yet")
	}
	if s.opts.JobsData == nil {
		j, ok, err := s.opts.Jobs.GetJob(ctx, req.JobId)
		if err != nil {
			s.opts.Logger.Error("get job", "err", err)
			return fail(http.StatusInternalServerError, CodeInternal, "internal error")
		}
		if !ok {
			return fail(http.StatusNotFound, CodeNotFound, "no such job")
		}
		return jobDetailResponse{Job: toJob(j), Steps: []gen.StepItem{}, History: []gen.HistoryPoint{}, Attempts: []gen.AttemptRef{}}, nil
	}
	d, ok, err := s.opts.JobsData.GetJob(ctx, req.JobId)
	if err != nil {
		s.opts.Logger.Error("get job", "err", err)
		return fail(http.StatusInternalServerError, CodeInternal, "internal error")
	}
	if !ok {
		return fail(http.StatusNotFound, CodeNotFound, "no such job")
	}
	body, err := s.jobDetail(ctx, d)
	if err != nil {
		s.opts.Logger.Error("get job", "err", err)
		return fail(http.StatusInternalServerError, CodeInternal, "internal error")
	}
	if in, ok := s.runnerInfos(ctx, []int64{body.Id})[body.Id]; ok {
		body.Job = withRunnerInfo(body.Job, in)
	}
	return jobDetailResponse(body), nil
}

func jobIDs(rows []jobsdata.Job) []int64 {
	ids := make([]int64, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.ID)
	}
	return ids
}

// jobDetail builds the detail: the job with its forecast and queue place, its steps with a forecast where step
// samples exist, the last 30 runs of its key, its attempts and the ETA of its run.
func (s *server) jobDetail(ctx context.Context, d jobsdata.JobDetail) (jobDetailBody, error) {
	r := s.opts.JobsData
	fc := newForecaster(r, s.now())
	jf, err := fc.forJob(ctx, d.Job)
	if err != nil {
		return jobDetailBody{}, err
	}
	qi, err := s.queueFor(ctx, fc, []jobsdata.Job{d.Job})
	if err != nil {
		return jobDetailBody{}, err
	}
	q, hasQ := qi[d.ID]
	body := jobDetailBody{Job: jobWithForecast(d.Job, jf, q, hasQ), Steps: []gen.StepItem{}, Attempts: toAttempts(d.Attempts)}
	body.Needs = ptrOf(d.Needs)
	if d.Needs == nil {
		body.Needs = ptrOf([]string{})
	}
	for _, st := range d.Steps {
		item := toStepItem(st)
		sf, ok, err := fc.forStep(ctx, d.RepositoryID, d.JobKey, st.Name)
		if err != nil {
			return jobDetailBody{}, err
		}
		if ok {
			g := toForecast(sf)
			item.Forecast = &g
		}
		body.Steps = append(body.Steps, item)
	}
	hist, err := r.History(ctx, d.RepositoryID, d.JobKey, historyLen)
	if err != nil {
		return jobDetailBody{}, err
	}
	body.History = toHistory(hist)
	if d.State != jobsdata.Done {
		run, ok, err := r.GetRun(ctx, d.RunID, d.Attempt)
		if err != nil {
			return jobDetailBody{}, err
		}
		if ok && run.State != jobsdata.Done {
			v, err := s.buildRun(ctx, fc, run)
			if err != nil {
				return jobDetailBody{}, err
			}
			body.RunEta = v.Eta
		}
	}
	return body, nil
}

func (s *server) machineKnown(ctx context.Context, id string) (bool, error) {
	if s.opts.Jobs == nil {
		return true, nil
	}
	return s.opts.Jobs.MachineExists(ctx, id)
}

func toSlot(h scheduler.HeldSlot) gen.Slot {
	return gen.Slot{Kind: gen.SlotKind(h.Kind), Number: h.Number, TakenAt: h.TakenAt, Repository: h.Job.Repository,
		RunId: h.Job.RunID, RunAttempt: h.Job.RunAttempt, Job: h.Job.Job, RunnerId: ptr(h.Job.RunnerName), StaleReason: ptr(h.StaleReason)}
}

// ListSlots implements GET /api/v1/machines/{machineId}/slots (scheduling R5.58).
func (s *server) ListSlots(ctx context.Context, req gen.ListSlotsRequestObject) (gen.ListSlotsResponseObject, error) {
	if s.opts.Slots == nil {
		return gen.ListSlotsdefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: notWired()}, nil
	}
	if ok, err := s.machineKnown(ctx, req.MachineId); err != nil {
		s.opts.Logger.Error("list slots", "err", err)
		return gen.ListSlotsdefaultJSONResponse{StatusCode: http.StatusInternalServerError, Body: errBody(CodeInternal, "internal error")}, nil
	} else if !ok {
		return gen.ListSlotsdefaultJSONResponse{StatusCode: http.StatusNotFound, Body: errBody(CodeNotFound, "no such machine")}, nil
	}
	st, err := s.opts.Slots.Status(ctx, req.MachineId)
	if err != nil {
		s.opts.Logger.Error("list slots", "err", err)
		return gen.ListSlotsdefaultJSONResponse{StatusCode: http.StatusInternalServerError, Body: errBody(CodeInternal, "internal error")}, nil
	}
	out := gen.SlotStatus{MachineId: req.MachineId, Slots: []gen.Slot{}, Tickets: []gen.Ticket{}}
	for _, h := range st.Slots {
		out.Slots = append(out.Slots, toSlot(h))
	}
	for _, t := range st.Tickets {
		out.Tickets = append(out.Tickets, gen.Ticket{Id: t.GateID, Repository: t.Job.Repository, RunId: t.Job.RunID,
			RunAttempt: t.Job.RunAttempt, Job: t.Job.Job, Priority: ptr(t.Priority), EventClass: ptr(t.Class), QueuedAt: t.QueuedAt,
			BeatAt: tptr(t.BeatAt), ExpiresAt: tptr(t.ExpiresAt), Place: t.Place, WaitReason: waitReason(string(t.Reason))})
	}
	return gen.ListSlots200JSONResponse(out), nil
}

// FreeStaleSlots implements POST /api/v1/machines/{machineId}/slots/free-stale
// (scheduling R5.37). Stale slots are freed at once. A live slot is freed only
// when named and confirmed; without confirm the answer names the job and asks for a yes.
func (s *server) FreeStaleSlots(ctx context.Context, req gen.FreeStaleSlotsRequestObject) (gen.FreeStaleSlotsResponseObject, error) {
	fail := func(status int, code, msg string) gen.FreeStaleSlotsResponseObject {
		return gen.FreeStaleSlotsdefaultJSONResponse{StatusCode: status, Body: errBody(code, msg)}
	}
	if s.opts.Slots == nil {
		return fail(http.StatusNotImplemented, CodeNotImplemented, "this operation is not built yet"), nil
	}
	if ok, err := s.machineKnown(ctx, req.MachineId); err != nil {
		s.opts.Logger.Error("free stale", "err", err)
		return fail(http.StatusInternalServerError, CodeInternal, "internal error"), nil
	} else if !ok {
		return fail(http.StatusNotFound, CodeNotFound, "no such machine"), nil
	}
	var live []scheduler.Grant
	if req.Body != nil && req.Body.Slots != nil && len(*req.Body.Slots) > 0 {
		st, err := s.opts.Slots.Status(ctx, req.MachineId)
		if err != nil {
			s.opts.Logger.Error("free stale", "err", err)
			return fail(http.StatusInternalServerError, CodeInternal, "internal error"), nil
		}
		confirmed := req.Body.Confirm != nil && *req.Body.Confirm
		for _, want := range *req.Body.Slots {
			var held *scheduler.HeldSlot
			for i := range st.Slots {
				if string(st.Slots[i].Kind) == string(want.Kind) && st.Slots[i].Number == want.Number {
					held = &st.Slots[i]
				}
			}
			if held == nil {
				return fail(http.StatusNotFound, CodeNotFound, fmt.Sprintf("slot %s %d is not held", want.Kind, want.Number)), nil
			}
			if held.StaleReason == "" && !confirmed {
				return fail(http.StatusConflict, CodeNeedsConfirmation, fmt.Sprintf(
					"slot %s %d is held by a live job: %s run %d job %s; send confirm to free it",
					want.Kind, want.Number, held.Job.Repository, held.Job.RunID, held.Job.Job)), nil
			}
			live = append(live, scheduler.Grant{Kind: held.Kind, Number: held.Number})
		}
	}
	freed, err := s.opts.Slots.FreeStale(ctx, req.MachineId, live)
	if err != nil {
		s.opts.Logger.Error("free stale", "err", err)
		return fail(http.StatusInternalServerError, CodeInternal, "internal error"), nil
	}
	out := gen.FreedSlotList{Items: []struct {
		Reason string   `json:"reason"`
		Slot   gen.Slot `json:"slot"`
	}{}}
	for _, f := range freed {
		if f.Number == 0 {
			continue // a dropped ticket is not a slot
		}
		out.Items = append(out.Items, struct {
			Reason string   `json:"reason"`
			Slot   gen.Slot `json:"slot"`
		}{Reason: f.Reason, Slot: gen.Slot{Kind: gen.SlotKind(f.Kind), Number: f.Number, TakenAt: f.TakenAt, Repository: f.Job.Repository,
			RunId: f.Job.RunID, RunAttempt: f.Job.RunAttempt, Job: f.Job.Job, RunnerId: ptr(f.Job.RunnerName)}})
	}
	return gen.FreeStaleSlots200JSONResponse(out), nil
}
