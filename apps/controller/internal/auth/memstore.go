package auth

import (
	"context"
	"sort"
	"sync"
	"time"
)

// MemStore is an in-memory Store for tests and for other packages' tests. It
// follows the rules the SQL schema enforces: one owner, unique user ids,
// single-use codes.
type MemStore struct {
	mu       sync.Mutex
	app      *AppRecord
	setup    map[string]setupRow
	users    map[int64]User
	sessions map[string]sessionRow
	tokens   map[string]CLIToken
	links    map[string]linkRow
	bindings map[[2]string]TrayBinding
	audit    []AuditEvent
	themes   map[int64]string
	// FailAudit makes Audit fail, to test that callers do not lose the error.
	FailAudit bool
}

type setupRow struct {
	expires time.Time
	used    bool
}
type sessionRow struct {
	s     Session
	ended bool
	seen  time.Time
}
type linkRow struct {
	c    LinkCode
	used bool
}

// NewMemStore returns an empty store.
func NewMemStore() *MemStore {
	return &MemStore{setup: map[string]setupRow{}, users: map[int64]User{}, sessions: map[string]sessionRow{},
		tokens: map[string]CLIToken{}, links: map[string]linkRow{}, bindings: map[[2]string]TrayBinding{}, themes: map[int64]string{}}
}

var _ Store = (*MemStore)(nil)

// AuditEvents returns the audit rows so far.
func (m *MemStore) AuditEvents() []AuditEvent {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]AuditEvent(nil), m.audit...)
}

// Dump returns every stored string value, to test that no secret is kept.
func (m *MemStore) Dump() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []string
	for h := range m.setup {
		out = append(out, h)
	}
	for h, s := range m.sessions {
		out = append(out, h, s.s.CSRFHash)
	}
	for _, t := range m.tokens {
		out = append(out, t.ID, t.Hash, t.Name)
	}
	for h := range m.links {
		out = append(out, h)
	}
	for _, e := range m.audit {
		out = append(out, e.Actor, e.ActorID, e.Source, e.Action, e.TargetType, e.Subject, e.Detail, e.Result)
	}
	for _, u := range m.users {
		out = append(out, u.Login, u.AddedBy)
	}
	return out
}

func (m *MemStore) GetApp(context.Context) (AppRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.app == nil {
		return AppRecord{}, ErrNotFound
	}
	return *m.app, nil
}

func (m *MemStore) SaveApp(_ context.Context, a AppRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.app = &a
	return nil
}

func (m *MemStore) InsertSetupCode(_ context.Context, hash string, expires time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.setup[hash] = setupRow{expires: expires}
	return nil
}

func (m *MemStore) UseSetupCode(_ context.Context, hash string, now time.Time) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.setup[hash]
	if !ok || r.used || !now.Before(r.expires) {
		return false, nil
	}
	r.used = true
	m.setup[hash] = r
	return true, nil
}

func (m *MemStore) CountUsers(context.Context) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.users), nil
}

func (m *MemStore) GetUser(_ context.Context, id int64) (User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[id]
	if !ok {
		return User{}, ErrNotFound
	}
	return u, nil
}

func (m *MemStore) GetOwner(context.Context) (User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, u := range m.users {
		if u.Role == RoleOwner {
			return u, nil
		}
	}
	return User{}, ErrNotFound
}

func (m *MemStore) ListUsers(context.Context) ([]User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]User, 0, len(m.users))
	for _, u := range m.users {
		out = append(out, u)
	}
	sort.Slice(out, func(i, j int) bool {
		if (out[i].Role == RoleOwner) != (out[j].Role == RoleOwner) {
			return out[i].Role == RoleOwner
		}
		return out[i].Login < out[j].Login
	})
	return out, nil
}

func (m *MemStore) InsertUser(_ context.Context, u User) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.users[u.GitHubID]; ok {
		return ErrConflict
	}
	if u.Role == RoleOwner {
		for _, x := range m.users {
			if x.Role == RoleOwner {
				return ErrConflict
			}
		}
	}
	m.users[u.GitHubID] = u
	return nil
}

func (m *MemStore) SetRole(_ context.Context, id int64, r Role) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[id]
	if !ok {
		return ErrNotFound
	}
	if r == RoleOwner {
		return ErrConflict // ownership moves only through TransferOwner
	}
	u.Role = r
	m.users[id] = u
	return nil
}

func (m *MemStore) TransferOwner(_ context.Context, from, to int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	f, ok1 := m.users[from]
	t, ok2 := m.users[to]
	if !ok1 || !ok2 || f.Role != RoleOwner {
		return ErrNotFound
	}
	f.Role, t.Role = RoleAdmin, RoleOwner
	m.users[from], m.users[to] = f, t
	return nil
}

func (m *MemStore) RecordSignIn(_ context.Context, id int64, login string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[id]
	if !ok {
		return ErrNotFound
	}
	u.Login, u.LastSignIn = login, at
	m.users[id] = u
	return nil
}

func (m *MemStore) DeleteUser(_ context.Context, id int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[id]
	if !ok || u.Role == RoleOwner {
		return ErrNotFound
	}
	delete(m.users, id)
	for h, s := range m.sessions {
		if s.s.UserID == id {
			delete(m.sessions, h)
		}
	}
	for k, t := range m.tokens {
		if t.UserID == id {
			delete(m.tokens, k)
		}
	}
	return nil
}

func (m *MemStore) InsertSession(_ context.Context, s Session) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[s.IDHash] = sessionRow{s: s}
	return nil
}

func (m *MemStore) GetSession(_ context.Context, h string) (Session, User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.sessions[h]
	if !ok || r.ended {
		return Session{}, User{}, ErrNotFound
	}
	u, ok := m.users[r.s.UserID]
	if !ok {
		return Session{}, User{}, ErrNotFound
	}
	return r.s, u, nil
}

func (m *MemStore) TouchSession(_ context.Context, h string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r, ok := m.sessions[h]; ok {
		r.seen = at
		m.sessions[h] = r
	}
	return nil
}

func (m *MemStore) EndSession(_ context.Context, h string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r, ok := m.sessions[h]; ok {
		r.ended = true
		m.sessions[h] = r
	}
	return nil
}

func (m *MemStore) EndUserSessions(_ context.Context, id int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for h, r := range m.sessions {
		if r.s.UserID == id {
			r.ended = true
			m.sessions[h] = r
		}
	}
	return nil
}

func (m *MemStore) InsertCLIToken(_ context.Context, t CLIToken) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tokens[t.ID] = t
	return nil
}

func (m *MemStore) GetCLIToken(_ context.Context, hash string, now time.Time) (CLIToken, User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, t := range m.tokens {
		if t.Hash != hash {
			continue
		}
		u, ok := m.users[t.UserID]
		if !ok || !t.RevokedAt.IsZero() || !now.Before(t.ExpiresAt) {
			return CLIToken{}, User{}, ErrNotFound
		}
		return t, u, nil
	}
	return CLIToken{}, User{}, ErrNotFound
}

func (m *MemStore) TouchCLIToken(_ context.Context, id string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if t, ok := m.tokens[id]; ok {
		t.LastUsedAt = at
		m.tokens[id] = t
	}
	return nil
}

func (m *MemStore) ListCLITokens(_ context.Context, userID int64) ([]CLIToken, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []CLIToken
	for _, t := range m.tokens {
		if userID == 0 || t.UserID == userID {
			if u, ok := m.users[t.UserID]; ok {
				t.UserLogin = u.Login
			}
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func (m *MemStore) GetCLITokenByID(_ context.Context, id string) (CLIToken, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tokens[id]
	if !ok {
		return CLIToken{}, ErrNotFound
	}
	return t, nil
}

func (m *MemStore) RevokeCLIToken(_ context.Context, id string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tokens[id]
	if !ok {
		return ErrNotFound
	}
	if t.RevokedAt.IsZero() {
		t.RevokedAt = at
		m.tokens[id] = t
	}
	return nil
}

func (m *MemStore) RevokeUserCLITokens(_ context.Context, userID int64, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, t := range m.tokens {
		if t.UserID == userID && t.RevokedAt.IsZero() {
			t.RevokedAt = at
			m.tokens[id] = t
		}
	}
	return nil
}

func (m *MemStore) InsertLinkCode(_ context.Context, c LinkCode) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.links[c.Hash] = linkRow{c: c}
	return nil
}

func (m *MemStore) UseLinkCode(_ context.Context, hash string, now time.Time) (LinkCode, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.links[hash]
	if !ok || r.used || !now.Before(r.c.ExpiresAt) {
		return LinkCode{}, ErrNotFound
	}
	r.used = true
	m.links[hash] = r
	return r.c, nil
}

func (m *MemStore) GetTrayBinding(_ context.Context, machineID, osUser string) (TrayBinding, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.bindings[[2]string{machineID, osUser}]
	if !ok {
		return TrayBinding{}, ErrNotFound
	}
	return b, nil
}

func (m *MemStore) UpsertTrayBinding(_ context.Context, b TrayBinding) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.bindings[[2]string{b.MachineID, b.OSUserID}] = b
	return nil
}

func (m *MemStore) Audit(_ context.Context, e AuditEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.FailAudit {
		return context.DeadlineExceeded
	}
	m.audit = append(m.audit, e)
	return nil
}

func (m *MemStore) GetTheme(_ context.Context, id int64) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.themes[id], nil
}

func (m *MemStore) SetTheme(_ context.Context, id int64, theme string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.themes[id] = theme
	return nil
}
