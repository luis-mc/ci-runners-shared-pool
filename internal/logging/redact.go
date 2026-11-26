// Package logging puts the redactor in front of every log path (github-integration
// R6.3). A slog handler redacts the message, every attribute value (strings,
// errors, stringers, any other value by its text) and every group, so a token,
// a PEM block or a known secret value never reaches the log writer.
package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"

	"github.com/luis-mc/ci-runners-shared-pool/internal/redact"
)

// Handler redacts what an inner handler would write.
type Handler struct {
	inner slog.Handler
	r     *redact.Redactor
	red   func(string) string
}

// NewHandler wraps inner. A nil redactor gets a fresh one (shapes only).
func NewHandler(inner slog.Handler, r *redact.Redactor) *Handler {
	if r == nil {
		r = redact.New()
	}
	return &Handler{inner: inner, r: r, red: r.Redact}
}

// NewTaggedHandler is NewHandler with [redacted:<kind>] in place of the mask
// (observability R6.5).
func NewTaggedHandler(inner slog.Handler, r *redact.Redactor) *Handler {
	h := NewHandler(inner, r)
	h.red = h.r.Tagged
	return h
}

// New returns a JSON logger on w whose output is redacted.
func New(w io.Writer, level slog.Leveler, r *redact.Redactor) *slog.Logger {
	return slog.New(NewHandler(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level}), r))
}

// Enabled implements slog.Handler.
func (h *Handler) Enabled(ctx context.Context, l slog.Level) bool { return h.inner.Enabled(ctx, l) }

// Handle implements slog.Handler.
func (h *Handler) Handle(ctx context.Context, rec slog.Record) error {
	out := slog.NewRecord(rec.Time, rec.Level, h.red(rec.Message), rec.PC)
	rec.Attrs(func(a slog.Attr) bool {
		out.AddAttrs(h.clean(a))
		return true
	})
	return h.inner.Handle(ctx, out)
}

// WithAttrs implements slog.Handler.
func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	clean := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		clean[i] = h.clean(a)
	}
	return &Handler{inner: h.inner.WithAttrs(clean), r: h.r, red: h.red}
}

// WithGroup implements slog.Handler.
func (h *Handler) WithGroup(name string) slog.Handler {
	return &Handler{inner: h.inner.WithGroup(name), r: h.r, red: h.red}
}

func (h *Handler) clean(a slog.Attr) slog.Attr {
	v := a.Value.Resolve()
	a.Key = h.red(a.Key)
	switch v.Kind() {
	case slog.KindString:
		a.Value = slog.StringValue(h.red(v.String()))
	case slog.KindGroup:
		g := v.Group()
		out := make([]slog.Attr, len(g))
		for i, x := range g {
			out[i] = h.clean(x)
		}
		a.Value = slog.GroupValue(out...)
	case slog.KindAny:
		// An error, a Stringer or a struct: judge it by its text. When nothing
		// is redacted the original value stays (the JSON handler keeps its shape).
		text := fmt.Sprintf("%+v", v.Any())
		if red := h.red(text); red != text {
			a.Value = slog.StringValue(red)
		}
	default:
		a.Value = v
	}
	return a
}
