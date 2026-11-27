package debugaccess

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/audit"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/auth"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/store"
)

// Audit actions (R5.24, R5.26).
const (
	ActionOpened   = "debug.session.opened"
	ActionRefused  = "debug.session.refused"
	ActionExtended = "debug.session.extended"
	ActionNotice   = "debug.session.notice"
	ActionEnded    = "debug.session.ended"
	ActionRepoSet  = "debug.repo.set"
	ActionPoolSet  = "debug.pool.set"
)

// Session and Transcript are the stored rows, named here so the API can use
// them without importing the store (STACKSPEC 3.2).
type (
	Session    = store.DebugSession
	Transcript = store.DebugTranscript
)

// Jobs reads the facts of a job for the policy.
type Jobs interface {
	// DebugJob returns one job; ok is false when it is not known.
	DebugJob(ctx context.Context, jobID int64) (Job, bool, error)
	// RunningBeside lists the other running jobs on the same machine and runner.
	RunningBeside(ctx context.Context, j Job) ([]int64, error)
}

// Repos reads the repository of a job as the installation knows it.
type Repos interface {
	DebugRepo(ctx context.Context, repoID int64) (Repo, error)
}

// Enterer is the machine side (R5.32): whether the agent can enter the job's
// environment safely. It answers a *Refusal with the backend's reason.
type Enterer interface {
	CanEnter(ctx context.Context, j Job) error
}

// Service is the debug access area.
type Service struct {
	DB    *store.Store
	Jobs  Jobs
	Repos Repos
	// Enter is nil until an agent can enter a job's environment; then every
	// open is refused with TextCannotEnter.
	Enter Enterer
	// Redact is the process redactor (redact.Redactor.Redact).
	Redact func(string) string
	Now    func() time.Time
	// NewID makes a session id; nil uses 16 random bytes.
	NewID func() string

	// openMu makes the cap checks and the insert of Open one step, so two
	// requests at once cannot both pass the caps (R5.4).
	openMu sync.Mutex
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *Service) newID() (string, error) {
	if s.NewID != nil {
		return s.NewID(), nil
	}
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "dbg_" + hex.EncodeToString(b[:]), nil
}

func actorOf(p auth.Principal, source string) audit.Actor {
	if source == "" {
		source = p.Source
	}
	id := ""
	if p.UserID != 0 {
		id = fmt.Sprint(p.UserID)
	}
	return audit.Actor{Login: p.Login, ID: id, Source: source}
}

func target(id string) audit.Target { return audit.Target{Type: "debug_session", ID: id} }

// ---------------------------------------------------------------- settings

// Pool reads the pool-wide setting.
func (s *Service) Pool(ctx context.Context) PoolSettings {
	v, ok := s.DB.DebugAccessJSON(ctx)
	if !ok {
		return DefaultPoolSettings()
	}
	p := DefaultPoolSettings()
	if err := json.Unmarshal([]byte(v), &p); err != nil {
		// A setting that cannot be read is the safe one: off.
		return PoolSettings{Enabled: false, Record: true}
	}
	return p
}

// OpenCount is how many sessions are open now.
func (s *Service) OpenCount(ctx context.Context) (int, error) {
	open, err := s.DB.OpenDebugSessions(ctx)
	return len(open), err
}

// SetPool saves the pool-wide setting (owner only). Turning sessions off ends
// every open session at once (the kill switch, R5.26); that needs confirm when
// a session is open. It returns how many sessions it ended.
func (s *Service) SetPool(ctx context.Context, p auth.Principal, next PoolSettings, confirm bool) (int, error) {
	if !p.Role.AtLeast(auth.RoleOwner) {
		return 0, &Refusal{Code: CodeOwnerOnly, Text: "Only the owner can change this."}
	}
	now := s.now()
	next.UpdatedAt, next.UpdatedBy = now, p.Login
	open, err := s.DB.OpenDebugSessions(ctx)
	if err != nil {
		return 0, err
	}
	if !next.Enabled && len(open) > 0 && !confirm {
		return 0, &Refusal{Code: CodeNeedsConfirm, Text: fmt.Sprintf("%d debug sessions are open. They end at once.", len(open))}
	}
	body, err := json.Marshal(next)
	if err != nil {
		return 0, err
	}
	actor := actorOf(p, "")
	ended := 0
	err = s.DB.WithTx(ctx, func(tx *store.Tx) error {
		if err := tx.SetSetting(store.SettingDebugAccess, string(body), p.Login); err != nil {
			return err
		}
		if err := audit.RecordTx(tx, actor, ActionPoolSet, audit.Target{Type: "setting", ID: store.SettingDebugAccess},
			map[string]any{"enabled": next.Enabled, "record": next.Record, "open_sessions": len(open)}); err != nil {
			return err
		}
		if next.Enabled {
			return nil
		}
		for _, o := range open {
			done, err := s.endTx(tx, actor, o, now, EndKillSwitch)
			if err != nil {
				return err
			}
			if done {
				ended++
			}
		}
		return nil
	})
	return ended, err
}

// Repo reads a repository's setting.
func (s *Service) Repo(ctx context.Context, repoID int64) (RepoSettings, error) {
	r, ok, err := s.DB.DebugRepoSettings(ctx, repoID)
	if err != nil {
		return RepoSettings{}, err
	}
	return repoFromStore(r, ok), nil
}

// SetRepo saves a repository's setting (admins and owner, R5.1, R5.2). A
// public repository cannot be switched on. Only the owner sets or lifts
// Owner only, so an admin can never widen what the owner narrowed.
func (s *Service) SetRepo(ctx context.Context, p auth.Principal, repoID int64, next RepoSettings) (RepoSettings, error) {
	if !p.Role.AtLeast(auth.RoleAdmin) {
		return RepoSettings{}, &Refusal{Code: CodeViewer, Text: "Only admins can change debug access."}
	}
	if err := next.Validate(); err != nil {
		return RepoSettings{}, err
	}
	info, err := s.Repos.DebugRepo(ctx, repoID)
	if err != nil {
		return RepoSettings{}, err
	}
	if !info.Known {
		return RepoSettings{}, &Refusal{Code: CodeRepoUnknown, Text: TextRepoUnknown}
	}
	repoName := info.FullName
	if next.Enabled && info.Visibility != "private" && info.Visibility != "internal" {
		return RepoSettings{}, &Refusal{Code: CodePublic, Text: TextPublic}
	}
	cur, err := s.Repo(ctx, repoID)
	if err != nil {
		return RepoSettings{}, err
	}
	if (cur.Who == WhoOwner || next.Who == WhoOwner) && cur.Who != next.Who && !p.Role.AtLeast(auth.RoleOwner) {
		return RepoSettings{}, &Refusal{Code: CodeOwnerOnly, Text: "Only the owner can set or lift Owner only."}
	}
	now := s.now()
	next.UpdatedAt, next.UpdatedBy = now, p.Login
	err = s.DB.WithTx(ctx, func(tx *store.Tx) error {
		if err := tx.PutDebugRepoSettings(store.DebugRepoSettings{RepositoryID: repoID, Repository: repoName, Enabled: next.Enabled,
			Who: next.Who, LimitMin: next.LimitMin, IdleMin: next.IdleMin, HoldMin: next.HoldMin, PersistentOK: next.PersistentOK,
			UpdatedAt: now, UpdatedBy: p.Login}); err != nil {
			return err
		}
		return audit.RecordTx(tx, actorOf(p, ""), ActionRepoSet, audit.Target{Type: "repository", ID: fmt.Sprint(repoID)},
			map[string]any{"repository": repoName, "enabled": next.Enabled, "who": next.Who, "limit_min": next.LimitMin,
				"idle_min": next.IdleMin, "hold_min": next.HoldMin, "persistent_ok": next.PersistentOK,
				"summary": fmt.Sprintf("debug access %s for %s", onOff(next.Enabled), repoName)})
	})
	if err != nil {
		return RepoSettings{}, err
	}
	return next, nil
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

// RepoInfo reads the repository as the installation knows it.
func (s *Service) RepoInfo(ctx context.Context, repoID int64) (Repo, error) {
	if s.Repos == nil {
		return Repo{}, errors.New("debug access is not wired")
	}
	return s.Repos.DebugRepo(ctx, repoID)
}

// ---------------------------------------------------------------- the job's card

// JobView is what the Debug card of a job shows.
type JobView struct {
	Job      Job
	Repo     RepoSettings
	Refusal  *Refusal
	Warnings []string
	Open     *store.DebugSession
	Sessions []store.DebugSession
}

// ErrNoJob is returned for a job crsp does not know.
var ErrNoJob = &Refusal{Code: CodeNotFound, Text: "crsp does not know this job."}

func (s *Service) facts(ctx context.Context, p auth.Principal, jobID int64) (Facts, error) {
	if s.Jobs == nil || s.Repos == nil {
		return Facts{}, errors.New("debug access is not wired")
	}
	j, ok, err := s.Jobs.DebugJob(ctx, jobID)
	if err != nil {
		return Facts{}, err
	}
	if !ok {
		return Facts{}, ErrNoJob
	}
	f := Facts{Pool: s.Pool(ctx), Who: p, Job: j}
	if f.Repo, err = s.Repo(ctx, j.RepositoryID); err != nil {
		return Facts{}, err
	}
	if f.RepoInfo, err = s.Repos.DebugRepo(ctx, j.RepositoryID); err != nil {
		return Facts{}, err
	}
	if j.Isolation == "container" {
		if f.SharedWith, err = s.Jobs.RunningBeside(ctx, j); err != nil {
			return Facts{}, err
		}
	}
	if s.Enter == nil {
		f.Enter = &Refusal{Code: CodeCannotEnter, Text: TextCannotEnter}
	} else {
		f.Enter = s.Enter.CanEnter(ctx, j)
	}
	open, err := s.DB.OpenDebugSessions(ctx)
	if err != nil {
		return Facts{}, err
	}
	for _, o := range open {
		if o.UserLogin == p.Login {
			f.UserOpen++
		}
		if o.JobID == jobID {
			f.JobOpen = append(f.JobOpen, o)
		}
	}
	return f, nil
}

// JobCard returns the Debug card of a job for the user.
func (s *Service) JobCard(ctx context.Context, p auth.Principal, jobID int64) (JobView, error) {
	f, err := s.facts(ctx, p, jobID)
	if err != nil {
		return JobView{}, err
	}
	v := JobView{Job: f.Job, Repo: f.Repo, Warnings: Warnings(f.Job)}
	if len(f.JobOpen) > 0 {
		o := f.JobOpen[0]
		v.Open = &o
	}
	v.Refusal = Decide(f)
	if v.Sessions, err = s.DB.DebugSessionsOfJob(ctx, jobID, 50); err != nil {
		return JobView{}, err
	}
	return v, nil
}

// ---------------------------------------------------------------- open, extend, end

// OpenRequest is one request to open a session.
type OpenRequest struct {
	JobID  int64
	Reason string
	// Source is "web" or "cli"; Addr is the request's remote address.
	Source string
	Addr   string
}

// Open opens a session (R5.2 to R5.10, R5.24). A refusal writes a refused
// audit row and returns the *Refusal; an open writes the session row and its
// audit row in one transaction.
func (s *Service) Open(ctx context.Context, p auth.Principal, req OpenRequest) (store.DebugSession, error) {
	s.openMu.Lock()
	defer s.openMu.Unlock()
	actor := actorOf(p, req.Source)
	refuse := func(r *Refusal, j Job) (store.DebugSession, error) {
		if err := s.recordRefused(ctx, actor, req, j, r); err != nil {
			return store.DebugSession{}, err
		}
		return store.DebugSession{}, r
	}
	f, err := s.facts(ctx, p, req.JobID)
	var rf *Refusal
	if errors.As(err, &rf) {
		return refuse(rf, Job{ID: req.JobID})
	}
	if err != nil {
		return store.DebugSession{}, err
	}
	if r := Decide(f); r != nil {
		return refuse(r, f.Job)
	}
	reason, r := CheckReason(req.Reason)
	if r != nil {
		return refuse(r, f.Job)
	}
	id, err := s.newID()
	if err != nil {
		return store.DebugSession{}, err
	}
	now := s.now()
	limit := f.Repo.LimitMin
	if limit <= 0 || limit > MaxLimitMin {
		limit = DefaultLimitMin
	}
	idle := f.Repo.IdleMin
	if idle <= 0 || idle > MaxIdleMin {
		idle = DefaultIdleMin
	}
	j := f.Job
	d := store.DebugSession{ID: id, JobID: j.ID, RepositoryID: j.RepositoryID, Repository: j.Repository, MachineID: j.MachineID,
		RunnerName: j.RunnerName, Backend: j.Backend, Isolation: j.Isolation, OS: j.OS, UserLogin: p.Login, UserID: p.UserID,
		Reason: reason, Source: actor.Source, SourceAddr: req.Addr, OpenedAt: now, EndsAt: now.Add(time.Duration(limit) * time.Minute),
		LastInputAt: now, LimitMin: limit, IdleMin: idle, Recorded: f.Pool.Record}
	err = s.DB.WithTx(ctx, func(tx *store.Tx) error {
		if err := tx.InsertDebugSession(d); err != nil {
			return err
		}
		details := sessionDetails(d)
		details["source_addr"] = req.Addr
		if !d.Recorded {
			details["transcript"] = "no transcript"
		}
		details["summary"] = fmt.Sprintf("%s opened a debug session on job %d", p.Login, j.ID)
		return audit.RecordTx(tx, actor, ActionOpened, target(id), details)
	})
	if err != nil {
		return store.DebugSession{}, err
	}
	return d, nil
}

func sessionDetails(d store.DebugSession) map[string]any {
	return map[string]any{"job_id": d.JobID, "repository": d.Repository, "machine_id": d.MachineID, "runner": d.RunnerName,
		"backend": d.Backend, "isolation": d.Isolation, "os": d.OS, "reason": d.Reason, "source": d.Source,
		"ends_at": d.EndsAt.Format(time.RFC3339)}
}

func (s *Service) recordRefused(ctx context.Context, actor audit.Actor, req OpenRequest, j Job, r *Refusal) error {
	return audit.New(s.DB).RecordRefused(ctx, actor, ActionRefused, audit.Target{Type: "job", ID: fmt.Sprint(req.JobID)},
		map[string]any{"job_id": req.JobID, "repository": j.Repository, "why": r.Code, "summary": r.Text, "source_addr": req.Addr})
}

// Session reads one session.
func (s *Service) Session(ctx context.Context, id string) (store.DebugSession, error) {
	d, ok, err := s.DB.DebugSession(ctx, id)
	if err != nil {
		return store.DebugSession{}, err
	}
	if !ok {
		return store.DebugSession{}, &Refusal{Code: CodeNotFound, Text: "No such debug session."}
	}
	return d, nil
}

// Sessions lists the newest sessions of the pool.
func (s *Service) Sessions(ctx context.Context, limit int) ([]store.DebugSession, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	return s.DB.DebugSessions(ctx, limit)
}

// Extend gives a session its one extension (R5.19). Only the user who opened
// it extends it; it is an audit row.
func (s *Service) Extend(ctx context.Context, p auth.Principal, id string) (store.DebugSession, error) {
	if !p.Role.AtLeast(auth.RoleAdmin) {
		return store.DebugSession{}, &Refusal{Code: CodeViewer, Text: TextViewer}
	}
	d, err := s.Session(ctx, id)
	if err != nil {
		return store.DebugSession{}, err
	}
	if d.UserLogin != p.Login {
		return store.DebugSession{}, &Refusal{Code: CodeNotYours, Text: "Only the user who opened a session can extend it."}
	}
	now := s.now()
	repo, err := s.Repo(ctx, d.RepositoryID)
	if err != nil {
		return store.DebugSession{}, err
	}
	next, r := ExtendTo(d, now, repo.LimitMin)
	if r != nil {
		return store.DebugSession{}, r
	}
	err = s.DB.WithTx(ctx, func(tx *store.Tx) error {
		ok, err := tx.ExtendDebugSession(id, next)
		if err != nil {
			return err
		}
		if !ok {
			return &Refusal{Code: CodeExtended, Text: "A session can be extended once."}
		}
		return audit.RecordTx(tx, actorOf(p, ""), ActionExtended, target(id),
			map[string]any{"job_id": d.JobID, "repository": d.Repository, "ends_at": next.Format(time.RFC3339),
				"summary": fmt.Sprintf("%s extended debug session %s by 15 minutes", p.Login, id)})
	})
	if err != nil {
		return store.DebugSession{}, err
	}
	d.EndsAt, d.Extended = next, true
	return d, nil
}

// End ends a session: the user's own ("user") or, for an admin or the owner,
// anyone's ("admin") (R5.26).
func (s *Service) End(ctx context.Context, p auth.Principal, id string) (store.DebugSession, error) {
	if !p.Role.AtLeast(auth.RoleAdmin) {
		return store.DebugSession{}, &Refusal{Code: CodeViewer, Text: "Only admins can end a session."}
	}
	d, err := s.Session(ctx, id)
	if err != nil {
		return store.DebugSession{}, err
	}
	how := EndUser
	if d.UserLogin != p.Login {
		how = EndAdmin
	}
	return s.end(ctx, actorOf(p, ""), d, how)
}

func (s *Service) end(ctx context.Context, actor audit.Actor, d store.DebugSession, how string) (store.DebugSession, error) {
	now := s.now()
	var done bool
	err := s.DB.WithTx(ctx, func(tx *store.Tx) error {
		var err error
		done, err = s.endTx(tx, actor, d, now, how)
		return err
	})
	if err != nil {
		return store.DebugSession{}, err
	}
	if !done {
		return store.DebugSession{}, &Refusal{Code: CodeEnded, Text: "This session has ended."}
	}
	d.EndedAt, d.EndReason, d.EndedBy = now, how, actor.Login
	return d, nil
}

func (s *Service) endTx(tx *store.Tx, actor audit.Actor, d store.DebugSession, now time.Time, how string) (bool, error) {
	ok, err := tx.EndDebugSession(d.ID, now, how, actor.Login)
	if err != nil || !ok {
		return false, err
	}
	return true, audit.RecordTx(tx, actor, ActionEnded, target(d.ID), map[string]any{"job_id": d.JobID, "repository": d.Repository,
		"machine_id": d.MachineID, "how": how, "length_s": int(now.Sub(d.OpenedAt).Seconds()),
		"summary": fmt.Sprintf("debug session %s ended: %s", d.ID, how)})
}

// Touch records a keystroke for the idle limit (R5.18).
func (s *Service) Touch(ctx context.Context, id string) error {
	_, err := s.DB.TouchDebugSession(ctx, id, s.now())
	return err
}

// ---------------------------------------------------------------- the controller's own work

// Sweep ends every open session that is due (time limit, idle, job ended) and
// prunes the transcripts (30 days, 500 MB). The controller runs it every few
// seconds; the agent keeps its own limits too (R6.3, follow-up).
func (s *Service) Sweep(ctx context.Context) (int, error) {
	now := s.now()
	open, err := s.DB.OpenDebugSessions(ctx)
	if err != nil {
		return 0, err
	}
	ended := 0
	for _, o := range open {
		how, due := Due(o, now)
		if !due {
			// A job that stopped running gives its session the 60-second notice (R5.20).
			if s.Jobs != nil && o.EndsAt.After(now.Add(JobEndNotice)) {
				j, ok, err := s.Jobs.DebugJob(ctx, o.JobID)
				if err == nil && (!ok || !j.Running) {
					if err := s.JobEnded(ctx, o.JobID); err != nil {
						return ended, err
					}
				}
			}
			continue
		}
		if _, err := s.end(ctx, audit.System, o, how); err != nil {
			var r *Refusal
			if errors.As(err, &r) {
				continue
			}
			return ended, err
		}
		ended++
	}
	if _, err := s.DB.PruneDebugTranscripts(ctx, now, TranscriptMaxPool); err != nil {
		return ended, err
	}
	return ended, nil
}

// JobEnded gives the open sessions of a job that ended a 60-second notice
// (R5.20): their end moves to now plus 60 s, never later than it was.
func (s *Service) JobEnded(ctx context.Context, jobID int64) error {
	open, err := s.DB.OpenDebugSessions(ctx)
	if err != nil {
		return err
	}
	at := s.now().Add(JobEndNotice)
	for _, o := range open {
		if o.JobID != jobID {
			continue
		}
		err := s.DB.WithTx(ctx, func(tx *store.Tx) error {
			moved, err := tx.BringDebugSessionEnd(o.ID, at)
			if err != nil || !moved {
				return err
			}
			return audit.RecordTx(tx, audit.System, ActionNotice, target(o.ID), map[string]any{"job_id": jobID,
				"ends_at": at.Format(time.RFC3339), "summary": "the job ended; the session ends in 60 s"})
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// Reconcile runs at start (R6.4): every open session that the agent does not
// list is ended as link lost. live nil means no agent lists any session (no
// stream is built yet), so every open session is ended.
func (s *Service) Reconcile(ctx context.Context, live func(store.DebugSession) bool) (int, error) {
	open, err := s.DB.OpenDebugSessions(ctx)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, o := range open {
		if live != nil && live(o) {
			continue
		}
		if _, err := s.end(ctx, audit.System, o, EndLinkLost); err != nil {
			var r *Refusal
			if errors.As(err, &r) {
				continue
			}
			return n, err
		}
		n++
	}
	return n, nil
}

// Transcript reads a session's transcript (admins and owner, R5.25).
func (s *Service) Transcript(ctx context.Context, p auth.Principal, id string) (store.DebugTranscript, bool, error) {
	if !p.Role.AtLeast(auth.RoleAdmin) {
		return store.DebugTranscript{}, false, &Refusal{Code: CodeViewer, Text: "Only admins can read a transcript."}
	}
	if _, err := s.Session(ctx, id); err != nil {
		return store.DebugTranscript{}, false, err
	}
	return s.DB.DebugTranscript(ctx, id)
}

// NewRecorder returns the transcript recorder of a session, or nil when the
// session keeps no transcript.
func (s *Service) NewRecorder(d store.DebugSession) *Recorder {
	if !d.Recorded {
		return nil
	}
	return &Recorder{SessionID: d.ID, Store: s.DB, Redact: s.Redact, Now: s.now}
}
