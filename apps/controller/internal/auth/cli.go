package auth

import (
	"context"
	"errors"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/github"
)

// CLITokenLifetime is how long a CLI token lives (R5.15).
const CLITokenLifetime = 30 * 24 * time.Hour

// CLITokenPrefix starts every CLI token, so the redactor and the secret
// scanners can find one.
const CLITokenPrefix = "crsp_cli_"

// CLIExchange trades a device-flow GitHub user token for a controller CLI
// token (R5.14, R5.15). GitHub is asked once whether the token is for this
// App; the user must be on the list; the GitHub token is dropped. The CLI
// token is returned once; only its hash is kept.
func (s *Service) CLIExchange(ctx context.Context, githubToken, name string) (string, CLIToken, error) {
	if githubToken == "" {
		return "", CLIToken{}, ErrBadCode
	}
	gu, err := s.cfg.GitHub.CheckUserToken(ctx, github.UserToken{Value: githubToken})
	// The GitHub token is not kept past this call: it is never stored or logged.
	if err != nil {
		s.audit(ctx, "anonymous", "auth.cli_failed", "cli_token", "", "token refused", "failed")
		return "", CLIToken{}, err
	}
	u, err := s.store.GetUser(ctx, gu.ID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			s.audit(ctx, gu.Login, "auth.cli_failed", "cli_token", gu.Login, "not on the list", "failed")
			return "", CLIToken{}, &NotAllowedError{Login: gu.Login}
		}
		return "", CLIToken{}, err
	}
	if name == "" {
		name = "cli"
	}
	raw := CLITokenPrefix + RandomToken(32)
	now := s.cfg.Now()
	t := CLIToken{ID: "ct_" + RandomToken(6), Hash: Hash(raw), UserID: u.GitHubID, UserLogin: u.Login,
		Name: name, CreatedAt: now, ExpiresAt: now.Add(CLITokenLifetime)}
	if err := s.store.InsertCLIToken(ctx, t); err != nil {
		return "", CLIToken{}, err
	}
	s.auditUser(ctx, Principal{UserID: u.GitHubID, Login: u.Login, Source: "cli"}, "auth.cli_token_made", "cli_token", t.ID, name)
	return raw, t, nil
}

// AuthenticateCLI turns a CLI token into a Principal that carries the user's
// current role.
func (s *Service) AuthenticateCLI(ctx context.Context, token string) (Principal, error) {
	if token == "" {
		return Principal{}, ErrUnauthenticated
	}
	t, u, err := s.store.GetCLIToken(ctx, Hash(token), s.cfg.Now())
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return Principal{}, ErrUnauthenticated
		}
		return Principal{}, err
	}
	_ = s.store.TouchCLIToken(ctx, t.ID, s.cfg.Now())
	return Principal{UserID: u.GitHubID, Login: u.Login, Role: u.Role, Source: "cli", TokenID: t.ID}, nil
}

// ListCLITokens lists the user's own tokens; the owner sees everyone's.
func (s *Service) ListCLITokens(ctx context.Context, p Principal) ([]CLIToken, error) {
	if err := Require(p, RoleViewer); err != nil {
		return nil, err
	}
	if p.Role == RoleOwner {
		return s.store.ListCLITokens(ctx, 0)
	}
	return s.store.ListCLITokens(ctx, p.UserID)
}

// RevokeCLIToken revokes a token. The owner or the token's user may.
func (s *Service) RevokeCLIToken(ctx context.Context, p Principal, id string) error {
	if err := Require(p, RoleViewer); err != nil {
		return err
	}
	t, err := s.store.GetCLITokenByID(ctx, id)
	if err != nil {
		return err
	}
	if p.Role != RoleOwner && t.UserID != p.UserID {
		return ErrForbidden
	}
	if err := s.store.RevokeCLIToken(ctx, id, s.cfg.Now()); err != nil {
		return err
	}
	s.auditUser(ctx, p, "auth.cli_token_revoked", "cli_token", id, "")
	return nil
}
