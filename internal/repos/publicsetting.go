package repos

import (
	"context"
	"fmt"
	"strings"

	"github.com/luis-mc/ci-runners-shared-pool/internal/auth"
)

// The public repositories setting (github-integration R5.54 to R5.58): crsp
// offers private repositories by default. The setting lives in
// internal/settings; this package reads it through Config.PublicRepositories.

// Problem codes and action ids of a repository (R5.56).
const (
	ProblemPublicPoolOnWhileOff = "public_pool_on_while_off"
	ActionTurnOnPublic          = "turn_on_public_repositories"
	ActionTurnOffPool           = "turn_off_pool"
)

// ProblemAction is one thing the owner can do about a Problem.
type ProblemAction struct{ ID, Label string }

// Problem is a fault of one repository the owner can fix.
type Problem struct {
	Code    string
	Text    string
	Actions []ProblemAction
}

// PublicOffError refuses to start serving a public repository while the
// setting is off (R5.55). The API answers 409 public_repositories_off.
type PublicOffError struct{ Repo string }

func (e *PublicOffError) Error() string {
	return e.Repo + " is a public repository, and the setting Public repositories is off, so crsp does not serve it. Turn the setting on first."
}

// PublicAllowed reports whether public repositories are on. Without a setting
// source they are off.
func (s *Service) PublicAllowed(ctx context.Context) (bool, error) {
	if s.cfg.PublicRepositories == nil {
		return false, nil
	}
	return s.cfg.PublicRepositories(ctx)
}

// RequirePublicAllowed refuses a public repository while the setting is off.
// A private or internal repository always passes. A setting that cannot be read
// refuses (fail closed).
func (s *Service) RequirePublicAllowed(ctx context.Context, r Repository) error {
	if r.Visibility != "public" {
		return nil
	}
	ok, err := s.PublicAllowed(ctx)
	if err != nil {
		return err
	}
	if !ok {
		return &PublicOffError{Repo: r.FullName}
	}
	return nil
}

// RequirePublicAllowedByID is RequirePublicAllowed for a stored repository; an
// unknown id passes (the caller refuses it for its own reason).
func (s *Service) RequirePublicAllowedByID(ctx context.Context, id int64) error {
	r, err := s.cfg.Store.GetRepository(ctx, id)
	if err != nil {
		return nil //nolint:nilerr // an unknown repository is not a public one; the caller refuses it
	}
	return s.RequirePublicAllowed(ctx, r)
}

// publicOnPool lists the public repositories whose pool is on.
func (s *Service) publicOnPool(ctx context.Context) ([]Repository, error) {
	on := true
	rs, err := s.cfg.Store.ListRepositories(ctx, ListFilter{Pool: &on})
	if err != nil {
		return nil, err
	}
	var out []Repository
	for _, r := range rs {
		if r.Visibility == "public" {
			out = append(out, r)
		}
	}
	return out, nil
}

// PublicOnPool counts the public repositories whose pool is on now.
func (s *Service) PublicOnPool(ctx context.Context) (int, error) {
	rs, err := s.publicOnPool(ctx)
	return len(rs), err
}

// StopPublicOnPool is what switching the setting off does (R5.55): for every
// public repository whose pool is on, the pool goes off (policy kept), the
// CRSP_RUNS_ON variables take the hosted labels, the runners are drained and
// removed, and the audit log says so. It returns how many repositories it
// stopped. Every repository is tried; the errors are joined.
func (s *Service) StopPublicOnPool(ctx context.Context, p auth.Principal, why string) (int, error) {
	rs, err := s.publicOnPool(ctx)
	if err != nil {
		return 0, err
	}
	var failed []string
	stopped := 0
	for _, r := range rs {
		done, err := s.stopPublic(ctx, p, r, why)
		if done {
			stopped++
		}
		if err != nil {
			failed = append(failed, fmt.Sprintf("%s: %s", r.FullName, publicError(err)))
		}
	}
	if len(failed) > 0 {
		return stopped, fmt.Errorf("public repositories are off, but not everything stopped: %s", strings.Join(failed, "; "))
	}
	return stopped, nil
}

// problemOf is the fault of a repository, or nil: a public repository on the
// pool while public repositories are off (R5.56).
func (s *Service) problemOf(ctx context.Context, r Repository) *Problem {
	if r.Visibility != "public" || !r.PoolOn {
		return nil
	}
	if ok, err := s.PublicAllowed(ctx); err != nil || ok {
		return nil
	}
	return &Problem{
		Code: ProblemPublicPoolOnWhileOff,
		Text: r.FullName + " is public and still on the pool, but the setting Public repositories is off. It keeps working until you act.",
		Actions: []ProblemAction{
			{ID: ActionTurnOnPublic, Label: "Turn on public repositories"},
			{ID: ActionTurnOffPool, Label: "Turn off the pool"},
		},
	}
}
