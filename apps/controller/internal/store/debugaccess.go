package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/store/sqlcgen"
)

// Debug access rows (migration 00049, docs/features/debug-access). Every write
// is a Tx method, so the change and its audit row commit together (R6.4).

// SettingDebugAccess is the key of the pool-wide debug settings (JSON): the kill
// switch and the transcript switch (R5.25, R5.26).
const SettingDebugAccess = "debug_access"

// DebugRepoSettings is one repository's debug setting. The zero value is off.
type DebugRepoSettings struct {
	RepositoryID int64
	Repository   string
	Enabled      bool
	Who          string // "admins" or "owner"
	LimitMin     int
	IdleMin      int
	HoldMin      int
	PersistentOK bool
	UpdatedAt    time.Time
	UpdatedBy    string
}

// DebugSession is one session row. EndedAt is zero while it is open.
type DebugSession struct {
	ID           string
	JobID        int64
	RepositoryID int64
	Repository   string
	MachineID    string
	RunnerName   string
	Backend      string
	Isolation    string
	OS           string
	UserLogin    string
	UserID       int64
	Reason       string
	Source       string
	SourceAddr   string
	OpenedAt     time.Time
	EndsAt       time.Time
	LastInputAt  time.Time
	LimitMin     int
	IdleMin      int
	Extended     bool
	Recorded     bool
	EndedAt      time.Time
	EndReason    string
	EndedBy      string
}

// DebugTranscript is the stored, redacted output of one session.
type DebugTranscript struct {
	SessionID string
	Text      string
	Bytes     int64
	Truncated bool
	ExpiresAt time.Time
	UpdatedAt time.Time
}

func parseOpt(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	return ParseTime(s)
}

func debugSessionFrom(r sqlcgen.DebugSession) DebugSession {
	return DebugSession{ID: r.ID, JobID: r.JobID, RepositoryID: r.RepositoryID, Repository: r.Repository, MachineID: r.MachineID,
		RunnerName: r.RunnerName, Backend: r.Backend, Isolation: r.Isolation, OS: r.Os, UserLogin: r.UserLogin, UserID: r.UserID,
		Reason: r.Reason, Source: r.Source, SourceAddr: r.SourceAddr, OpenedAt: ParseTime(r.OpenedAt), EndsAt: ParseTime(r.EndsAt),
		LastInputAt: ParseTime(r.LastInputAt), LimitMin: int(r.LimitMin), IdleMin: int(r.IdleMin), Extended: r.Extended == 1,
		Recorded: r.Recorded == 1, EndedAt: parseOpt(r.EndedAt), EndReason: r.EndReason, EndedBy: r.EndedBy}
}

func debugSessions(rows []sqlcgen.DebugSession) []DebugSession {
	out := make([]DebugSession, 0, len(rows))
	for _, r := range rows {
		out = append(out, debugSessionFrom(r))
	}
	return out
}

// DebugRepoSettings reads a repository's setting; ok is false when it has none
// (and so is off, R5.1).
func (s *Store) DebugRepoSettings(ctx context.Context, repoID int64) (DebugRepoSettings, bool, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	r, err := s.q.GetDebugRepoSettings(ctx, repoID)
	if errors.Is(err, sql.ErrNoRows) {
		return DebugRepoSettings{RepositoryID: repoID}, false, nil
	}
	if err != nil {
		return DebugRepoSettings{}, false, fmt.Errorf("get debug repo settings: %w", err)
	}
	return DebugRepoSettings{RepositoryID: r.RepositoryID, Repository: r.Repository, Enabled: r.Enabled == 1, Who: r.Who,
		LimitMin: int(r.LimitMin), IdleMin: int(r.IdleMin), HoldMin: int(r.HoldMin), PersistentOK: r.PersistentOk == 1,
		UpdatedAt: ParseTime(r.UpdatedAt), UpdatedBy: r.UpdatedBy}, true, nil
}

// DebugAccessJSON reads the pool-wide debug settings; ok is false when none were saved.
func (s *Store) DebugAccessJSON(ctx context.Context) (string, bool) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	return settingValue(ctx, s.db, SettingDebugAccess)
}

// DebugSession reads one session.
func (s *Store) DebugSession(ctx context.Context, id string) (DebugSession, bool, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	r, err := s.q.GetDebugSession(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return DebugSession{}, false, nil
	}
	if err != nil {
		return DebugSession{}, false, fmt.Errorf("get debug session: %w", err)
	}
	return debugSessionFrom(r), true, nil
}

// OpenDebugSessions lists the sessions that have not ended, oldest first.
func (s *Store) OpenDebugSessions(ctx context.Context) ([]DebugSession, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	rows, err := s.q.ListOpenDebugSessions(ctx)
	if err != nil {
		return nil, fmt.Errorf("list open debug sessions: %w", err)
	}
	return debugSessions(rows), nil
}

// DebugSessionsOfJob lists a job's sessions, newest first.
func (s *Store) DebugSessionsOfJob(ctx context.Context, jobID int64, limit int) ([]DebugSession, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	rows, err := s.q.ListDebugSessionsOfJob(ctx, sqlcgen.ListDebugSessionsOfJobParams{JobID: jobID, Limit: int64(limit)})
	if err != nil {
		return nil, fmt.Errorf("list debug sessions of job: %w", err)
	}
	return debugSessions(rows), nil
}

// DebugSessions lists the newest sessions of the pool.
func (s *Store) DebugSessions(ctx context.Context, limit int) ([]DebugSession, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	rows, err := s.q.ListDebugSessions(ctx, int64(limit))
	if err != nil {
		return nil, fmt.Errorf("list debug sessions: %w", err)
	}
	return debugSessions(rows), nil
}

// DebugTranscript reads a session's transcript; ok is false when there is none.
func (s *Store) DebugTranscript(ctx context.Context, sessionID string) (DebugTranscript, bool, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	r, err := s.q.GetDebugTranscript(ctx, sessionID)
	if errors.Is(err, sql.ErrNoRows) {
		return DebugTranscript{}, false, nil
	}
	if err != nil {
		return DebugTranscript{}, false, fmt.Errorf("get debug transcript: %w", err)
	}
	return DebugTranscript{SessionID: r.SessionID, Text: r.Text, Bytes: r.Bytes, Truncated: r.Truncated == 1,
		ExpiresAt: ParseTime(r.ExpiresAt), UpdatedAt: ParseTime(r.UpdatedAt)}, true, nil
}

// PutDebugRepoSettings writes a repository's setting.
func (t *Tx) PutDebugRepoSettings(r DebugRepoSettings) error {
	err := t.q.UpsertDebugRepoSettings(t.ctx, sqlcgen.UpsertDebugRepoSettingsParams{RepositoryID: r.RepositoryID, Repository: r.Repository,
		Enabled: b2i(r.Enabled), Who: r.Who, LimitMin: int64(r.LimitMin), IdleMin: int64(r.IdleMin), HoldMin: int64(r.HoldMin),
		PersistentOk: b2i(r.PersistentOK), UpdatedAt: FormatTime(r.UpdatedAt), UpdatedBy: r.UpdatedBy})
	if err != nil {
		return fmt.Errorf("put debug repo settings: %w", err)
	}
	return nil
}

// InsertDebugSession adds an open session.
func (t *Tx) InsertDebugSession(d DebugSession) error {
	err := t.q.InsertDebugSession(t.ctx, sqlcgen.InsertDebugSessionParams{ID: d.ID, JobID: d.JobID, RepositoryID: d.RepositoryID,
		Repository: d.Repository, MachineID: d.MachineID, RunnerName: d.RunnerName, Backend: d.Backend, Isolation: d.Isolation, Os: d.OS,
		UserLogin: d.UserLogin, UserID: d.UserID, Reason: d.Reason, Source: d.Source, SourceAddr: d.SourceAddr,
		OpenedAt: FormatTime(d.OpenedAt), EndsAt: FormatTime(d.EndsAt), LastInputAt: FormatTime(d.LastInputAt),
		LimitMin: int64(d.LimitMin), IdleMin: int64(d.IdleMin), Extended: b2i(d.Extended), Recorded: b2i(d.Recorded)})
	if err != nil {
		return fmt.Errorf("insert debug session: %w", err)
	}
	return nil
}

// ExtendDebugSession moves an open, not yet extended session's end; false when
// the session is ended or was extended already.
func (t *Tx) ExtendDebugSession(id string, endsAt time.Time) (bool, error) {
	n, err := t.q.ExtendDebugSession(t.ctx, sqlcgen.ExtendDebugSessionParams{EndsAt: FormatTime(endsAt), ID: id})
	if err != nil {
		return false, fmt.Errorf("extend debug session: %w", err)
	}
	return n == 1, nil
}

// BringDebugSessionEnd moves an open session's end earlier, never later.
func (t *Tx) BringDebugSessionEnd(id string, endsAt time.Time) (bool, error) {
	v := FormatTime(endsAt)
	n, err := t.q.SetDebugSessionEndsAt(t.ctx, sqlcgen.SetDebugSessionEndsAtParams{EndsAt: v, ID: id, EndsAt_2: v})
	if err != nil {
		return false, fmt.Errorf("bring debug session end: %w", err)
	}
	return n == 1, nil
}

// EndDebugSession closes an open session; false when it had ended already.
func (t *Tx) EndDebugSession(id string, at time.Time, reason, by string) (bool, error) {
	n, err := t.q.EndDebugSession(t.ctx, sqlcgen.EndDebugSessionParams{EndedAt: FormatTime(at), EndReason: reason, EndedBy: by, ID: id})
	if err != nil {
		return false, fmt.Errorf("end debug session: %w", err)
	}
	return n == 1, nil
}

// TouchDebugSession records a keystroke time for the idle limit (R5.18).
func (s *Store) TouchDebugSession(ctx context.Context, id string, at time.Time) (bool, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	n, err := s.q.TouchDebugSession(ctx, sqlcgen.TouchDebugSessionParams{LastInputAt: FormatTime(at), ID: id})
	if err != nil {
		return false, fmt.Errorf("touch debug session: %w", err)
	}
	return n == 1, nil
}

// AppendDebugTranscript adds redacted text to a session's transcript; the first
// call makes the row with its expiry.
func (s *Store) AppendDebugTranscript(ctx context.Context, d DebugTranscript) error {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	err := s.q.AppendDebugTranscript(ctx, sqlcgen.AppendDebugTranscriptParams{SessionID: d.SessionID, Text: d.Text, Bytes: d.Bytes,
		Truncated: b2i(d.Truncated), ExpiresAt: FormatTime(d.ExpiresAt), UpdatedAt: FormatTime(d.UpdatedAt)})
	if err != nil {
		return fmt.Errorf("append debug transcript: %w", err)
	}
	return nil
}

// PruneDebugTranscripts drops transcripts past their time, then the oldest
// until the pool total is at most maxBytes (R6.6). The session and audit rows
// stay. It returns how many transcripts went.
func (s *Store) PruneDebugTranscripts(ctx context.Context, now time.Time, maxBytes int64) (int, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	n, err := s.q.DeleteExpiredDebugTranscripts(ctx, FormatTime(now))
	if err != nil {
		return 0, fmt.Errorf("prune debug transcripts: %w", err)
	}
	gone := int(n)
	for {
		total, err := s.q.SumDebugTranscriptBytes(ctx)
		if err != nil {
			return gone, fmt.Errorf("sum debug transcripts: %w", err)
		}
		if total <= maxBytes {
			return gone, nil
		}
		old, err := s.q.OldestDebugTranscript(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			return gone, nil
		}
		if err != nil {
			return gone, fmt.Errorf("oldest debug transcript: %w", err)
		}
		if err := s.q.DeleteDebugTranscript(ctx, old.SessionID); err != nil {
			return gone, fmt.Errorf("delete debug transcript: %w", err)
		}
		gone++
	}
}
