package logs

import (
	"context"
	"errors"
	"log/slog"
)

type multi []slog.Handler

func fanout(h []slog.Handler) slog.Handler {
	if len(h) == 1 {
		return h[0]
	}
	return multi(h)
}

func (m multi) Enabled(ctx context.Context, l slog.Level) bool {
	for _, h := range m {
		if h.Enabled(ctx, l) {
			return true
		}
	}
	return false
}

func (m multi) Handle(ctx context.Context, r slog.Record) error {
	var errs []error
	for _, h := range m {
		if h.Enabled(ctx, r.Level) {
			errs = append(errs, h.Handle(ctx, r.Clone()))
		}
	}
	return errors.Join(errs...)
}

func (m multi) WithAttrs(a []slog.Attr) slog.Handler {
	out := make(multi, len(m))
	for i, h := range m {
		out[i] = h.WithAttrs(a)
	}
	return out
}

func (m multi) WithGroup(n string) slog.Handler {
	out := make(multi, len(m))
	for i, h := range m {
		out[i] = h.WithGroup(n)
	}
	return out
}
