// Package auth is sign-in, users, sessions, CLI tokens and the first-run
// setup of the controller (github-integration R5.1 to R5.16; tray R5.7).
//
// It reaches data only through the Store interface declared here (the API
// layer may not import the database package), and GitHub only through the
// GitHub interface. Secrets go to the OS secret store (R6.1); the database
// keeps hashes of codes, session ids and tokens, never the values.
package auth

import (
	"errors"
	"fmt"
	"time"
)

// Role is a product role (R5.12).
type Role string

// The three roles. A viewer reads; an admin also changes pool, policy,
// machines and runners; the owner also manages users.
const (
	RoleViewer Role = "viewer"
	RoleAdmin  Role = "admin"
	RoleOwner  Role = "owner"
)

// Valid reports whether r is one of the three roles.
func (r Role) Valid() bool { return r == RoleViewer || r == RoleAdmin || r == RoleOwner }

func (r Role) rank() int {
	switch r {
	case RoleOwner:
		return 3
	case RoleAdmin:
		return 2
	case RoleViewer:
		return 1
	}
	return 0
}

// AtLeast reports whether r may do what min may do.
func (r Role) AtLeast(min Role) bool { return r.rank() >= min.rank() && r.rank() > 0 }

// User is a person allowed to sign in (R5.9, R6.10).
type User struct {
	GitHubID   int64
	Login      string
	Role       Role
	AddedBy    string
	AddedAt    time.Time
	LastSignIn time.Time
}

// Principal is who a request acts as.
type Principal struct {
	UserID int64
	Login  string
	Role   Role
	// Source is "web", "tray" or "cli".
	Source string
	// SessionHash identifies the web session; empty for a CLI token.
	SessionHash string
	// TokenID identifies the CLI token; empty for a web session.
	TokenID string
	CSRF    string
}

// Errors the API turns into answers.
var (
	ErrUnauthenticated = errors.New("not signed in")
	ErrForbidden       = errors.New("role does not allow this")
	ErrBadState        = errors.New("sign-in state is missing, used or expired")
	ErrBadCode         = errors.New("code is wrong, used or expired")
	ErrConflict        = errors.New("already exists")
	ErrNotFound        = errors.New("not found")
	ErrNeedsConfirm    = errors.New("needs confirmation")
	ErrLoopbackOnly    = errors.New("this is allowed from this computer only")
	ErrInsecure        = errors.New("plain http is allowed on a loopback address only")
	ErrSetupDone       = errors.New("setup is already done")
	ErrNoApp           = errors.New("the GitHub App is not set up")
)

// NotAllowedError is returned at sign-in for a GitHub user who is not on the
// list; the page names their login and says to ask the owner (R5.11).
type NotAllowedError struct{ Login string }

func (e *NotAllowedError) Error() string {
	return fmt.Sprintf("github user %q is not allowed; ask the owner to add them", e.Login)
}

// AppRecord is the public facts about the App (no secrets, R6.1).
type AppRecord struct {
	ID            int64
	Slug          string
	ClientID      string
	OwnerLogin    string
	OwnerType     string
	HTMLURL       string
	WebhookActive bool
	Permissions   map[string]string
}

// Session is a stored web session; only hashes are kept (R5.13).
type Session struct {
	IDHash   string
	CSRFHash string
	UserID   int64
	Source   string
}

// CLIToken is a stored CLI token (R5.15).
type CLIToken struct {
	ID         string
	Hash       string
	UserID     int64
	UserLogin  string
	Name       string
	CreatedAt  time.Time
	ExpiresAt  time.Time
	LastUsedAt time.Time
	RevokedAt  time.Time
}

// LinkCode is a one-time tray link (tray R6.1).
type LinkCode struct {
	Hash       string
	UserID     int64
	Purpose    string // sign_in or bind_tray
	TargetPath string
	ExpiresAt  time.Time
}

// TrayBinding is the GitHub user bound to an OS user of a machine.
type TrayBinding struct {
	MachineID string
	OSUserID  string
	UserID    int64
}

// AuditEvent is one audit row. It never holds a token (R5.16).
type AuditEvent struct {
	Actor      string
	ActorID    string
	Source     string
	Action     string
	TargetType string
	Subject    string
	Detail     string
	Result     string // done or failed
}

// InvalidError is a request the person can fix; its message is safe to show.
type InvalidError struct{ Msg string }

func (e *InvalidError) Error() string { return e.Msg }

// Invalid makes an InvalidError.
func Invalid(format string, args ...any) error {
	return &InvalidError{Msg: fmt.Sprintf(format, args...)}
}
