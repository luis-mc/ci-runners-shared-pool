package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api/gen"
)

// The jobs export (web-console R5.24, UX-39): every row of the filter, in the order the list shows (newest
// first), never only one page. Text that looks like a formula is neutralised in the CSV. The file is named for
// what it holds, then the date and time.

// exportPage is the page size the export reads with; it walks every page of the filter, one page in memory at a time.
const exportPage = 500

// jobsExportColumns are the CSV columns, in order.
var jobsExportColumns = []string{
	"id", "repository", "run_id", "run_attempt", "workflow", "job_name", "state", "result", "wait_reason", "machine",
	"runner", "backend", "labels", "created_at", "started_at", "completed_at", "github_wait_seconds", "slot_wait_seconds",
	"run_seconds", "url", "job_ident", "branch", "runner_kind",
}

// jobsExportStream is an export answer written as it is read: the head goes out, then each page of rows as it is
// read, so the memory of an export is one page however many rows the filter holds (audit F4). A read that fails after
// the head is out cannot change the status, so it ends the connection (http.ErrAbortHandler): the download fails
// instead of ending as a file that looks whole.
type jobsExportStream struct {
	contentType string
	filename    string
	write       func(w io.Writer) error
	logger      *slog.Logger
}

func (e jobsExportStream) VisitExportJobsResponse(w http.ResponseWriter) error {
	w.Header().Set("Content-Type", e.contentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", e.filename))
	w.WriteHeader(http.StatusOK)
	bw := bufio.NewWriterSize(w, 64<<10)
	err := e.write(bw)
	if err == nil {
		err = bw.Flush()
	}
	if err != nil {
		e.logger.Error("export jobs: stream ended early", "err", err)
		panic(http.ErrAbortHandler)
	}
	return nil
}

// neutralise stops a spreadsheet from running a cell as a formula (UX-39).
func neutralise(v string) string {
	if v != "" && strings.ContainsRune("=+-@\t\r", rune(v[0])) {
		return "'" + v
	}
	return v
}

func timeText(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func intText(n *int) string {
	if n == nil {
		return ""
	}
	return strconv.Itoa(*n)
}

func jobRow(j gen.Job) []string {
	str := func(p *string) string {
		if p == nil {
			return ""
		}
		return *p
	}
	var result, reason string
	if j.Result != nil {
		result = string(*j.Result)
	}
	if j.WaitReason != nil {
		reason = string(*j.WaitReason)
	}
	var kind string
	if j.Runner != nil {
		kind = string(j.Runner.Kind)
	}
	return []string{
		strconv.FormatInt(j.Id, 10), j.Repository, strconv.FormatInt(j.RunId, 10), strconv.Itoa(j.RunAttempt), str(j.Workflow),
		j.JobName, string(j.State), result, reason, str(j.MachineId), str(j.RunnerName), str(j.Backend), strings.Join(j.Labels, " "),
		timeText(j.CreatedAt), timeText(j.StartedAt), timeText(j.CompletedAt), intText(j.GithubWaitSeconds), intText(j.SlotWaitSeconds),
		intText(j.RunSeconds), str(j.HtmlUrl), str(j.JobIdent), str(j.Branch), kind,
	}
}

// jobPager reads the jobs of a filter a page at a time, newest first, from the Jobs section data when it is wired and
// else from the scheduler's table. It never asks for the total.
type jobPager struct {
	next func() ([]gen.Job, bool, error) // the next page and whether more follow
}

func (s *server) exportPager(ctx context.Context, q jobQuery) jobPager {
	if s.opts.JobsData == nil {
		f, _ := q.legacy()
		f.Limit = exportPage
		return jobPager{next: func() ([]gen.Job, bool, error) {
			rows, err := s.opts.Jobs.ListJobs(ctx, f)
			if err != nil {
				return nil, false, err
			}
			more := len(rows) > f.Limit
			if more {
				rows = rows[:f.Limit]
			}
			out := make([]gen.Job, 0, len(rows))
			for _, r := range rows {
				out = append(out, toJob(r))
			}
			if more {
				f.After = rows[len(rows)-1].ID
			}
			return out, more, nil
		}}
	}
	f, _ := q.jobFilter()
	f.Limit, f.NoCount = exportPage, true
	return jobPager{next: func() ([]gen.Job, bool, error) {
		rows, _, err := s.opts.JobsData.ListJobs(ctx, f)
		if err != nil {
			return nil, false, err
		}
		more := len(rows) > f.Limit
		if more {
			rows = rows[:f.Limit]
		}
		out := make([]gen.Job, 0, len(rows))
		for _, r := range rows {
			out = append(out, toJobItem(r))
		}
		if more {
			f.After = rows[len(rows)-1].ID
		}
		return out, more, nil
	}}
}

// ExportJobs implements GET /api/v1/exports/jobs.
func (s *server) ExportJobs(ctx context.Context, req gen.ExportJobsRequestObject) (gen.ExportJobsResponseObject, error) {
	bad := func(msg string) (gen.ExportJobsResponseObject, error) {
		return gen.ExportJobsdefaultJSONResponse{StatusCode: http.StatusBadRequest, Body: errBody(CodeBadRequest, msg)}, nil
	}
	if s.opts.Jobs == nil && s.opts.JobsData == nil {
		return gen.ExportJobsdefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: notWired()}, nil
	}
	p := req.Params
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
	if _, msg := q.jobFilter(); msg != "" {
		return bad(msg)
	}
	format := gen.ExportJobsParamsFormat("csv")
	if p.Format != nil {
		format = *p.Format
	}
	if format != "csv" && format != "json" {
		return bad("format must be csv or json")
	}
	// The first page is read before the head goes out, so a store that is down answers 500, not a broken file.
	pager := s.exportPager(ctx, q)
	first, more, err := pager.next()
	if err != nil {
		s.opts.Logger.Error("export jobs", "err", err)
		return gen.ExportJobsdefaultJSONResponse{StatusCode: http.StatusInternalServerError, Body: errBody(CodeInternal, "internal error")}, nil
	}
	name := "crsp-jobs-" + s.now().Format(fileStamp) + "." + string(format)
	// walk gives each page to emit, the first one already read.
	walk := func(emit func([]gen.Job) error) error {
		page := first
		for {
			if err := emit(page); err != nil {
				return err
			}
			if !more {
				return nil
			}
			var err error
			if page, more, err = pager.next(); err != nil {
				return err
			}
		}
	}
	if format == "json" {
		return jobsExportStream{contentType: "application/json", filename: name, logger: s.opts.Logger, write: func(w io.Writer) error {
			if _, err := io.WriteString(w, "["); err != nil {
				return err
			}
			n := 0
			if err := walk(func(page []gen.Job) error {
				for _, j := range page {
					b, err := json.Marshal(j)
					if err != nil {
						return err
					}
					if n > 0 {
						b = append([]byte{','}, b...)
					}
					n++
					if _, err := w.Write(b); err != nil {
						return err
					}
				}
				return nil
			}); err != nil {
				return err
			}
			_, err := io.WriteString(w, "]")
			return err
		}}, nil
	}
	return jobsExportStream{contentType: "text/csv", filename: name, logger: s.opts.Logger, write: func(w io.Writer) error {
		cw := csv.NewWriter(w)
		_ = cw.Write(jobsExportColumns)
		if err := walk(func(page []gen.Job) error {
			for _, j := range page {
				row := jobRow(j)
				for i := range row {
					row[i] = neutralise(row[i])
				}
				_ = cw.Write(row)
			}
			cw.Flush()
			return cw.Error()
		}); err != nil {
			return err
		}
		cw.Flush()
		return cw.Error()
	}}, nil
}

// jobSummaryColumns are the CSV columns of the daily summaries, in order.
var jobSummaryColumns = []string{"day", "repository", "job_type", "jobs", "failed", "wait_p50_ms", "wait_p90_ms", "run_p50_ms", "run_p90_ms"}

type jobSummariesExportAnswer struct{ exportFile }

func (a jobSummariesExportAnswer) VisitExportJobSummariesResponse(w http.ResponseWriter) error {
	return a.write(w)
}

// ExportJobSummaries implements GET /api/v1/exports/job-summaries: every daily summary of the range as csv.
func (s *server) ExportJobSummaries(ctx context.Context, req gen.ExportJobSummariesRequestObject) (gen.ExportJobSummariesResponseObject, error) {
	if s.opts.JobsData == nil {
		return gen.ExportJobSummariesdefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: notWired()}, nil
	}
	rows, err := s.opts.JobsData.Summaries(ctx, timeOf(req.Params.From), timeOf(req.Params.To))
	if err != nil {
		s.opts.Logger.Error("export job summaries", "err", err)
		return gen.ExportJobSummariesdefaultJSONResponse{StatusCode: http.StatusInternalServerError, Body: errBody(CodeInternal, "internal error")}, nil
	}
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	_ = w.Write(jobSummaryColumns)
	for _, r := range rows {
		row := []string{r.Day, r.Repository, r.JobType, strconv.Itoa(r.Jobs), strconv.Itoa(r.Failed), strconv.FormatInt(r.WaitP50MS, 10),
			strconv.FormatInt(r.WaitP90MS, 10), strconv.FormatInt(r.RunP50MS, 10), strconv.FormatInt(r.RunP90MS, 10)}
		for i := range row {
			row[i] = neutralise(row[i])
		}
		_ = w.Write(row)
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return nil, err
	}
	return jobSummariesExportAnswer{exportFile{name: "crsp-job-summaries-" + s.now().Format(fileStamp) + ".csv", contentType: "text/csv", body: buf.Bytes()}}, nil
}

func timeOf(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}
