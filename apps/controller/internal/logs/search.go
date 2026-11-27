package logs

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// Page sizes of a search.
const (
	DefaultPageSize = 50
	MaxPageSize     = 200
)

// Query is a log search (R5.21): full text over the message and filters on
// machine, runner, job, level and time range. Zero fields do not filter.
type Query struct {
	Text    string
	Machine string
	Runner  string
	Job     string
	// MinLevel keeps lines at this level or higher; nil keeps every line.
	MinLevel *slog.Level
	From, To time.Time
	Limit    int
	// BeforeID is the cursor: the NextBefore of the previous page.
	BeforeID int64
}

// Page is one page of results. NextBefore is zero on the last page.
type Page struct {
	Lines      []Line
	NextBefore int64
}

// Validate checks the query and fills the page size.
func (q *Query) Validate() error {
	if q.Limit <= 0 {
		q.Limit = DefaultPageSize
	}
	if q.Limit > MaxPageSize {
		return fmt.Errorf("limit %d is over the largest page of %d", q.Limit, MaxPageSize)
	}
	if !q.From.IsZero() && !q.To.IsZero() && q.To.Before(q.From) {
		return errors.New("the end of the time range is before its start")
	}
	if len(q.Text) > 500 {
		return errors.New("the search text is longer than 500 characters")
	}
	return nil
}

// MatchExpr turns the user's text into an FTS5 match expression: every word is
// a quoted string, so FTS5 syntax typed by the user cannot fail the query, and
// all words must be present. Empty text gives "".
func (q Query) MatchExpr() string {
	words := strings.Fields(q.Text)
	for i, w := range words {
		words[i] = `"` + strings.ReplaceAll(w, `"`, `""`) + `"`
	}
	return strings.Join(words, " ")
}

// ParseLevel reads "debug", "info", "warn" or "error".
func ParseLevel(s string) (slog.Level, error) {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	}
	return 0, fmt.Errorf("level %q is not debug, info, warn or error", s)
}

// LevelName is the lower-case name of a level, as the API shows it.
func LevelName(l slog.Level) string {
	switch {
	case l >= slog.LevelError:
		return "error"
	case l >= slog.LevelWarn:
		return "warn"
	case l >= slog.LevelInfo:
		return "info"
	}
	return "debug"
}
