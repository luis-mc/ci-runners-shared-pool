package api

import (
	"context"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api/gen"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/auth"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/webhookhealth"
)

// WebhookHealth is what the console reads about a repository's webhook
// deliveries (github-integration R5.50). *webhookhealth.Tracker implements it.
type WebhookHealth interface {
	Get(fullName string) webhookhealth.State
	SwitchBack(fullName string) webhookhealth.State
	WebhooksOff() bool
}

var _ WebhookHealth = (*webhookhealth.Tracker)(nil)

func toWebhookHealth(st webhookhealth.State) gen.WebhookHealth {
	out := gen.WebhookHealth{Mode: gen.WebhookHealthMode(st.Mode), FailuresInARow: st.Failures}
	if !st.LastAt.IsZero() {
		at := st.LastAt
		res := gen.WebhookHealthLastResult(st.Last)
		out.LastDeliveryAt, out.LastResult = &at, &res
	}
	if !st.SwitchedAt.IsZero() {
		at := st.SwitchedAt
		out.PollingSince = &at
	}
	return out
}

// withHealth adds the webhook health to a repository when the area has it.
func (s *server) withHealth(r gen.Repository) gen.Repository {
	a := s.ghArea()
	if a == nil || a.Health == nil {
		return r
	}
	h := toWebhookHealth(a.Health.Get(r.FullName))
	if a.Health.WebhooksOff() {
		off := true
		h.WebhooksOff = &off
	}
	r.Webhook = &h
	return r
}

// SetRepositoryWebhookMode implements PUT /api/v1/repositories/{repositoryId}/webhook-mode.
func (s *server) SetRepositoryWebhookMode(ctx context.Context, req gen.SetRepositoryWebhookModeRequestObject) (gen.SetRepositoryWebhookModeResponseObject, error) {
	fail := func(err error) (gen.SetRepositoryWebhookModeResponseObject, error) {
		code, body := failure(err)
		return gen.SetRepositoryWebhookModedefaultJSONResponse{StatusCode: code, Body: body}, nil
	}
	a := s.ghArea()
	if a == nil || a.Health == nil {
		return gen.SetRepositoryWebhookModedefaultJSONResponse{StatusCode: 501, Body: notImplemented()}, nil
	}
	if req.Body == nil || req.Body.Mode != gen.WebhookModeUpdateModeWebhook {
		return fail(auth.Invalid("only the webhook mode can be chosen"))
	}
	v, err := a.Repos.Get(ctx, s.principal(ctx), req.RepositoryId)
	if err != nil {
		return fail(err)
	}
	return gen.SetRepositoryWebhookMode200JSONResponse(toWebhookHealth(a.Health.SwitchBack(v.FullName))), nil
}

func toAppWebhook(st auth.WebhookStatus) gen.AppWebhook {
	out := gen.AppWebhook{State: gen.AppWebhookState(st.State)}
	if st.Address != "" {
		a := st.Address
		out.Address = &a
	}
	if !st.LastDeliveryAt.IsZero() {
		at := st.LastDeliveryAt
		out.LastDeliveryAt = &at
	}
	if st.LastResult != "" {
		r := gen.AppWebhookLastResult(st.LastResult)
		out.LastResult = &r
	}
	if st.EventsLink != "" {
		l := st.EventsLink
		out.EventsLink = &l
	}
	return out
}

// GetAppWebhook implements GET /api/v1/app/webhook (github-integration R5.51).
func (s *server) GetAppWebhook(ctx context.Context, _ gen.GetAppWebhookRequestObject) (gen.GetAppWebhookResponseObject, error) {
	a := s.ghArea()
	if a == nil || a.Auth == nil {
		return gen.GetAppWebhookdefaultJSONResponse{StatusCode: 501, Body: notImplemented()}, nil
	}
	st, err := a.Auth.WebhookStatus(ctx)
	if err != nil {
		code, body := failure(err)
		return gen.GetAppWebhookdefaultJSONResponse{StatusCode: code, Body: body}, nil
	}
	return gen.GetAppWebhook200JSONResponse(toAppWebhook(st)), nil
}

// SetAppWebhook implements PUT /api/v1/app/webhook (github-integration R5.51).
func (s *server) SetAppWebhook(ctx context.Context, req gen.SetAppWebhookRequestObject) (gen.SetAppWebhookResponseObject, error) {
	fail := func(err error) (gen.SetAppWebhookResponseObject, error) {
		code, body := failure(err)
		return gen.SetAppWebhookdefaultJSONResponse{StatusCode: code, Body: body}, nil
	}
	a := s.ghArea()
	if a == nil || a.Auth == nil {
		return gen.SetAppWebhookdefaultJSONResponse{StatusCode: 501, Body: notImplemented()}, nil
	}
	if req.Body == nil {
		return fail(auth.Invalid("send the public address"))
	}
	st, err := a.Auth.SetWebhook(ctx, s.principal(ctx), req.Body.Address)
	if err != nil {
		return fail(err)
	}
	return gen.SetAppWebhook200JSONResponse(toAppWebhook(st)), nil
}

// ClearAppWebhook implements DELETE /api/v1/app/webhook (github-integration R5.51).
func (s *server) ClearAppWebhook(ctx context.Context, _ gen.ClearAppWebhookRequestObject) (gen.ClearAppWebhookResponseObject, error) {
	a := s.ghArea()
	if a == nil || a.Auth == nil {
		return gen.ClearAppWebhookdefaultJSONResponse{StatusCode: 501, Body: notImplemented()}, nil
	}
	st, err := a.Auth.ClearWebhook(ctx, s.principal(ctx))
	if err != nil {
		code, body := failure(err)
		return gen.ClearAppWebhookdefaultJSONResponse{StatusCode: code, Body: body}, nil
	}
	return gen.ClearAppWebhook200JSONResponse(toAppWebhook(st)), nil
}
