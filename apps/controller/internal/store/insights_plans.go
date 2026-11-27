package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/insights"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/insights/actions"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/store/sqlcgen"
)

// InsightPlans is actions.PlanStore over the insight_plans table (R5.10).
type InsightPlans struct{ s *Store }

var _ actions.PlanStore = InsightPlans{}

// InsightPlans returns the plan store.
func (s *Store) InsightPlans() InsightPlans { return InsightPlans{s: s} }

type planChangeJSON struct {
	Target        string   `json:"target"`
	TargetName    string   `json:"target_name"`
	Field         string   `json:"field"`
	From          int      `json:"from"`
	To            int      `json:"to"`
	Words         string   `json:"words"`
	JobsWaitedFor []string `json:"jobs_waited_for,omitempty"`
}

type planResultJSON struct {
	Target string `json:"target"`
	Field  string `json:"field"`
	State  string `json:"state"`
	Error  string `json:"error,omitempty"`
}

func planTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func parsePlanTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// Insert stores a new draft plan.
func (p InsightPlans) Insert(ctx context.Context, pl actions.Plan) error {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	changes := make([]planChangeJSON, len(pl.Changes))
	for i, c := range pl.Changes {
		changes[i] = planChangeJSON{c.Target, c.TargetName, c.Field, c.From, c.To, c.Words, c.JobsWaitedFor}
	}
	b, err := json.Marshal(changes)
	if err != nil {
		return err
	}
	if err := p.s.q.InsertInsightPlan(ctx, sqlcgen.InsertInsightPlanParams{
		ID: pl.ID, FindingID: pl.FindingID, ActionKind: string(pl.Action), Changes: string(b),
		CreatedBy: pl.CreatedBy, CreatedAt: planTime(pl.CreatedAt),
	}); err != nil {
		return fmt.Errorf("insert insight plan: %w", err)
	}
	return nil
}

// Get returns a plan; actions.ErrNotFound when absent.
func (p InsightPlans) Get(ctx context.Context, id string) (actions.Plan, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	row, err := p.s.q.GetInsightPlan(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return actions.Plan{}, actions.ErrNotFound
	}
	if err != nil {
		return actions.Plan{}, fmt.Errorf("get insight plan: %w", err)
	}
	var cj []planChangeJSON
	var rj []planResultJSON
	if err := json.Unmarshal([]byte(row.Changes), &cj); err != nil {
		return actions.Plan{}, fmt.Errorf("insight plan changes: %w", err)
	}
	if err := json.Unmarshal([]byte(row.Results), &rj); err != nil {
		return actions.Plan{}, fmt.Errorf("insight plan results: %w", err)
	}
	pl := actions.Plan{
		ID: row.ID, FindingID: row.FindingID, Action: insights.ActionKind(row.ActionKind),
		State: actions.State(row.State), CreatedBy: row.CreatedBy, CreatedAt: parsePlanTime(row.CreatedAt),
		StartedAt: parsePlanTime(row.StartedAt), FinishedAt: parsePlanTime(row.FinishedAt),
	}
	for _, c := range cj {
		pl.Changes = append(pl.Changes, actions.Change{Target: c.Target, TargetName: c.TargetName, Field: c.Field,
			From: c.From, To: c.To, Words: c.Words, JobsWaitedFor: c.JobsWaitedFor})
	}
	for _, r := range rj {
		pl.Results = append(pl.Results, actions.ChangeResult{Target: r.Target, Field: r.Field, State: r.State, Error: r.Error})
	}
	if len(pl.Results) == 0 {
		for _, c := range pl.Changes {
			pl.Results = append(pl.Results, actions.ChangeResult{Target: c.Target, Field: c.Field, State: "pending"})
		}
	}
	return pl, nil
}

// Update stores the state and results of a plan.
func (p InsightPlans) Update(ctx context.Context, pl actions.Plan) error {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	rj := make([]planResultJSON, len(pl.Results))
	for i, r := range pl.Results {
		rj[i] = planResultJSON{r.Target, r.Field, r.State, r.Error}
	}
	b, err := json.Marshal(rj)
	if err != nil {
		return err
	}
	if err := p.s.q.UpdateInsightPlanState(ctx, sqlcgen.UpdateInsightPlanStateParams{
		State: string(pl.State), Results: string(b), StartedAt: planTime(pl.StartedAt),
		FinishedAt: planTime(pl.FinishedAt), ID: pl.ID,
	}); err != nil {
		return fmt.Errorf("update insight plan: %w", err)
	}
	return nil
}
