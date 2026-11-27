package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api/gen"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/audit"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/listenaddr"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/update"
)

// ListenService is what the listen address routes need (operations R5.90).
// *listenaddr.Service implements it.
type ListenService interface {
	State(ctx context.Context) (listenaddr.State, error)
	Choose(ctx context.Context, actor audit.Actor, addr string) (string, error)
}

var _ ListenService = (*listenaddr.Service)(nil)

// GetListenAddress implements GET /api/v1/settings/listen-address.
func (s *server) GetListenAddress(ctx context.Context, _ gen.GetListenAddressRequestObject) (gen.GetListenAddressResponseObject, error) {
	fail := func(f opsFailure) (gen.GetListenAddressResponseObject, error) {
		return gen.GetListenAddressdefaultJSONResponse{StatusCode: f.status, Body: f.body}, nil
	}
	if s.opts.Listen == nil {
		return fail(unavailable())
	}
	st, err := s.opts.Listen.State(ctx)
	if err != nil {
		return fail(s.opsError(err))
	}
	out := gen.ListenAddress{Running: st.Running, FromEnv: st.FromEnv, Candidates: make([]gen.ListenAddressCandidate, 0, len(st.Candidates))}
	if st.Stored != "" {
		v := st.Stored
		out.Stored = &v
	}
	if st.FromEnv {
		n := st.EnvName
		out.EnvName = &n
	}
	if st.Problem != "" {
		p := st.Problem
		out.Problem = &p
	}
	for _, c := range st.Candidates {
		g := gen.ListenAddressCandidate{Id: gen.ListenAddressCandidateId(c.ID), Label: c.Label, Who: c.Who}
		if c.Address != "" {
			a := c.Address
			g.Address = &a
		}
		if c.Recommended {
			r := true
			g.Recommended = &r
		}
		out.Candidates = append(out.Candidates, g)
	}
	return gen.GetListenAddress200JSONResponse(out), nil
}

// PutListenAddress implements PUT /api/v1/settings/listen-address: it keeps
// the choice, audits it, and restarts the controller once through the drain
// path, so running jobs finish first.
func (s *server) PutListenAddress(ctx context.Context, req gen.PutListenAddressRequestObject) (gen.PutListenAddressResponseObject, error) {
	fail := func(f opsFailure) (gen.PutListenAddressResponseObject, error) {
		return gen.PutListenAddressdefaultJSONResponse{StatusCode: f.status, Body: f.body}, nil
	}
	if s.opts.Listen == nil || s.opts.Update == nil {
		return fail(unavailable())
	}
	if req.Body == nil {
		return fail(envelope(http.StatusBadRequest, CodeBadRequest, "the request is not valid"))
	}
	addr, err := s.opts.Listen.Choose(ctx, publicActor(ctx, s.principal(ctx)), req.Body.Address)
	var bad *listenaddr.ErrInvalid
	switch {
	case errors.As(err, &bad):
		return fail(envelope(http.StatusBadRequest, CodeBadRequest, bad.Reason))
	case errors.Is(err, listenaddr.ErrFromEnv):
		return fail(envelope(http.StatusConflict, CodeConflict, err.Error()))
	case err != nil:
		return fail(s.opsError(err))
	}
	yes := true
	if _, f := s.applyKind(ctx, update.KindRestart, &yes); f != nil {
		f.body.Error.Message = "The address " + addr + " is kept, but the controller did not restart: " + f.body.Error.Message + " It listens on it after the next restart."
		return fail(*f)
	}
	return gen.PutListenAddress202JSONResponse{Address: addr, Restarting: true}, nil
}
