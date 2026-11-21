package timeline

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/insights"
)

// MinFollowUpWait is the shortest runner wait of a follow-up job the timeline draws as a segment of its own.
const MinFollowUpWait = time.Minute

// MaxRange is the longest window getPoolTimeline answers (R5.21).
const MaxRange = 24 * time.Hour

// RunnerIdle is one span in which an online crsp runner sat idle, read from runner states.
type RunnerIdle struct {
	RunnerName  string
	MachineName string
	Labels      []string
	Start, End  time.Time
}

// RunnerStates reads the idle spans of crsp runners that overlap a window (implemented over the runner
// state history by the controller wiring; this package has no store import).
type RunnerStates interface {
	IdleSpans(ctx context.Context, w insights.Window) ([]RunnerIdle, error)
}

// followUpEvent is the event of a run that another run's completion started.
const followUpEvent = "workflow_run"

type builder struct {
	jobs   insights.JobSource
	states RunnerStates
}

// NewBuilder returns the Builder over a JobSource and the runner states.
func NewBuilder(jobs insights.JobSource, states RunnerStates) Builder {
	return builder{jobs: jobs, states: states}
}

// waitSpan is the span in which a job waited for a runner: created to started (or completed when it never
// started, or the window end while it still waits). ok is false when the start is unknown.
func waitSpan(j insights.JobFact, end time.Time) (s, e time.Time, ok bool) {
	if j.CreatedAt.IsZero() {
		return s, e, false
	}
	switch {
	case !j.StartedAt.IsZero():
		e = j.StartedAt
	case !j.CompletedAt.IsZero():
		e = j.CompletedAt
	default:
		e = end
	}
	if !e.After(j.CreatedAt) {
		return s, e, false
	}
	return j.CreatedAt, e, true
}

func overlap(a1, a2, b1, b2 time.Time) time.Duration {
	s, e := a1, a2
	if b1.After(s) {
		s = b1
	}
	if b2.Before(e) {
		e = b2
	}
	if !e.After(s) {
		return 0
	}
	return e.Sub(s)
}

// canTake says whether a runner with labels can take a job that asked for want (every wanted label is on
// the runner, case-insensitive).
func canTake(runner, want []string) bool {
	have := map[string]bool{}
	for _, l := range runner {
		have[strings.ToLower(l)] = true
	}
	for _, l := range want {
		if !have[strings.ToLower(l)] {
			return false
		}
	}
	return true
}

// LabelKey is the stable key of a label set.
func LabelKey(labels []string) string {
	c := make([]string, len(labels))
	for i, l := range labels {
		c[i] = strings.ToLower(l)
	}
	sort.Strings(c)
	return strings.Join(c, ",")
}

func (b builder) Timeline(ctx context.Context, w insights.Window) ([]Lane, error) {
	if !w.To.After(w.From) || w.To.Sub(w.From) > MaxRange {
		return nil, errors.New("timeline: range must be positive and at most 24 hours")
	}
	// Jobs created up to a day before the window may still run inside it.
	all, err := b.jobs.JobsBetween(ctx, insights.Window{From: w.From.Add(-MaxRange), To: w.To}, 0)
	if err != nil {
		return nil, fmt.Errorf("timeline: read jobs: %w", err)
	}
	idles, err := b.states.IdleSpans(ctx, w)
	if err != nil {
		return nil, fmt.Errorf("timeline: read runner states: %w", err)
	}
	// Waiting jobs, whole list, for markers.
	var waiting []insights.JobFact
	for _, j := range all {
		if s, e, ok := waitSpan(j, w.To); ok && overlap(s, e, w.From, w.To) > 0 {
			waiting = append(waiting, j)
		}
	}
	lanes := map[string]*Lane{}
	lane := func(name string) *Lane {
		l := lanes[name]
		if l == nil {
			l = &Lane{RunnerName: name}
			lanes[name] = l
		}
		return l
	}
	for _, j := range all {
		if j.RunnerName == "" || j.StartedAt.IsZero() {
			continue
		}
		end := j.CompletedAt
		if end.IsZero() {
			end = w.To
		}
		if overlap(j.StartedAt, end, w.From, w.To) == 0 {
			continue
		}
		l := lane(j.RunnerName)
		if l.Origin == insights.OriginUnknown || j.Origin == insights.OriginCrsp {
			l.Origin = j.Origin
		}
		if j.MachineID != "" && l.MachineName == "" {
			l.MachineName = j.MachineID
		}
		if len(l.Labels) == 0 {
			l.Labels = j.RunnerLabels
		}
		seg := Segment{Kind: SegmentJob, Start: j.StartedAt, End: end, JobID: j.ID, RunID: j.RunID, Repository: j.Repository,
			JobIdent: j.JobIdent, Event: j.Event, FollowUp: j.Event == followUpEvent}
		if seg.FollowUp {
			// the runner wait of a follow-up run (the Installers wait after CI) is part of the run's timeline
			if ws, we, ok := waitSpan(j, w.To); ok && we.Sub(ws) >= MinFollowUpWait && overlap(ws, we, w.From, w.To) > 0 {
				l.Segments = append(l.Segments, Segment{Kind: SegmentWait, Start: ws, End: we, JobID: j.ID, RunID: j.RunID,
					FollowUp: true, Repository: j.Repository, JobIdent: j.JobIdent, Event: j.Event})
			}
		}
		if j.Origin == insights.OriginCrsp && j.Event == "push" && j.DefaultBranch {
			for _, o := range waiting {
				if o.Event != "pull_request" || o.ID == j.ID {
					continue
				}
				if s, e, _ := waitSpan(o, w.To); overlap(s, e, j.StartedAt, end) > 0 {
					seg.WaitingJobIDs = append(seg.WaitingJobIDs, o.ID)
				}
			}
			if len(seg.WaitingJobIDs) > 0 {
				seg.Markers = []Marker{AfterMergeWork}
			}
		}
		l.Segments = append(l.Segments, seg)
	}
	for _, g := range idles {
		l := lane(g.RunnerName)
		l.Origin = insights.OriginCrsp
		if g.MachineName != "" {
			l.MachineName = g.MachineName
		}
		if len(g.Labels) > 0 {
			l.Labels = g.Labels
		}
		seg := Segment{Kind: SegmentIdle, Start: g.Start, End: g.End}
		for _, o := range waiting {
			if s, e, _ := waitSpan(o, w.To); overlap(s, e, g.Start, g.End) > 0 && canTake(g.Labels, o.Labels) {
				seg.WaitingJobIDs = append(seg.WaitingJobIDs, o.ID)
			}
		}
		if len(seg.WaitingJobIDs) > 0 {
			seg.Markers = []Marker{IdleWhileWaiting}
		}
		l.Segments = append(l.Segments, seg)
	}
	out := make([]Lane, 0, len(lanes))
	for _, l := range lanes {
		sort.SliceStable(l.Segments, func(i, k int) bool { return l.Segments[i].Start.Before(l.Segments[k].Start) })
		out = append(out, *l)
	}
	sort.Slice(out, func(i, k int) bool {
		if out[i].Origin != out[k].Origin {
			return out[i].Origin < out[k].Origin
		}
		return out[i].RunnerName < out[k].RunnerName
	})
	return out, nil
}
