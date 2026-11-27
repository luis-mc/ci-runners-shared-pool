package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/insights"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/insights/digest"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/store/sqlcgen"
)

// This file implements insights.FindingStore and insights.RunLog over queries/insights_findings.sql
// (docs/features/ci-run-insights R5.7 to R5.11, R6.3, R6.8). Times are stored in UTC (R6.9). There is no
// dismiss method: a finding closes only when a full successful pass stops reporting it (R5.8).

var (
	_ insights.FindingStore = (*InsightFindings)(nil)
	_ insights.RunLog       = (*InsightFindings)(nil)
)

const (
	maxFindingsPage     = 200
	defaultFindingsPage = 50
	// minEnoughJobs is the number of finished jobs under which the Overview says there is not enough data (UX-52).
	minEnoughJobs = 50
)

// InsightFindings is the findings store and the analyser run log.
type InsightFindings struct{ s *Store }

// InsightFindings returns the findings store of s.
func (s *Store) InsightFindings() *InsightFindings { return &InsightFindings{s: s} }

func findingFromRow(r sqlcgen.InsightFinding) insights.StoredFinding {
	ev := json.RawMessage(r.Evidence)
	if len(ev) == 0 {
		ev = json.RawMessage("{}")
	}
	return insights.StoredFinding{
		Finding: insights.Finding{
			Kind: insights.Kind(r.Kind), Scope: insights.Scope(r.Scope), RepositoryID: r.RepositoryID,
			Repository: r.Repository, Subject: r.Subject, Area: insights.Area(r.Area), Title: r.Title,
			Fix: r.Fix, Snippet: r.Snippet, Evidence: ev, MinutesSavedWeek: int(r.MinutesSavedWeek),
			Guardrail: insights.Guardrail(r.Guardrail), Action: insights.ActionKind(r.ActionKind),
		},
		ID: r.ID, FirstSeen: parseTime(r.FirstSeen), LastSeen: parseTime(r.LastSeen),
		ResolvedAt: parseTimeOpt(r.ResolvedAt), DismissedUntil: parseTimeOpt(r.HiddenUntil),
	}
}

func evidenceText(e json.RawMessage) string {
	if len(e) == 0 || !json.Valid(e) {
		return "{}"
	}
	return string(e)
}

// Sync records one successful pass: found findings are opened or refreshed, and the open findings of kinds
// that were not found are resolved, all in one transaction.
func (f *InsightFindings) Sync(ctx context.Context, kinds []insights.Kind, found []insights.Finding, now time.Time) (insights.SyncResult, error) {
	var res insights.SyncResult
	owned := make(map[insights.Kind]bool, len(kinds))
	for _, k := range kinds {
		owned[k] = true
	}
	for _, fd := range found {
		if !owned[fd.Kind] {
			return res, fmt.Errorf("insight sync: finding of kind %q is not owned by this pass", fd.Kind)
		}
	}
	nowText := fmtTime(now)
	err := f.s.WithTx(ctx, func(tx *Tx) error {
		res = insights.SyncResult{}
		for _, fd := range found {
			cur, err := tx.q.GetOpenInsightFinding(tx.ctx, sqlcgen.GetOpenInsightFindingParams{
				Kind: string(fd.Kind), Scope: string(fd.Scope), RepositoryID: fd.RepositoryID, Subject: fd.Subject})
			switch {
			case errors.Is(err, sql.ErrNoRows):
				if _, err := tx.q.InsertInsightFinding(tx.ctx, sqlcgen.InsertInsightFindingParams{
					Kind: string(fd.Kind), Scope: string(fd.Scope), RepositoryID: fd.RepositoryID,
					Repository: fd.Repository, Subject: fd.Subject, Area: string(fd.Area), Title: fd.Title, Fix: fd.Fix,
					Snippet: fd.Snippet, Evidence: evidenceText(fd.Evidence), MinutesSavedWeek: int64(max(fd.MinutesSavedWeek, 0)),
					Guardrail: string(fd.Guardrail), ActionKind: string(fd.Action), FirstSeen: nowText, LastSeen: nowText,
				}); err != nil {
					return fmt.Errorf("insert finding: %w", err)
				}
				res.Opened++
			case err != nil:
				return fmt.Errorf("get open finding: %w", err)
			default:
				if err := tx.q.UpdateInsightFindingSeen(tx.ctx, sqlcgen.UpdateInsightFindingSeenParams{
					Title: fd.Title, Fix: fd.Fix, Snippet: fd.Snippet, Evidence: evidenceText(fd.Evidence),
					MinutesSavedWeek: int64(max(fd.MinutesSavedWeek, 0)), ActionKind: string(fd.Action),
					LastSeen: nowText, ID: cur.ID,
				}); err != nil {
					return fmt.Errorf("update finding: %w", err)
				}
				res.Updated++
			}
		}
		// Every finding of this pass now has last_seen = now; an open one of an owned kind that is older was not found.
		for _, k := range kinds {
			n, err := tx.q.ResolveInsightFindingsNotSeen(tx.ctx, sqlcgen.ResolveInsightFindingsNotSeenParams{
				Now: nowText, Kind: string(k), SeenBefore: nowText})
			if err != nil {
				return fmt.Errorf("resolve findings: %w", err)
			}
			res.Resolved += int(n)
		}
		return nil
	})
	if err != nil {
		return insights.SyncResult{}, err
	}
	return res, nil
}

// List returns one ranked page (R5.11): open findings by minutes saved, then first seen, then id; resolved
// ones newest first. The cursor is the offset of the next page.
func (f *InsightFindings) List(ctx context.Context, fl insights.Filter) ([]insights.StoredFinding, int, string, error) {
	limit := fl.Limit
	if limit <= 0 {
		limit = defaultFindingsPage
	}
	limit = min(limit, maxFindingsPage)
	off := 0
	if fl.Cursor != "" {
		n, err := strconv.Atoi(fl.Cursor)
		if err != nil || n < 0 {
			return nil, 0, "", fmt.Errorf("insight list: bad cursor %q", fl.Cursor)
		}
		off = n
	}
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	var rows []sqlcgen.InsightFinding
	var total int64
	var err error
	if fl.Resolved {
		total, err = f.s.q.CountResolvedInsightFindingsFiltered(ctx, sqlcgen.CountResolvedInsightFindingsFilteredParams{
			RepositoryID: fl.RepositoryID, Area: string(fl.Area), Guardrail: string(fl.Guardrail)})
		if err == nil {
			rows, err = f.s.q.ListResolvedInsightFindings(ctx, sqlcgen.ListResolvedInsightFindingsParams{
				RepositoryID: fl.RepositoryID, Area: string(fl.Area), Guardrail: string(fl.Guardrail), Lim: int64(limit), Off: int64(off)})
		}
	} else {
		total, err = f.s.q.CountOpenInsightFindingsFiltered(ctx, sqlcgen.CountOpenInsightFindingsFilteredParams{
			RepositoryID: fl.RepositoryID, Area: string(fl.Area), Guardrail: string(fl.Guardrail), Now: fmtTime(time.Now())})
		if err == nil {
			rows, err = f.s.q.ListOpenInsightFindings(ctx, sqlcgen.ListOpenInsightFindingsParams{
				RepositoryID: fl.RepositoryID, Area: string(fl.Area), Guardrail: string(fl.Guardrail), Now: fmtTime(time.Now()), Lim: int64(limit), Off: int64(off)})
		}
	}
	if err != nil {
		return nil, 0, "", fmt.Errorf("list insight findings: %w", err)
	}
	items := make([]insights.StoredFinding, 0, len(rows))
	for _, r := range rows {
		items = append(items, findingFromRow(r))
	}
	next := ""
	if off+len(rows) < int(total) && len(rows) > 0 {
		next = strconv.Itoa(off + len(rows))
	}
	return items, int(total), next, nil
}

// Get returns one finding by id, open or resolved.
func (f *InsightFindings) Get(ctx context.Context, id int64) (insights.StoredFinding, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	r, err := f.s.q.GetInsightFinding(ctx, id)
	if err != nil {
		return insights.StoredFinding{}, fmt.Errorf("get insight finding %d: %w", id, err)
	}
	return findingFromRow(r), nil
}

// Summary counts the open findings and sums their minutes. EnoughData is false under 50 finished jobs in the
// last 7 days.
func (f *InsightFindings) Summary(ctx context.Context) (insights.Summary, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	c, err := f.s.q.CountOpenInsightFindings(ctx, fmtTime(time.Now()))
	if err != nil {
		return insights.Summary{}, fmt.Errorf("insight summary: %w", err)
	}
	var done int
	since := fmtTime(time.Now().Add(-7 * 24 * time.Hour))
	if err := f.s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM jobs WHERE state = 'done' AND completed_at >= ?`, since).Scan(&done); err != nil {
		return insights.Summary{}, fmt.Errorf("insight summary jobs: %w", err)
	}
	return insights.Summary{Open: int(c.Findings), MinutesSavedWeek: int(c.Minutes), EnoughData: done >= minEnoughJobs}, nil
}

var _ insights.Dismisser = (*InsightFindings)(nil)

// Dismiss hides one open finding until now + 7 days (D17). Sync keeps refreshing it, so it comes back after the
// time if its condition still holds, and is resolved as usual if it does not.
func (f *InsightFindings) Dismiss(ctx context.Context, id int64, now time.Time) (time.Time, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	until := now.UTC().Add(insights.DismissFor)
	n, err := f.s.q.DismissInsightFinding(ctx, sqlcgen.DismissInsightFindingParams{HiddenUntil: fmtTime(until), ID: id})
	if err != nil {
		return time.Time{}, fmt.Errorf("dismiss insight finding %d: %w", id, err)
	}
	if n == 0 {
		r, err := f.s.q.GetInsightFinding(ctx, id)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			return time.Time{}, insights.ErrNotFound
		case err != nil:
			return time.Time{}, fmt.Errorf("dismiss insight finding %d: %w", id, err)
		case r.ResolvedAt != "":
			return time.Time{}, insights.ErrResolved
		}
	}
	return until, nil
}

// Begin records the start of an analyser pass.
func (f *InsightFindings) Begin(ctx context.Context, analyser string, w insights.Window, at time.Time) (int64, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	id, err := f.s.q.InsertInsightAnalyserRun(ctx, sqlcgen.InsertInsightAnalyserRunParams{
		Analyser: analyser, WindowFrom: fmtTime(w.From), WindowTo: fmtTime(w.To), StartedAt: fmtTime(at)})
	if err != nil {
		return 0, fmt.Errorf("begin analyser run: %w", err)
	}
	return id, nil
}

// End records how a pass ended. errText must already be free of secrets (the engine redacts it).
func (f *InsightFindings) End(ctx context.Context, id int64, at time.Time, ok bool, found, resolved int, errText string) error {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	var okInt int64
	if ok {
		okInt = 1
	}
	if err := f.s.q.FinishInsightAnalyserRun(ctx, sqlcgen.FinishInsightAnalyserRunParams{
		FinishedAt: fmtTime(at), Ok: okInt, Found: int64(found), Resolved: int64(resolved), Error: errText, ID: id}); err != nil {
		return fmt.Errorf("end analyser run: %w", err)
	}
	return nil
}

// DigestFindings and DigestIndex feed the weekly digest (R5.38): findings by week and the insight_digests rows.

var (
	_ digest.Findings = (*InsightDigest)(nil)
	_ digest.Index    = (*InsightDigest)(nil)
)

// InsightDigest implements digest.Findings and digest.Index.
type InsightDigest struct{ s *Store }

// InsightDigest returns the digest store of s.
func (s *Store) InsightDigest() *InsightDigest { return &InsightDigest{s: s} }

func storedList(rows []sqlcgen.InsightFinding) []insights.StoredFinding {
	out := make([]insights.StoredFinding, 0, len(rows))
	for _, r := range rows {
		out = append(out, findingFromRow(r))
	}
	return out
}

// Open returns every open finding.
func (d *InsightDigest) Open(ctx context.Context) ([]insights.StoredFinding, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	rows, err := d.s.q.ListOpenInsightFindings(ctx, sqlcgen.ListOpenInsightFindingsParams{
		RepositoryID: int64(0), Area: "", Guardrail: "", Now: fmtTime(time.Now()), Lim: int64(-1), Off: int64(0)})
	if err != nil {
		return nil, fmt.Errorf("digest open findings: %w", err)
	}
	return storedList(rows), nil
}

// OpenedBetween returns the findings first seen in [from, to).
func (d *InsightDigest) OpenedBetween(ctx context.Context, from, to time.Time) ([]insights.StoredFinding, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	rows, err := d.s.q.ListInsightFindingsOpenedBetween(ctx, sqlcgen.ListInsightFindingsOpenedBetweenParams{
		FromAt: fmtTime(from), ToAt: fmtTime(to)})
	if err != nil {
		return nil, fmt.Errorf("digest opened findings: %w", err)
	}
	return storedList(rows), nil
}

// ResolvedBetween returns the findings resolved in [from, to).
func (d *InsightDigest) ResolvedBetween(ctx context.Context, from, to time.Time) ([]insights.StoredFinding, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	rows, err := d.s.q.ListInsightFindingsResolvedBetween(ctx, sqlcgen.ListInsightFindingsResolvedBetweenParams{
		FromAt: fmtTime(from), ToAt: fmtTime(to)})
	if err != nil {
		return nil, fmt.Errorf("digest resolved findings: %w", err)
	}
	return storedList(rows), nil
}

// Put records a written digest.
func (d *InsightDigest) Put(ctx context.Context, dg digest.Digest) error {
	ids := dg.TopIDs
	if ids == nil {
		ids = []int64{}
	}
	top, err := json.Marshal(ids)
	if err != nil {
		return fmt.Errorf("digest top: %w", err)
	}
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	if err := d.s.q.UpsertInsightDigest(ctx, sqlcgen.UpsertInsightDigestParams{
		Week: string(dg.Week), Path: dg.Path, Top: string(top), WrittenAt: fmtTime(dg.WrittenAt)}); err != nil {
		return fmt.Errorf("digest put: %w", err)
	}
	return nil
}

// Last returns the newest n digest rows, newest first, without their text.
func (d *InsightDigest) Last(ctx context.Context, n int) ([]digest.Digest, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	rows, err := d.s.q.ListInsightDigests(ctx, int64(n))
	if err != nil {
		return nil, fmt.Errorf("digest last: %w", err)
	}
	out := make([]digest.Digest, 0, len(rows))
	for _, r := range rows {
		var ids []int64
		_ = json.Unmarshal([]byte(r.Top), &ids)
		out = append(out, digest.Digest{Week: digest.Week(r.Week), Path: r.Path, TopIDs: ids, WrittenAt: parseTime(r.WrittenAt)})
	}
	return out, nil
}

// Delete removes a digest row.
func (d *InsightDigest) Delete(ctx context.Context, w digest.Week) error {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	if err := d.s.q.DeleteInsightDigest(ctx, string(w)); err != nil {
		return fmt.Errorf("digest delete: %w", err)
	}
	return nil
}
