// Package logsapi adapts the log store to the API's log search port
// (observability R5.21). It is its own package so that internal/api and
// internal/logs do not import each other.
package logsapi

import (
	"context"
	"errors"
	"fmt"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/logs"
)

// Searcher is api.LogSearcher over a logs.Store.
type Searcher struct{ Store logs.Store }

var _ api.LogSearcher = Searcher{}

// SearchLogs implements api.LogSearcher.
func (s Searcher) SearchLogs(ctx context.Context, q api.LogQuery) ([]api.LogEntry, int64, error) {
	lq := logs.Query{Text: q.Text, Machine: q.Machine, Runner: q.Runner, Job: q.Job, From: q.From, To: q.To, Limit: q.Limit, BeforeID: q.BeforeID}
	if q.Level != "" {
		lv, err := logs.ParseLevel(q.Level)
		if err != nil {
			return nil, 0, fmt.Errorf("%w: %v", api.ErrBadLogQuery, err)
		}
		lq.MinLevel = &lv
	}
	if err := lq.Validate(); err != nil {
		return nil, 0, errors.Join(api.ErrBadLogQuery, err)
	}
	page, err := s.Store.Search(ctx, lq)
	if err != nil {
		return nil, 0, err
	}
	out := make([]api.LogEntry, 0, len(page.Lines))
	for _, l := range page.Lines {
		out = append(out, api.LogEntry{ID: l.ID, At: l.At, Level: logs.LevelName(l.Level), Machine: l.Machine, Runner: l.Runner, Job: l.Job, Component: l.Component, Message: l.Message, Attrs: l.Attrs})
	}
	return out, page.NextBefore, nil
}
