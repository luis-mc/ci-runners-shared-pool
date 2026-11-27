// Package digest writes the weekly digest (docs/features/ci-run-insights R5.38): a local Markdown file with
// the top 3 findings and what changed. It is never sent anywhere (observability R6.7). CIX8 contract: types
// and interfaces only.
package digest

import (
	"context"
	"time"
)

// Week is an ISO week, such as "2026-W47".
type Week string

// Digest is one written digest.
type Digest struct {
	Week      Week
	Path      string // relative to the data dir: ci-data/digests/<YYYY>-W<ww>.md
	Text      string
	TopIDs    []int64
	WrittenAt time.Time
}

// Writer writes the digest of the week that ended before now, every Monday at 08:00 Europe/Lisbon time. It reads
// the open findings, the findings opened and resolved in the week, guardrail changes per repository and the
// week's wait, hold and run totals, then keeps the last 52 files (R6.2).
type Writer interface {
	Write(ctx context.Context, now time.Time) (Digest, error)
}

// Reader answers listInsightDigests with the last n digests, newest first, with their text.
type Reader interface {
	Last(ctx context.Context, n int) ([]Digest, error)
}
