package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api/gen"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/auth"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/events"
)

// LiveService is what the live routes need: the overview snapshot and the
// event hub (observability R5.1 to R5.3, R6.3). The overview package and the
// events hub implement the two halves.
type LiveService interface {
	Overview(ctx context.Context) (gen.Overview, error)
	Subscribe(ctx context.Context, lastEventID uint64) (events.Subscription, error)
}

// Live joins an overview source and a hub into a LiveService.
type Live struct {
	Overviews interface {
		Get(ctx context.Context) (gen.Overview, error)
	}
	Hub events.Hub
	// KeepAlive overrides events.KeepAlive; tests shorten it.
	KeepAlive time.Duration
	// Recheck is how often an open stream asks again whether its session still stands and its role still reads
	// (R6.4). Zero means SessionRecheck.
	Recheck time.Duration
}

// SessionRecheck is the gap between two session checks of an open event stream.
const SessionRecheck = 10 * time.Second

// Overview implements LiveService.
func (l Live) Overview(ctx context.Context) (gen.Overview, error) { return l.Overviews.Get(ctx) }

// Subscribe implements LiveService.
func (l Live) Subscribe(ctx context.Context, last uint64) (events.Subscription, error) {
	return l.Hub.Subscribe(ctx, last)
}

// GetOverview implements GET /api/v1/overview.
func (s *server) GetOverview(ctx context.Context, _ gen.GetOverviewRequestObject) (gen.GetOverviewResponseObject, error) {
	if s.opts.Live == nil {
		return gen.GetOverviewdefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: notImplemented()}, nil
	}
	o, err := s.opts.Live.Overview(ctx)
	if err != nil {
		s.opts.Logger.Error("overview", "err", err)
		return gen.GetOverviewdefaultJSONResponse{StatusCode: http.StatusInternalServerError,
			Body: gen.ErrorEnvelope{Error: gen.ErrorBody{Code: CodeInternal, Message: "the overview could not be built"}}}, nil
	}
	s.markOverview(ctx, &o)
	return gen.GetOverview200JSONResponse(o), nil
}

// sseResponse writes the subscription as text/event-stream until the client
// leaves. The strict handler calls Visit after the operation returns; the
// request context is kept in the response because Visit gets only the writer.
type sseResponse struct {
	ctx       context.Context
	sub       events.Subscription
	keepAlive time.Duration
	// stillAllowed says whether the session of the stream still stands and its role still reads (R6.4); nil
	// skips the check. recheck is the gap between two checks.
	stillAllowed func(context.Context) bool
	recheck      time.Duration
}

func (r sseResponse) VisitStreamEventsResponse(w http.ResponseWriter) error {
	ctx, cancel := context.WithCancel(r.ctx)
	defer cancel()
	if r.stillAllowed != nil {
		go func() {
			tick := time.NewTicker(r.recheck)
			defer tick.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-tick.C:
					if !r.stillAllowed(ctx) {
						cancel() // the stream ends: the session ended or the role was removed
						return
					}
				}
			}
		}()
	}
	req := (&http.Request{}).WithContext(ctx)
	events.Serve(w, req, r.sub, r.keepAlive)
	return nil
}

// streamStillAllowed builds the session check of an open stream: the same authenticator and the same role rule
// as the gate in front of streamEvents. An answer that is neither "not signed in" nor a role that is too low
// (a database hiccup) leaves the stream open; the next check asks again.
func (s *server) streamStillAllowed(ctx context.Context) func(context.Context) bool {
	req := requestFrom(ctx)
	if s.authn == nil || req == nil {
		return nil
	}
	return func(ctx context.Context) bool {
		p, err := s.authn.Authenticate(ctx, req)
		switch {
		case err == nil:
			return roleAllows(p, authUser)
		case errors.Is(err, auth.ErrUnauthenticated):
			return false
		}
		return ctx.Err() == nil
	}
}

// StreamEvents implements GET /api/v1/events (observability R5.2, R5.3, R6.3).
// The session is checked by the auth layer before this runs (R6.4): nothing is
// sent before it.
func (s *server) StreamEvents(ctx context.Context, req gen.StreamEventsRequestObject) (gen.StreamEventsResponseObject, error) {
	if s.opts.Live == nil {
		return gen.StreamEventsdefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: notImplemented()}, nil
	}
	var last uint64
	if req.Params.LastEventID != nil {
		last = events.ParseLastEventID(*req.Params.LastEventID)
	}
	sub, err := s.opts.Live.Subscribe(ctx, last)
	switch {
	case errors.Is(err, events.ErrTooManyStreams):
		return gen.StreamEventsdefaultJSONResponse{StatusCode: http.StatusTooManyRequests,
			Body: gen.ErrorEnvelope{Error: gen.ErrorBody{Code: CodeRateLimited, Message: "too many open event streams; close one and try again"}}}, nil
	case err != nil:
		s.opts.Logger.Error("event stream", "err", err)
		return gen.StreamEventsdefaultJSONResponse{StatusCode: http.StatusInternalServerError,
			Body: gen.ErrorEnvelope{Error: gen.ErrorBody{Code: CodeInternal, Message: "the event stream could not be opened"}}}, nil
	}
	var ka time.Duration
	recheck := SessionRecheck
	if l, ok := s.opts.Live.(Live); ok {
		ka = l.KeepAlive
		if l.Recheck > 0 {
			recheck = l.Recheck
		}
	}
	return sseResponse{ctx: ctx, sub: sub, keepAlive: ka, stillAllowed: s.streamStillAllowed(ctx), recheck: recheck}, nil
}
