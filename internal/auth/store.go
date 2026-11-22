package auth

import (
	"context"
	"time"
)

// Store is the data this package needs. The SQLite adapter lives with the
// database package; MemStore is the in-memory implementation for tests.
// Implementations return ErrNotFound for a missing row and ErrConflict when a
// unique rule is hit (a second owner, a repeated user).
type Store interface {
	GetApp(ctx context.Context) (AppRecord, error)
	SaveApp(ctx context.Context, a AppRecord) error

	InsertSetupCode(ctx context.Context, hash string, expires time.Time) error
	// UseSetupCode marks a live code used; false when it is wrong, used or old.
	UseSetupCode(ctx context.Context, hash string, now time.Time) (bool, error)

	CountUsers(ctx context.Context) (int, error)
	GetUser(ctx context.Context, id int64) (User, error)
	GetOwner(ctx context.Context) (User, error)
	ListUsers(ctx context.Context) ([]User, error)
	// InsertUser fails with ErrConflict for a repeated id or a second owner.
	InsertUser(ctx context.Context, u User) error
	SetRole(ctx context.Context, id int64, r Role) error
	// TransferOwner makes `to` the owner and `from` an admin in one step.
	TransferOwner(ctx context.Context, from, to int64) error
	RecordSignIn(ctx context.Context, id int64, login string, at time.Time) error
	// DeleteUser removes a non-owner user.
	DeleteUser(ctx context.Context, id int64) error

	InsertSession(ctx context.Context, s Session) error
	// GetSession returns the session and its user, if the session has not ended.
	GetSession(ctx context.Context, idHash string) (Session, User, error)
	TouchSession(ctx context.Context, idHash string, at time.Time) error
	EndSession(ctx context.Context, idHash string) error
	EndUserSessions(ctx context.Context, userID int64) error

	InsertCLIToken(ctx context.Context, t CLIToken) error
	// GetCLIToken returns an unrevoked, unexpired token and its user.
	GetCLIToken(ctx context.Context, hash string, now time.Time) (CLIToken, User, error)
	TouchCLIToken(ctx context.Context, id string, at time.Time) error
	ListCLITokens(ctx context.Context, userID int64) ([]CLIToken, error) // userID 0 lists all
	GetCLITokenByID(ctx context.Context, id string) (CLIToken, error)
	RevokeCLIToken(ctx context.Context, id string, at time.Time) error
	RevokeUserCLITokens(ctx context.Context, userID int64, at time.Time) error

	InsertLinkCode(ctx context.Context, c LinkCode) error
	// UseLinkCode marks a live code used and returns it.
	UseLinkCode(ctx context.Context, hash string, now time.Time) (LinkCode, error)
	GetTrayBinding(ctx context.Context, machineID, osUserID string) (TrayBinding, error)
	UpsertTrayBinding(ctx context.Context, b TrayBinding) error

	GetTheme(ctx context.Context, userID int64) (string, error) // "" when unset
	SetTheme(ctx context.Context, userID int64, theme string) error

	// Audit appends an event (R5.16).
	Audit(ctx context.Context, e AuditEvent) error
}
