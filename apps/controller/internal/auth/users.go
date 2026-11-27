package auth

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/github"
)

// Require checks that p may do what min may do (R5.12). A route declares its
// minimum in x-auth; the handler calls this.
func Require(p Principal, min Role) error {
	if p.UserID == 0 {
		return ErrUnauthenticated
	}
	if !p.Role.AtLeast(min) {
		return ErrForbidden
	}
	return nil
}

// ListUsers is owner only.
func (s *Service) ListUsers(ctx context.Context, p Principal) ([]User, error) {
	if err := Require(p, RoleOwner); err != nil {
		return nil, err
	}
	return s.store.ListUsers(ctx)
}

// AddUser allows a GitHub login as admin or viewer; the login is resolved to
// a numeric id now (R5.11).
func (s *Service) AddUser(ctx context.Context, p Principal, login string, role Role) (User, error) {
	if err := Require(p, RoleOwner); err != nil {
		return User{}, err
	}
	if role != RoleAdmin && role != RoleViewer {
		return User{}, Invalid("role must be admin or viewer")
	}
	login = strings.TrimPrefix(strings.TrimSpace(login), "@")
	if login == "" {
		return User{}, Invalid("a GitHub login is needed")
	}
	gu, err := s.cfg.GitHub.ResolveLogin(ctx, login)
	if err != nil {
		if errors.Is(err, github.ErrNotFound) {
			return User{}, ErrNotFound
		}
		return User{}, err
	}
	u := User{GitHubID: gu.ID, Login: gu.Login, Role: role, AddedBy: p.Login, AddedAt: s.cfg.Now()}
	if err := s.store.InsertUser(ctx, u); err != nil {
		return User{}, err
	}
	s.auditUser(ctx, p, "user.added", "user", u.Login, "role "+string(role))
	return u, nil
}

// UpdateRole changes a role. Making someone owner hands the role over: it
// needs confirm, goes to an admin only, and the old owner becomes an admin.
// There is always exactly one owner (R5.10). Sessions of the changed users
// end (R5.13).
func (s *Service) UpdateRole(ctx context.Context, p Principal, id int64, role Role, confirm bool) (User, error) {
	if err := Require(p, RoleOwner); err != nil {
		return User{}, err
	}
	target, err := s.store.GetUser(ctx, id)
	if err != nil {
		return User{}, err
	}
	if !role.Valid() {
		return User{}, Invalid("unknown role")
	}
	switch {
	case target.Role == RoleOwner:
		return User{}, Invalid("the owner's role changes only by handing ownership to an admin")
	case role == RoleOwner:
		if target.Role != RoleAdmin {
			return User{}, Invalid("ownership goes to an admin; make them an admin first")
		}
		if !confirm {
			return User{}, ErrNeedsConfirm
		}
		if err := s.store.TransferOwner(ctx, p.UserID, id); err != nil {
			return User{}, err
		}
		_ = s.store.EndUserSessions(ctx, p.UserID)
		_ = s.store.EndUserSessions(ctx, id)
		s.auditUser(ctx, p, "user.ownership_handed", "user", target.Login, "from "+p.Login)
	default:
		if err := s.store.SetRole(ctx, id, role); err != nil {
			return User{}, err
		}
		_ = s.store.EndUserSessions(ctx, id)
		s.dropToken(id)
		s.auditUser(ctx, p, "user.role_changed", "user", target.Login, string(target.Role)+" to "+string(role))
	}
	return s.store.GetUser(ctx, id)
}

// RemoveUser removes a user and ends their sessions and CLI tokens. The owner
// cannot remove themselves.
func (s *Service) RemoveUser(ctx context.Context, p Principal, id int64) error {
	if err := Require(p, RoleOwner); err != nil {
		return err
	}
	u, err := s.store.GetUser(ctx, id)
	if err != nil {
		return err
	}
	if u.Role == RoleOwner {
		return Invalid("the owner cannot be removed")
	}
	now := s.cfg.Now()
	_ = s.store.EndUserSessions(ctx, id)
	s.dropToken(id)
	_ = s.store.RevokeUserCLITokens(ctx, id, now)
	if err := s.store.DeleteUser(ctx, id); err != nil {
		return err
	}
	s.auditUser(ctx, p, "user.removed", "user", u.Login, "id "+strconv.FormatInt(id, 10))
	return nil
}

// Me reads the current user's row.
func (s *Service) Me(ctx context.Context, p Principal) (User, error) {
	return s.store.GetUser(ctx, p.UserID)
}

// Themes the console offers (web-console 4.9).
var Themes = []string{"system", "dark", "light"}

// Theme reads the user's theme; "dark" when never set (owner decision D4: a new install is dark).
func (s *Service) Theme(ctx context.Context, p Principal) (string, error) {
	if err := Require(p, RoleViewer); err != nil {
		return "", err
	}
	t, err := s.store.GetTheme(ctx, p.UserID)
	if err != nil {
		return "", err
	}
	if t == "" {
		t = "dark"
	}
	return t, nil
}

// SetTheme saves the user's own theme.
func (s *Service) SetTheme(ctx context.Context, p Principal, theme string) error {
	if err := Require(p, RoleViewer); err != nil {
		return err
	}
	ok := false
	for _, t := range Themes {
		ok = ok || t == theme
	}
	if !ok {
		return Invalid("theme must be system, dark or light")
	}
	return s.store.SetTheme(ctx, p.UserID, theme)
}
