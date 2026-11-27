package debugaccess

import (
	"bytes"
	"context"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/store"
)

// Transcript limits (R5.25, R6.6).
const (
	TranscriptKeep        = 30 * 24 * time.Hour
	TranscriptMaxSession  = 5 << 20
	TranscriptMaxPool     = 500 << 20
	transcriptPendingMax  = 64 << 10
	transcriptNoBlockMax  = 4 << 10
	transcriptBeginMarker = "-----BEGIN "
	transcriptEndMarker   = "-----END "
)

// TranscriptAppender is where a recorder writes; *store.Store implements it.
type TranscriptAppender interface {
	AppendDebugTranscript(ctx context.Context, d store.DebugTranscript) error
}

// Recorder keeps the terminal output of one session (R5.25). Output is held
// in memory until a line ends, so a secret is redacted whole and never split
// across two writes; a private key block is held until its END line (up to
// 64 KB). Only redacted text reaches the store. The store copy stops at 5 MB
// and is marked cut. Safe for concurrent use.
type Recorder struct {
	SessionID string
	Store     TranscriptAppender
	Redact    func(string) string
	Now       func() time.Time

	mu      sync.Mutex
	pending []byte
	stored  int64
	cut     bool
	opened  time.Time
}

// Write takes output bytes from the stream; it never fails the stream.
func (r *Recorder) Write(ctx context.Context, p []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cut {
		return nil
	}
	r.pending = append(r.pending, p...)
	return r.flushLocked(ctx, false)
}

// Close stores what is still held.
func (r *Recorder) Close(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.flushLocked(ctx, true)
}

func (r *Recorder) flushLocked(ctx context.Context, all bool) error {
	n := len(r.pending)
	if !all {
		n = bytes.LastIndexByte(r.pending, '\n') + 1
		open := bytes.LastIndex(r.pending, []byte(transcriptBeginMarker))
		if open >= 0 && bytes.Index(r.pending[open:], []byte(transcriptEndMarker)) < 0 {
			// A key block has begun and not ended: hold from its start.
			n = open
			if len(r.pending) > transcriptPendingMax {
				n = len(r.pending)
			}
		} else if n == 0 && len(r.pending) > transcriptNoBlockMax {
			n = len(r.pending)
		}
	}
	if n <= 0 {
		return nil
	}
	chunk := r.pending[:n]
	r.pending = append([]byte(nil), r.pending[n:]...)
	text := string(bytes.ToValidUTF8(chunk, []byte("�")))
	if r.Redact != nil {
		text = r.Redact(text)
	}
	if room := int64(TranscriptMaxSession) - r.stored; int64(len(text)) > room {
		text = cutUTF8(text, int(room))
		r.cut = true
		r.pending = nil
	}
	if text == "" && !r.cut {
		return nil
	}
	now := time.Now()
	if r.Now != nil {
		now = r.Now()
	}
	if r.opened.IsZero() {
		r.opened = now
	}
	r.stored += int64(len(text))
	return r.Store.AppendDebugTranscript(ctx, store.DebugTranscript{SessionID: r.SessionID, Text: text, Bytes: int64(len(text)),
		Truncated: r.cut, ExpiresAt: r.opened.Add(TranscriptKeep), UpdatedAt: now})
}

func cutUTF8(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
