package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/auth"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/store/sqlcgen"
)

// AuthStore is the SQLite side of internal/auth (github-integration R5.1 to
// R5.16; tray R6.1): users, sessions, CLI tokens, one-time tray links, tray
// bindings and the audit rows of sign-in. It keeps hashes, never the values.
type AuthStore struct{ s *Store }

// Auth returns the auth.Store backed by this database.
func (s *Store) Auth() *AuthStore { return &AuthStore{s: s} }

var _ auth.Store = (*AuthStore)(nil)

func nf(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return auth.ErrNotFound
	}
	return err
}

func isUnique(err error) bool {
	return err != nil && (strings.Contains(err.Error(), "UNIQUE constraint") || strings.Contains(err.Error(), "constraint failed"))
}

func nullTime(n sql.NullString) time.Time {
	if !n.Valid {
		return time.Time{}
	}
	return ParseTime(n.String)
}

func toUser(u sqlcgen.User) auth.User {
	return auth.User{GitHubID: u.GithubUserID, Login: u.Login, Role: auth.Role(u.Role), AddedBy: u.AddedBy,
		AddedAt: ParseTime(u.AddedAt), LastSignIn: nullTime(u.LastSignInAt)}
}

func (a *AuthStore) GetApp(ctx context.Context) (auth.AppRecord, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	g, err := a.s.q.GetGitHubApp(ctx)
	if err != nil {
		return auth.AppRecord{}, nf(err)
	}
	perms := map[string]string{}
	_ = json.Unmarshal([]byte(g.Permissions), &perms)
	return auth.AppRecord{ID: g.ID, Slug: g.Slug, ClientID: g.ClientID, OwnerLogin: g.OwnerLogin, OwnerType: g.OwnerType,
		HTMLURL: g.HtmlUrl, WebhookActive: g.WebhookActive != 0, Permissions: perms}, nil
}

func (a *AuthStore) SaveApp(ctx context.Context, r auth.AppRecord) error {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	perms, _ := json.Marshal(r.Permissions)
	var wh int64
	if r.WebhookActive {
		wh = 1
	}
	return a.s.q.UpsertGitHubApp(ctx, sqlcgen.UpsertGitHubAppParams{ID: r.ID, Slug: r.Slug, ClientID: r.ClientID,
		OwnerLogin: r.OwnerLogin, OwnerType: r.OwnerType, HtmlUrl: r.HTMLURL, WebhookActive: wh, Permissions: string(perms)})
}

func (a *AuthStore) InsertSetupCode(ctx context.Context, hash string, expires time.Time) error {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	return a.s.q.InsertSetupCode(ctx, sqlcgen.InsertSetupCodeParams{CodeHash: hash, ExpiresAt: fmtTime(expires)})
}

func (a *AuthStore) UseSetupCode(ctx context.Context, hash string, _ time.Time) (bool, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	n, err := a.s.q.UseSetupCode(ctx, hash)
	return n > 0, err
}

func (a *AuthStore) CountUsers(ctx context.Context) (int, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	n, err := a.s.q.CountUsers(ctx)
	return int(n), err
}

func (a *AuthStore) GetUser(ctx context.Context, id int64) (auth.User, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	u, err := a.s.q.GetUser(ctx, id)
	if err != nil {
		return auth.User{}, nf(err)
	}
	return toUser(u), nil
}

func (a *AuthStore) GetOwner(ctx context.Context) (auth.User, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	u, err := a.s.q.GetOwner(ctx)
	if err != nil {
		return auth.User{}, nf(err)
	}
	return toUser(u), nil
}

func (a *AuthStore) ListUsers(ctx context.Context) ([]auth.User, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	rows, err := a.s.q.ListUsers(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]auth.User, 0, len(rows))
	for _, r := range rows {
		out = append(out, toUser(r))
	}
	return out, nil
}

func (a *AuthStore) InsertUser(ctx context.Context, u auth.User) error {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	err := a.s.q.InsertUser(ctx, sqlcgen.InsertUserParams{GithubUserID: u.GitHubID, Login: u.Login, Role: string(u.Role), AddedBy: u.AddedBy})
	if isUnique(err) {
		return auth.ErrConflict
	}
	return err
}

func (a *AuthStore) SetRole(ctx context.Context, id int64, r auth.Role) error {
	if r == auth.RoleOwner {
		return auth.ErrConflict // ownership moves only through TransferOwner
	}
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	n, err := a.s.q.UpdateUserRole(ctx, sqlcgen.UpdateUserRoleParams{Role: string(r), GithubUserID: id})
	if err != nil {
		return err
	}
	if n == 0 {
		return auth.ErrNotFound
	}
	return nil
}

func (a *AuthStore) TransferOwner(ctx context.Context, from, to int64) error {
	return a.s.WithTx(ctx, func(t *Tx) error {
		f, err := t.q.GetUser(t.ctx, from)
		if err != nil {
			return nf(err)
		}
		if f.Role != string(auth.RoleOwner) {
			return auth.ErrNotFound
		}
		if _, err := t.q.GetUser(t.ctx, to); err != nil {
			return nf(err)
		}
		if _, err := t.q.UpdateUserRole(t.ctx, sqlcgen.UpdateUserRoleParams{Role: string(auth.RoleAdmin), GithubUserID: from}); err != nil {
			return err
		}
		_, err = t.q.UpdateUserRole(t.ctx, sqlcgen.UpdateUserRoleParams{Role: string(auth.RoleOwner), GithubUserID: to})
		return err
	})
}

func (a *AuthStore) RecordSignIn(ctx context.Context, id int64, login string, _ time.Time) error {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	return a.s.q.RecordSignIn(ctx, sqlcgen.RecordSignInParams{Login: login, GithubUserID: id})
}

func (a *AuthStore) DeleteUser(ctx context.Context, id int64) error {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	n, err := a.s.q.DeleteUser(ctx, id)
	if err != nil {
		return err
	}
	if n == 0 {
		return auth.ErrNotFound
	}
	return nil
}

func (a *AuthStore) InsertSession(ctx context.Context, s auth.Session) error {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	return a.s.q.InsertSession(ctx, sqlcgen.InsertSessionParams{IDHash: s.IDHash, GithubUserID: s.UserID, CsrfHash: s.CSRFHash, Source: s.Source})
}

func (a *AuthStore) GetSession(ctx context.Context, idHash string) (auth.Session, auth.User, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	r, err := a.s.q.GetActiveSession(ctx, idHash)
	if err != nil {
		return auth.Session{}, auth.User{}, nf(err)
	}
	return auth.Session{IDHash: r.IDHash, CSRFHash: r.CsrfHash, UserID: r.GithubUserID},
		auth.User{GitHubID: r.GithubUserID, Login: r.Login, Role: auth.Role(r.Role)}, nil
}

func (a *AuthStore) TouchSession(ctx context.Context, idHash string, _ time.Time) error {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	return a.s.q.TouchSession(ctx, idHash)
}

func (a *AuthStore) EndSession(ctx context.Context, idHash string) error {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	return a.s.q.EndSession(ctx, idHash)
}

func (a *AuthStore) EndUserSessions(ctx context.Context, id int64) error {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	return a.s.q.EndUserSessions(ctx, id)
}

func (a *AuthStore) InsertCLIToken(ctx context.Context, t auth.CLIToken) error {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	return a.s.q.InsertCliToken(ctx, sqlcgen.InsertCliTokenParams{ID: t.ID, Hash: t.Hash, GithubUserID: t.UserID, Name: t.Name, ExpiresAt: fmtTime(t.ExpiresAt)})
}

func (a *AuthStore) GetCLIToken(ctx context.Context, hash string, _ time.Time) (auth.CLIToken, auth.User, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	r, err := a.s.q.GetActiveCliToken(ctx, hash)
	if err != nil {
		return auth.CLIToken{}, auth.User{}, nf(err)
	}
	return auth.CLIToken{ID: r.ID, Hash: hash, UserID: r.GithubUserID, UserLogin: r.Login, ExpiresAt: ParseTime(r.ExpiresAt)},
		auth.User{GitHubID: r.GithubUserID, Login: r.Login, Role: auth.Role(r.Role)}, nil
}

func (a *AuthStore) TouchCLIToken(ctx context.Context, id string, at time.Time) error {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	_, err := a.s.db.ExecContext(ctx, `UPDATE cli_tokens SET last_used_at = ? WHERE id = ?`, fmtTime(at), id)
	return err
}

func toCLIToken(r sqlcgen.ListCliTokensRow) auth.CLIToken {
	return auth.CLIToken{ID: r.ID, Name: r.Name, UserID: r.GithubUserID, UserLogin: r.Login, CreatedAt: ParseTime(r.CreatedAt),
		ExpiresAt: ParseTime(r.ExpiresAt), LastUsedAt: nullTime(r.LastUsedAt), RevokedAt: nullTime(r.RevokedAt)}
}

func (a *AuthStore) ListCLITokens(ctx context.Context, userID int64) ([]auth.CLIToken, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	var arg interface{}
	if userID != 0 {
		arg = userID
	}
	rows, err := a.s.q.ListCliTokens(ctx, arg)
	if err != nil {
		return nil, err
	}
	out := make([]auth.CLIToken, 0, len(rows))
	for _, r := range rows {
		out = append(out, toCLIToken(r))
	}
	return out, nil
}

func (a *AuthStore) GetCLITokenByID(ctx context.Context, id string) (auth.CLIToken, error) {
	rows, err := a.ListCLITokens(ctx, 0)
	if err != nil {
		return auth.CLIToken{}, err
	}
	for _, t := range rows {
		if t.ID == id {
			return t, nil
		}
	}
	return auth.CLIToken{}, auth.ErrNotFound
}

func (a *AuthStore) RevokeCLIToken(ctx context.Context, id string, _ time.Time) error {
	if _, err := a.GetCLITokenByID(ctx, id); err != nil {
		return err
	}
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	_, err := a.s.q.RevokeCliToken(ctx, id)
	return err
}

func (a *AuthStore) RevokeUserCLITokens(ctx context.Context, userID int64, _ time.Time) error {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	return a.s.q.RevokeUserCliTokens(ctx, userID)
}

func (a *AuthStore) InsertLinkCode(ctx context.Context, c auth.LinkCode) error {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	return a.s.q.InsertLinkCode(ctx, sqlcgen.InsertLinkCodeParams{CodeHash: c.Hash, GithubUserID: c.UserID,
		Purpose: c.Purpose, TargetPath: c.TargetPath, ExpiresAt: fmtTime(c.ExpiresAt)})
}

// UseLinkCode marks a live code used in the same statement that finds it, so
// two requests with one code cannot both succeed (tray R6.1). Expiry is
// judged by the database clock.
func (a *AuthStore) UseLinkCode(ctx context.Context, hash string, _ time.Time) (auth.LinkCode, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	r, err := a.s.q.UseLinkCode(ctx, hash)
	if err != nil {
		return auth.LinkCode{}, nf(err)
	}
	return auth.LinkCode{Hash: hash, UserID: r.GithubUserID, Purpose: r.Purpose, TargetPath: r.TargetPath}, nil
}

func (a *AuthStore) GetTrayBinding(ctx context.Context, machineID, osUserID string) (auth.TrayBinding, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	b, err := a.s.q.GetTrayBinding(ctx, sqlcgen.GetTrayBindingParams{MachineID: machineID, OsUserID: osUserID})
	if err != nil {
		return auth.TrayBinding{}, nf(err)
	}
	return auth.TrayBinding{MachineID: b.MachineID, OSUserID: b.OsUserID, UserID: b.GithubUserID}, nil
}

func (a *AuthStore) UpsertTrayBinding(ctx context.Context, b auth.TrayBinding) error {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	return a.s.q.UpsertTrayBinding(ctx, sqlcgen.UpsertTrayBindingParams{MachineID: b.MachineID, OsUserID: b.OSUserID, GithubUserID: b.UserID})
}

func (a *AuthStore) GetTheme(ctx context.Context, userID int64) (string, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	p, err := a.s.q.GetPreferences(ctx, userID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return p.Theme, err
}

func (a *AuthStore) SetTheme(ctx context.Context, userID int64, theme string) error {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	return a.s.q.SetPreferences(ctx, sqlcgen.SetPreferencesParams{GithubUserID: userID, Theme: theme})
}

func (a *AuthStore) Audit(ctx context.Context, e auth.AuditEvent) error {
	detail, _ := json.Marshal(map[string]string{"detail": e.Detail})
	if e.Result == "failed" {
		e.Result = "refused" // the audit log's word (observability R5.24)
	}
	_, err := a.s.InsertAudit(ctx, AuditEntry{Actor: e.Actor, ActorID: e.ActorID, Source: e.Source, Action: e.Action,
		TargetType: e.TargetType, TargetID: e.Subject, Detail: string(detail), Result: e.Result})
	return err
}
