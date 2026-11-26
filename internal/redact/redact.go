// Package redact removes known secrets from log text before it leaves the agent
// (runners R5.11). It holds the secret values a process knows (registration,
// remove and JIT tokens) and also matches the shapes of GitHub tokens, private
// keys, JWTs and credential headers.
package redact

import (
	"encoding/base64"
	"io/fs"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// Mask replaces every secret (runners R5.11).
const Mask = "***"

// minLen is the shortest value kept: a shorter one would blank ordinary text.
const minLen = 6

// ValueKinds are the kinds of secret value a process holds and teaches the
// redactor with AddKind (observability R6.5). A planted-secret test covers
// each one, so a kind added here needs a row there.
var ValueKinds = []string{
	"app_private_key", "webhook_secret", "installation_token", "oauth_token", "refresh_token",
	"registration_token", "remove_token", "jit_config", "join_code", "machine_key", "session_cookie",
}

type shape struct {
	kind string
	re   *regexp.Regexp
	keep bool // keep group 1 (a name before the value)
}

var shapes = []shape{
	{kind: "github_token", re: regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{20,}`)},
	{kind: "github_pat", re: regexp.MustCompile(`github_pat_[A-Za-z0-9_]{20,}`)},
	{kind: "private_key", re: regexp.MustCompile(`(?s)-----BEGIN [A-Z0-9 ]+-----.*?-----END [A-Z0-9 ]+-----`)},
	{kind: "cli_token", re: regexp.MustCompile(`crsp_cli_[A-Za-z0-9_-]{8,}`)},
	{kind: "access_token", re: regexp.MustCompile(`crsp_acc_[A-Za-z0-9_-]{8,}`)},
	{kind: "jwt", re: regexp.MustCompile(`eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}`)},
	{kind: "auth_header", re: regexp.MustCompile(`(?i)(authorization|cookie):\s*[^\r\n]+`)},
	{kind: "token_flag", re: regexp.MustCompile(`(?i)(--token|--pat)\s+\S+`)},
	{kind: "runner_input", re: regexp.MustCompile(`(?i)(ACTIONS_RUNNER_INPUT_(?:TOKEN|JITCONFIG)=)\S+`), keep: true},
}

// ShapeKinds lists the kinds the shapes match, in table order.
func ShapeKinds() []string {
	out := make([]string, len(shapes))
	for i, sh := range shapes {
		out[i] = sh.kind
	}
	return out
}

type value struct{ v, kind string }

// Redactor is safe for concurrent use.
type Redactor struct {
	mu     sync.RWMutex
	values []value // longest first
}

// New returns a redactor that knows no values yet; the shapes always apply.
func New() *Redactor { return &Redactor{} }

// Add teaches the redactor a secret value and its base64 forms. A value that
// is too short is ignored.
func (r *Redactor) Add(v string) { r.AddKind("secret", v) }

// AddKind is Add with the kind that Tagged names in its replacement.
func (r *Redactor) AddKind(kind, v string) {
	v = strings.TrimSpace(v)
	if len(v) < minLen {
		return
	}
	forms := []string{v,
		base64.StdEncoding.EncodeToString([]byte(v)),
		base64.RawStdEncoding.EncodeToString([]byte(v)),
		base64.URLEncoding.EncodeToString([]byte(v)),
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, f := range forms {
		have := false
		for _, x := range r.values {
			if x.v == f {
				have = true
				break
			}
		}
		if !have {
			r.values = append(r.values, value{f, kind})
		}
	}
	sort.SliceStable(r.values, func(i, j int) bool { return len(r.values[i].v) > len(r.values[j].v) })
}

// Redact returns s with known values and secret shapes replaced by Mask.
func (r *Redactor) Redact(s string) string {
	return r.apply(s, func(string) string { return Mask })
}

// Tagged returns s with each secret replaced by [redacted:<kind>] (R6.5).
func (r *Redactor) Tagged(s string) string {
	return r.apply(s, func(kind string) string { return "[redacted:" + kind + "]" })
}

// TaggedBytes is Tagged for a payload, the shape of events.Options.Redact.
func (r *Redactor) TaggedBytes(b []byte) []byte { return []byte(r.Tagged(string(b))) }

func (r *Redactor) apply(s string, mask func(kind string) string) string {
	r.mu.RLock()
	for _, v := range r.values {
		s = strings.ReplaceAll(s, v.v, mask(v.kind))
	}
	r.mu.RUnlock()
	for _, sh := range shapes {
		if sh.keep {
			s = sh.re.ReplaceAllString(s, "${1}"+mask(sh.kind))
			continue
		}
		s = sh.re.ReplaceAllString(s, mask(sh.kind))
	}
	return s
}

// Files is the part of a file system a private log needs.
type Files interface {
	MkdirAll(path string, perm fs.FileMode) error
	WriteFile(path string, data []byte, perm fs.FileMode) error
	ReadFile(path string) ([]byte, error)
}

// Log modes: a runner's setup log can name paths, users and command output, so
// only the owner of the folder reads it.
const (
	LogFileMode fs.FileMode = 0o600
	LogDirMode  fs.FileMode = 0o700
)

// PrivateLog appends redacted lines to one file that only its owner can read.
type PrivateLog struct {
	FS   Files
	Path string
	R    *Redactor
	Now  func() time.Time
	mu   sync.Mutex
}

// Line writes "<time> <step> <message>" after redaction. The file is made with
// LogFileMode in a LogDirMode folder; a failure is returned, never fatal to a job.
func (l *PrivateLog) Line(step, msg string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now
	if l.Now != nil {
		now = l.Now
	}
	dir := l.Path
	if i := strings.LastIndexAny(dir, `/\`); i >= 0 {
		dir = dir[:i]
	}
	if err := l.FS.MkdirAll(dir, LogDirMode); err != nil {
		return err
	}
	old, _ := l.FS.ReadFile(l.Path)
	line := now().UTC().Format(time.RFC3339) + " " + l.R.Redact(step) + " " + l.R.Redact(strings.TrimSpace(msg)) + "\n"
	return l.FS.WriteFile(l.Path, append(old, line...), LogFileMode)
}
