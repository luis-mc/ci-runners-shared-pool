package api

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/internal/audit"
	"github.com/luis-mc/ci-runners-shared-pool/internal/auth"
	"github.com/luis-mc/ci-runners-shared-pool/internal/machines"
)

// LOOK49: a change made in the console is audited under the signed-in login in
// every area, not "unknown" (runners, machines) or "system" (audit).
func TestMiddlewareNamesTheSignedInActorForEveryArea(t *testing.T) {
	who := fixedAuthn{auth.Principal{UserID: 7, Login: "luis-mc", Role: auth.RoleOwner, Source: "web"}}
	z := newAuthz(who, true, slog.New(slog.NewTextHandler(io.Discard, nil)))
	var got struct {
		runners, machine string
		audit            audit.Actor
	}
	h := z.middleware()(func(ctx context.Context, _ http.ResponseWriter, _ *http.Request, _ any) (any, error) {
		got.runners = actorFrom(ctx)
		got.machine = machines.ActorFrom(ctx)
		got.audit = audit.ActorFrom(ctx)
		return nil, nil
	}, "getSession")
	rec := httptest.NewRecorder()
	_, _ = h(context.Background(), rec, httptest.NewRequestWithContext(context.Background(), "GET", "/x", nil), nil)
	if got.runners != "luis-mc" || got.machine != "luis-mc" {
		t.Fatalf("runners=%q machines=%q", got.runners, got.machine)
	}
	if got.audit.Login != "luis-mc" || got.audit.ID != "7" || got.audit.Source != "web" {
		t.Fatalf("audit actor = %+v", got.audit)
	}
}
