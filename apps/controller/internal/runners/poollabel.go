package runners

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/github"
)

// The pool rename moves every runner from one pool label to another with the
// runner label API of GitHub, so no runner is registered again and no job is
// cut (runners R5.178 to R5.181). A runner record keeps the labels it was made
// with; the rename keeps the record and GitHub the same.

// LabelHolder is a runner record that carries a label.
type LabelHolder struct {
	ID         string
	Name       string
	Registered bool // GitHub holds its labels: a persistent registration
	Ephemeral  bool
}

func hasLabel(labels []string, want string) bool {
	for _, l := range labels {
		if strings.EqualFold(strings.TrimSpace(l), want) {
			return true
		}
	}
	return false
}

// LabelHolders lists the runner records that carry label, by name.
func (s *Service) LabelHolders(ctx context.Context, label string) ([]LabelHolder, error) {
	all, err := s.Store.ListRunners(ctx, Filter{})
	if err != nil {
		return nil, err
	}
	var out []LabelHolder
	for _, r := range all {
		if r.State == Removing || !hasLabel(r.Labels, label) {
			continue
		}
		out = append(out, LabelHolder{ID: r.ID, Name: r.Name, Registered: r.Kind == "persistent" && r.GitHubRunnerID != 0, Ephemeral: r.Kind != "persistent"})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// githubLabel runs fn on GitHub for a registered persistent runner.
func (s *Service) githubLabel(ctx context.Context, r Record, fn func(github.RunnerLabeler, Target) error) error {
	if r.Kind != "persistent" || r.GitHubRunnerID == 0 {
		return nil // a pool runner has no registration of its own; a job runner is read-only
	}
	if s.Labeler == nil {
		return fmt.Errorf("the runner %s cannot be relabelled: GitHub is not connected", r.Name)
	}
	sc, err := s.registrationScope(ctx, r.Scope)
	if err != nil {
		return fmt.Errorf("resolve scope: %w", err)
	}
	t, err := s.Resolver.Resolve(ctx, sc)
	if err != nil {
		return fmt.Errorf("resolve scope: %w", err)
	}
	return fn(s.Labeler, t)
}

// AddPoolLabel puts label on the runner at GitHub and in its record. Doing it
// again changes nothing. A runner that is gone is not an error: there is
// nothing left to relabel.
func (s *Service) AddPoolLabel(ctx context.Context, id, label, actor string) error {
	r, err := s.Store.GetRunner(ctx, id)
	if err != nil {
		if err == ErrNotFound {
			return nil
		}
		return err
	}
	if err := s.githubLabel(ctx, r, func(l github.RunnerLabeler, t Target) error {
		return l.AddRunnerLabels(ctx, t.InstallationID, t.Scope, r.GitHubRunnerID, []string{label})
	}); err != nil {
		return err
	}
	if !hasLabel(r.Labels, label) {
		if err := s.Store.SetRunnerLabels(ctx, id, append(append([]string(nil), r.Labels...), label)); err != nil {
			return err
		}
	}
	s.audit(ctx, actor, "runner.label_added", r.MachineID, r.ID, label)
	return nil
}

// RemovePoolLabel takes label off the runner at GitHub and from its record.
// A label or runner that is already gone is not an error.
func (s *Service) RemovePoolLabel(ctx context.Context, id, label, actor string) error {
	r, err := s.Store.GetRunner(ctx, id)
	if err != nil {
		if err == ErrNotFound {
			return nil
		}
		return err
	}
	if err := s.githubLabel(ctx, r, func(l github.RunnerLabeler, t Target) error {
		return l.RemoveRunnerLabel(ctx, t.InstallationID, t.Scope, r.GitHubRunnerID, label)
	}); err != nil {
		return err
	}
	var keep []string
	for _, l := range r.Labels {
		if !strings.EqualFold(strings.TrimSpace(l), label) {
			keep = append(keep, l)
		}
	}
	if len(keep) != len(r.Labels) {
		if err := s.Store.SetRunnerLabels(ctx, id, keep); err != nil {
			return err
		}
	}
	s.audit(ctx, actor, "runner.label_removed", r.MachineID, r.ID, label)
	return nil
}
