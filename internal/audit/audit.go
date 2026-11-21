// Package audit is the one writer of the audit log (observability R5.24 to
// R5.27). Every area that changes state calls Record, or RecordTx inside its
// own transaction, so the row and the change commit together (R5.25).
//
// How other areas use it:
//
//	rec := audit.New(db)                       // db is *store.Store
//	err := rec.Record(ctx, actor, "runner.add",
//	    audit.Target{Type: "runner", ID: id},
//	    map[string]any{"summary": "added runner r1", "os": "linux"})
//
//	// In the same transaction as the change (preferred):
//	err := db.WithTx(ctx, func(tx *store.Tx) error {
//	    ... the change ...
//	    return audit.RecordTx(tx, actor, "runner.add", target, details)
//	})
//
// A failed audit write returns an error. The caller must then refuse the
// change: the audit log fails closed. A change that was refused (for example a
// forbidden action) is recorded with RecordRefused.
//
// The actor comes from the request: the auth layer calls WithActor, handlers
// call ActorFrom. Details are redacted: a key that looks like a secret is
// replaced by "[redacted]" and long strings are cut. Never put a secret value
// in details.
//
// The package has no update or delete path. Only Prune, the retention job's
// entry, removes rows, and it writes its own row with the count (R5.26).
package audit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/store"
)

// Sources of a change (R5.24).
const (
	SourceWeb    = "web"
	SourceCLI    = "cli"
	SourceTray   = "tray"
	SourceAPI    = "api"
	SourceSystem = "system"
	SourceAgent  = "agent"
)

// Results of a change (R5.24).
const (
	ResultDone    = "done"
	ResultRefused = "refused"
)

// Actor says who made a change.
type Actor struct {
	// Login is the GitHub login, "system", or the machine name for agent actions.
	Login string
	// ID is the GitHub user id, or empty.
	ID string
	// Source is one of the Source constants.
	Source string
}

// System is the actor of work the controller starts itself.
var System = Actor{Login: "system", Source: SourceSystem}

// Target names what a change touched.
type Target struct {
	Type string
	ID   string
}

// Recorder is what other areas depend on, so tests can swap it.
type Recorder interface {
	Record(ctx context.Context, actor Actor, action string, target Target, details map[string]any) error
	RecordRefused(ctx context.Context, actor Actor, action string, target Target, details map[string]any) error
}

type actorKey struct{}

// WithActor returns ctx carrying the actor of the request.
func WithActor(ctx context.Context, a Actor) context.Context {
	return context.WithValue(ctx, actorKey{}, a)
}

// ActorFrom returns the actor of ctx, or System when none was set.
func ActorFrom(ctx context.Context) Actor {
	if a, ok := ctx.Value(actorKey{}).(Actor); ok && a.Login != "" {
		return a
	}
	return System
}

// Log writes and reads the audit log.
type Log struct {
	db *store.Store
}

// New returns the audit log over db.
func New(db *store.Store) *Log { return &Log{db: db} }

var _ Recorder = (*Log)(nil)

// Record appends a row with result "done".
func (l *Log) Record(ctx context.Context, actor Actor, action string, target Target, details map[string]any) error {
	e, err := entry(actor, action, target, details, ResultDone)
	if err != nil {
		return err
	}
	_, err = l.db.InsertAudit(ctx, e)
	return err
}

// RecordRefused appends a row with result "refused".
func (l *Log) RecordRefused(ctx context.Context, actor Actor, action string, target Target, details map[string]any) error {
	e, err := entry(actor, action, target, details, ResultRefused)
	if err != nil {
		return err
	}
	_, err = l.db.InsertAudit(ctx, e)
	return err
}

// RecordTx appends a row with result "done" inside tx. If it fails, return its
// error from the WithTx callback so the change rolls back.
func RecordTx(tx *store.Tx, actor Actor, action string, target Target, details map[string]any) error {
	e, err := entry(actor, action, target, details, ResultDone)
	if err != nil {
		return err
	}
	_, err = tx.InsertAudit(e)
	return err
}

func entry(actor Actor, action string, target Target, details map[string]any, result string) (store.AuditEntry, error) {
	if strings.TrimSpace(action) == "" {
		return store.AuditEntry{}, errors.New("audit: action is required")
	}
	if actor.Login == "" {
		actor = System
	}
	if actor.Source == "" {
		actor.Source = SourceSystem
	}
	raw, err := json.Marshal(Redact(details))
	if err != nil {
		return store.AuditEntry{}, fmt.Errorf("audit: details: %w", err)
	}
	return store.AuditEntry{
		Actor: actor.Login, ActorID: actor.ID, Source: actor.Source, Action: action,
		TargetType: target.Type, TargetID: target.ID, Detail: string(raw), Result: result,
	}, nil
}

var secretKey = regexp.MustCompile(`(?i)(private[-_]?key|secret|token|password|passwd|credential|api[-_]?key)`)

const maxValueLen = 500

// Redact returns a copy of details safe to store: values under secret-looking
// keys become "[redacted]", long strings are cut, nested maps are walked.
func Redact(details map[string]any) map[string]any {
	if details == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(details))
	for k, v := range details {
		out[k] = redactValue(k, v)
	}
	return out
}

func redactValue(key string, v any) any {
	if secretKey.MatchString(key) {
		return "[redacted]"
	}
	switch x := v.(type) {
	case string:
		if len(x) > maxValueLen {
			return x[:maxValueLen] + "…"
		}
		return x
	case map[string]any:
		return Redact(x)
	default:
		return v
	}
}

// Event is one audit row as shown to a reader.
type Event struct {
	ID         int64
	At         time.Time
	Actor      string
	ActorID    string
	Source     string
	Action     string
	TargetType string
	TargetID   string
	Summary    string
	Result     string
}

// Filter narrows List: who, action, target and time (R5.27).
type Filter struct {
	Actor, Action, TargetType, TargetID string
	From, To                            time.Time
	Cursor                              string
	Limit                               int
}

// Page is one page of events, newest first.
type Page struct {
	Items      []Event
	NextCursor string
}

// MaxPage is the largest page List returns.
const MaxPage = 200

// ErrBadCursor is returned for a cursor List did not hand out.
var ErrBadCursor = errors.New("audit: bad cursor")

// List returns events newest first.
func (l *Log) List(ctx context.Context, f Filter) (Page, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > MaxPage {
		limit = MaxPage
	}
	var before int64
	if f.Cursor != "" {
		n, err := strconv.ParseInt(f.Cursor, 10, 64)
		if err != nil || n <= 0 {
			return Page{}, ErrBadCursor
		}
		before = n
	}
	rows, err := l.db.ListAudit(ctx, store.AuditFilter{
		Actor: f.Actor, Action: f.Action, TargetType: f.TargetType, TargetID: f.TargetID,
		From: f.From, To: f.To, BeforeID: before, Limit: limit + 1,
	})
	if err != nil {
		return Page{}, err
	}
	var page Page
	if len(rows) > limit {
		rows = rows[:limit]
		page.NextCursor = strconv.FormatInt(rows[len(rows)-1].ID, 10)
	}
	for _, r := range rows {
		page.Items = append(page.Items, Event{
			ID: r.ID, At: r.At, Actor: r.Actor, ActorID: r.ActorID, Source: r.Source, Action: r.Action,
			TargetType: r.TargetType, TargetID: r.TargetID, Summary: Summary(r.Detail), Result: r.Result,
		})
	}
	return page, nil
}

// Summary turns stored details into one short line: the "summary" key when
// there is one, else the "detail" text alone (the auth area stores its note
// under that key), else "key=value" pairs in key order.
func Summary(detailJSON string) string {
	var m map[string]any
	if err := json.Unmarshal([]byte(detailJSON), &m); err != nil || len(m) == 0 {
		return ""
	}
	if s, ok := m["summary"].(string); ok && s != "" {
		return s
	}
	if len(m) == 1 {
		if d, ok := m["detail"].(string); ok {
			return d
		}
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%v", k, m[k]))
	}
	return strings.Join(parts, ", ")
}

// Prune deletes rows older than cutoff and, when it deleted any, writes its own
// row with the count (R5.26). It is the only deleting path.
func (l *Log) Prune(ctx context.Context, cutoff time.Time) (int64, error) {
	n, err := l.db.DeleteAuditBefore(ctx, cutoff)
	if err != nil || n == 0 {
		return n, err // an hourly run that deletes nothing writes nothing
	}
	err = l.Record(ctx, System, "audit.retention", Target{Type: "audit_log"}, map[string]any{
		"summary": fmt.Sprintf("deleted %d audit rows older than %s", n, cutoff.UTC().Format("2006-01-02")),
		"deleted": n,
	})
	return n, err
}
