package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/api/gen"
)

// LogQuery is one log search (observability R5.21). The API declares its own
// port, like the other areas (STACKSPEC 3.2); internal/logs/logsapi adapts the
// log database to it.
type LogQuery struct {
	Text, Machine, Runner, Job, Level string
	From, To                          time.Time
	Limit                             int
	// BeforeID is the cursor of the previous page; 0 starts at the newest line.
	BeforeID int64
}

// LogEntry is one line of a result.
type LogEntry struct {
	ID                                              int64
	At                                              time.Time
	Level, Machine, Runner, Job, Component, Message string
	Attrs                                           string
}

// ErrBadLogQuery marks a query the user can fix; its text is shown.
var ErrBadLogQuery = errors.New("bad log query")

// LogSearcher is the log database as the API sees it. nil answers 501.
type LogSearcher interface {
	// SearchLogs returns one page, newest first, and the cursor of the next (0 on the last page).
	SearchLogs(ctx context.Context, q LogQuery) ([]LogEntry, int64, error)
}

// SearchLogs implements GET /api/v1/logs (admin and owner only: x-auth admin).
func (s *server) SearchLogs(ctx context.Context, req gen.SearchLogsRequestObject) (gen.SearchLogsResponseObject, error) {
	fail := func(f opsFailure) (gen.SearchLogsResponseObject, error) {
		return gen.SearchLogsdefaultJSONResponse{StatusCode: f.status, Body: f.body}, nil
	}
	if s.opts.Logs == nil {
		return fail(unavailable())
	}
	p := req.Params
	str := func(v *string) string {
		if v == nil {
			return ""
		}
		return *v
	}
	q := LogQuery{Text: str(p.Q), Machine: str(p.Machine), Runner: str(p.Runner), Job: str(p.Job)}
	if p.Level != nil {
		q.Level = string(*p.Level)
	}
	if p.From != nil {
		q.From = *p.From
	}
	if p.To != nil {
		q.To = *p.To
	}
	if p.Limit != nil {
		q.Limit = *p.Limit
	}
	if p.Cursor != nil && *p.Cursor != "" {
		id, err := strconv.ParseInt(*p.Cursor, 10, 64)
		if err != nil || id < 1 {
			return fail(envelope(http.StatusBadRequest, CodeBadRequest, "the cursor is not one this server gave"))
		}
		q.BeforeID = id
	}
	items, next, err := s.opts.Logs.SearchLogs(ctx, q)
	if err != nil {
		if errors.Is(err, ErrBadLogQuery) {
			return fail(envelope(http.StatusBadRequest, CodeBadRequest, err.Error()))
		}
		s.opts.Logger.Error("log search", "err", err)
		return fail(envelope(http.StatusInternalServerError, CodeInternal, "the log search failed"))
	}
	out := gen.LogPage{Items: make([]gen.LogLine, 0, len(items))}
	for _, e := range items {
		l := gen.LogLine{Id: e.ID, At: e.At, Level: gen.LogLineLevel(e.Level), Message: e.Message}
		set := func(dst **string, v string) {
			if v != "" {
				*dst = &v
			}
		}
		set(&l.Machine, e.Machine)
		set(&l.Runner, e.Runner)
		set(&l.Job, e.Job)
		set(&l.Component, e.Component)
		set(&l.Attrs, e.Attrs)
		out.Items = append(out.Items, l)
	}
	if next > 0 {
		c := strconv.FormatInt(next, 10)
		out.NextCursor = &c
	}
	return gen.SearchLogs200JSONResponse(out), nil
}
