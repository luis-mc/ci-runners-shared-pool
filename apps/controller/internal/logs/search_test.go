package logs

import (
	"strings"
	"testing"
	"time"
)

func TestQueryValidation(t *testing.T) {
	q := Query{}
	if err := q.Validate(); err != nil || q.Limit != DefaultPageSize {
		t.Fatalf("default: %v limit %d", err, q.Limit)
	}
	now := time.Now()
	for name, bad := range map[string]Query{
		"big page":      {Limit: MaxPageSize + 1},
		"range reverse": {From: now, To: now.Add(-time.Hour)},
		"long text":     {Text: strings.Repeat("a", 501)},
	} {
		if err := bad.Validate(); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	if got := (Query{Text: `a "b" c`}).MatchExpr(); got != `"a" """b""" "c"` {
		t.Fatalf("match expression %q", got)
	}
	if _, err := ParseLevel("loud"); err == nil {
		t.Fatal("unknown level must fail")
	}
}
