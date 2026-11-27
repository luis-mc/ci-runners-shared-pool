package api

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/auth"
)

// R6.5: the two role tables together equal x-auth in the contract, operation by
// operation, with none missing and none extra.
func TestEveryOperationHasItsRole(t *testing.T) {
	seen := map[string]bool{}
	for _, op := range loadContractOps(t) {
		seen[op.ID] = true
		got, ok := roleFor(op.ID)
		if !ok {
			t.Errorf("%s %s (%s): x-auth %q but no role rule in authz.go (otherAreaAuth) or handlers_auth.go (githubAreaAuth)", op.Method, op.Path, op.ID, op.Auth)
			continue
		}
		if got != op.Auth {
			t.Errorf("%s: x-auth %q, role rule %q", op.ID, op.Auth, got)
		}
	}
	for _, table := range []map[string]string{githubAreaAuth, otherAreaAuth} {
		for id := range table {
			if !seen[id] {
				t.Errorf("%s has a role rule but is not in the contract", id)
			}
		}
	}
	for id := range otherAreaAuth {
		if _, dup := githubAreaAuth[id]; dup {
			t.Errorf("%s is in both role tables", id)
		}
	}
}

// muxAs mounts the API with one signed-in role, or with nobody ("").
func muxAs(role auth.Role) *http.ServeMux {
	mux := http.NewServeMux()
	opts := Options{Version: "t", DB: fakeDB{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if role != "" {
		opts.Authn = asRole(role)
	}
	Register(mux, opts)
	return mux
}

func callOp(mux *http.ServeMux, op contractOp) int {
	target := pathParam.ReplaceAllString(op.Path, "1") + "?code=c&state=s"
	var body io.Reader
	if op.HasBody {
		body = bytes.NewReader([]byte(`{}`))
	}
	req := httptest.NewRequestWithContext(context.Background(), op.Method, target, body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec.Code
}

// R6.5: for every operation of the contract and every role, the API answers as
// x-auth says, whatever the console shows: nobody signed in gets 401 on all
// but public operations; a role below x-auth gets 403, the same for a viewer's
// change request; a role at or above it gets past the gate (anything but 401
// or 403; the stubs and unwired areas answer 501).
func TestRoleMatrixOverAllOperations(t *testing.T) {
	rank := map[string]int{authPublic: 0, authUser: 1, authAdmin: 2, authOwner: 3}
	roles := []struct {
		role auth.Role
		rank int
	}{{"", 0}, {auth.RoleViewer, 1}, {auth.RoleAdmin, 2}, {auth.RoleOwner, 3}}
	ops := loadContractOps(t)
	if len(ops) < 50 {
		t.Fatalf("only %d operations in the contract", len(ops))
	}
	for _, r := range roles {
		mux := muxAs(r.role)
		for _, op := range ops {
			if op.ID == "receiveGithubWebhook" {
				continue // served ahead of the API with its own signature check
			}
			got := callOp(mux, op)
			need := rank[op.Auth]
			switch {
			case need == 0:
				if got == 401 || got == 403 {
					t.Errorf("role %q: public %s answered %d", r.role, op.ID, got)
				}
			case r.rank == 0:
				if got != 401 {
					t.Errorf("nobody signed in: %s (%s) answered %d, want 401", op.ID, op.Auth, got)
				}
			case r.rank < need:
				if got != 403 {
					t.Errorf("role %q: %s (%s) answered %d, want 403", r.role, op.ID, op.Auth, got)
				}
			default:
				if got == 401 || got == 403 {
					t.Errorf("role %q: %s (%s) answered %d, want it to pass the gate", r.role, op.ID, op.Auth, got)
				}
			}
		}
	}
}

// R6.5: a viewer's change request gets 403 on every changing operation that
// needs more than a signed-in user.
func TestViewerChangeRequestsGet403(t *testing.T) {
	mux := muxAs(auth.RoleViewer)
	n := 0
	for _, op := range loadContractOps(t) {
		if !isUnsafe(op.Method) || op.Auth == authPublic || op.Auth == authUser {
			continue
		}
		n++
		if got := callOp(mux, op); got != http.StatusForbidden {
			t.Errorf("viewer %s %s (%s): %d, want 403", op.Method, op.Path, op.ID, got)
		}
	}
	if n < 20 {
		t.Fatalf("only %d admin or owner changes found", n)
	}
}

// R6.5: an operation without a role rule is refused, never opened.
func TestOperationWithoutRoleRuleFailsClosed(t *testing.T) {
	z := newAuthz(asRole(auth.RoleOwner), true, slog.New(slog.NewTextHandler(io.Discard, nil)))
	called := false
	h := z.middleware()(func(context.Context, http.ResponseWriter, *http.Request, any) (any, error) {
		called = true
		return nil, nil
	}, "BrandNewOperation")
	rec := httptest.NewRecorder()
	_, _ = h(context.Background(), rec, httptest.NewRequestWithContext(context.Background(), "GET", "/x", nil), nil)
	if called || rec.Code != http.StatusForbidden {
		t.Fatalf("called=%v status=%d", called, rec.Code)
	}
}
