package doctor

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api/gen"
)

// API converts a finding to the contract type.
func (f Finding) API() gen.DoctorFinding {
	out := gen.DoctorFinding{
		Check: f.Check, CheckedAt: f.CheckedAt, Milestone: f.Milestone, NeedsAdmin: f.NeedsAdmin,
		Severity: gen.Severity(f.Severity), Title: f.Title,
	}
	out.Subject.Kind, out.Subject.Id, out.Subject.Name = f.Subject.Kind, f.Subject.ID, f.Subject.Name
	if f.Detail != "" {
		d := f.Detail
		out.Detail = &d
	}
	out.Fix.Text = f.Fix.Text
	if f.Fix.Command != "" {
		c := f.Fix.Command
		out.Fix.Command = &c
	}
	if f.Fix.Link != "" {
		l := f.Fix.Link
		out.Fix.Link = &l
	}
	if len(f.Items) > 0 {
		items := make([]gen.RunnerLeftover, 0, len(f.Items))
		for _, it := range f.Items {
			x := gen.RunnerLeftover{Kind: gen.RunnerLeftoverKind(it.Kind), Name: it.Name, Why: it.Why}
			if it.Path != "" {
				p := it.Path
				x.Path = &p
			}
			if it.SizeBytes > 0 {
				n := it.SizeBytes
				x.SizeBytes = &n
			}
			if it.Account != "" {
				a := it.Account
				x.Account = &a
			}
			items = append(items, x)
		}
		out.Items = &items
	}
	if f.Action != nil {
		out.Action = &struct {
			Args  *gen.RegistrationActionArgs `json:"args,omitempty"`
			Id    gen.DoctorFindingActionId   `json:"id"`
			Label string                      `json:"label"`
		}{Id: gen.DoctorFindingActionId(f.Action.ID), Label: f.Action.Label}
		if a := f.Action.Args; a != nil {
			args := &gen.RegistrationActionArgs{}
			str := func(v string) *string {
				if v == "" {
					return nil
				}
				return &v
			}
			num := func(v int64) *int64 {
				if v == 0 {
					return nil
				}
				return &v
			}
			args.RunnerId, args.Name = str(a.RunnerID), str(a.Name)
			args.Owner, args.Repo, args.Organization = str(a.Owner), str(a.Repo), str(a.Organization)
			args.GithubRunnerId, args.InstallationId = num(a.GitHubRunnerID), num(a.InstallationID)
			out.Action.Args = args
		}
	}
	return out
}

// ScopeAPI converts a scope to the contract type; nil for an all-scope.
func (s Scope) ScopeAPI() *gen.DoctorScope {
	if s.IsAll() {
		return nil
	}
	out := &gen.DoctorScope{}
	if len(s.Machines) > 0 {
		m := append([]string(nil), s.Machines...)
		out.Machines = &m
	}
	if len(s.Repositories) > 0 {
		r := append([]int64(nil), s.Repositories...)
		out.Repositories = &r
	}
	if len(s.Checks) > 0 {
		c := append([]string(nil), s.Checks...)
		out.Checks = &c
	}
	return out
}

// ScopeFromAPI converts the request body.
func ScopeFromAPI(s *gen.DoctorScope) Scope {
	var out Scope
	if s == nil {
		return out
	}
	if s.Machines != nil {
		out.Machines = *s.Machines
	}
	if s.Repositories != nil {
		out.Repositories = *s.Repositories
	}
	if s.Checks != nil {
		out.Checks = *s.Checks
	}
	return out
}

// API converts a run to the contract type.
func (r Run) API() gen.DoctorRun {
	out := gen.DoctorRun{
		Id: r.ID, Trigger: gen.DoctorRunTrigger(r.Trigger), State: gen.DoctorRunState(r.State),
		Scope: r.Scope.ScopeAPI(), StartedAt: r.StartedAt, Counts: map[string]int{},
		Findings: make([]gen.DoctorFinding, 0, len(r.Findings)),
	}
	if !r.FinishedAt.IsZero() {
		t := r.FinishedAt
		out.FinishedAt = &t
	}
	if r.State != Running && r.Worst != "" {
		w := gen.Severity(r.Worst)
		out.Worst = &w
	}
	for s, n := range r.Counts {
		out.Counts[string(s)] = n
	}
	fs := append([]Finding(nil), r.Findings...)
	SortFindings(fs)
	for _, f := range fs {
		out.Findings = append(out.Findings, f.API())
	}
	return out
}

// ErrNotFound is returned for an unknown run.
var ErrNotFound = errors.New("doctor run not found")

// Service is the doctor as the API sees it. It implements the interface that
// internal/api declares.
type Service struct {
	Engine *Engine
}

// Run starts a run (operation runDoctor); a run of the same scope that is
// still going is joined (R5.8).
func (s *Service) Run(ctx context.Context, scope *gen.DoctorScope, trigger, by string) (gen.DoctorRun, error) {
	sc := ScopeFromAPI(scope)
	id, _, err := s.Engine.Start(ctx, sc, Trigger(trigger), by)
	if err != nil {
		return gen.DoctorRun{}, err
	}
	r, ok, err := s.Engine.Store.GetRun(ctx, id)
	if err != nil {
		return gen.DoctorRun{}, err
	}
	if !ok {
		return gen.DoctorRun{}, ErrNotFound
	}
	return r.API(), nil
}

// GetRun returns a run with its findings (operation getDoctorRun).
func (s *Service) GetRun(ctx context.Context, id string) (gen.DoctorRun, error) {
	r, ok, err := s.Engine.Store.GetRun(ctx, id)
	if err != nil {
		return gen.DoctorRun{}, err
	}
	if !ok {
		return gen.DoctorRun{}, ErrNotFound
	}
	return r.API(), nil
}

// Findings returns the latest finding per check and subject (operation listDoctorFindings).
func (s *Service) Findings(ctx context.Context, severity, group string) (gen.DoctorFindingList, error) {
	fs, last, err := s.Engine.Store.Latest(ctx, Severity(severity), Group(group))
	if err != nil {
		return gen.DoctorFindingList{}, err
	}
	out := gen.DoctorFindingList{Items: make([]gen.DoctorFinding, 0, len(fs))}
	for _, f := range fs {
		out.Items = append(out.Items, f.API())
	}
	if !last.IsZero() {
		t := last.Truncate(time.Millisecond)
		out.LastRunAt = &t
	}
	return out, nil
}

// SortedIDs returns the registered check IDs, for listings and tests.
func (r *Registry) SortedIDs() []string {
	ids := make([]string, 0, len(r.checks))
	for _, c := range r.checks {
		ids = append(ids, c.ID())
	}
	sort.Strings(ids)
	return ids
}

// IsNotFound says whether err means a doctor run does not exist.
func (s *Service) IsNotFound(err error) bool { return errors.Is(err, ErrNotFound) }
