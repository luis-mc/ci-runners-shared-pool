package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/auth"
)

type fakeLogs struct{ got LogQuery }

func (f *fakeLogs) SearchLogs(_ context.Context, q LogQuery) ([]LogEntry, int64, error) {
	f.got = q
	return []LogEntry{{ID: 9, At: time.Date(2026, 11, 20, 1, 2, 3, 0, time.UTC), Level: "warn", Machine: "m1", Message: "disk low"}}, 8, nil
}

func logsMux(role auth.Role, l LogSearcher) *http.ServeMux {
	mux := http.NewServeMux()
	Register(mux, Options{Version: "test", Authn: asRole(role), Logs: l})
	return mux
}

func getLogs(mux *http.ServeMux, target string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, target, nil))
	return rec
}

// TestLogSearchIsAdminOnly: a viewer is refused; an admin and the owner get the page, with the filters passed on (R5.21).
func TestLogSearchIsAdminOnly(t *testing.T) {
	if rec := getLogs(logsMux(auth.RoleViewer, &fakeLogs{}), "/api/v1/logs"); rec.Code != http.StatusForbidden {
		t.Fatalf("viewer got %d, want 403", rec.Code)
	}
	for _, role := range []auth.Role{auth.RoleAdmin, auth.RoleOwner} {
		f := &fakeLogs{}
		rec := getLogs(logsMux(role, f), "/api/v1/logs?q=disk+low&machine=m1&level=warn&limit=20&cursor=30")
		if rec.Code != http.StatusOK {
			t.Fatalf("%v got %d: %s", role, rec.Code, rec.Body)
		}
		if f.got.Text != "disk low" || f.got.Machine != "m1" || f.got.Level != "warn" || f.got.Limit != 20 || f.got.BeforeID != 30 {
			t.Fatalf("query %+v", f.got)
		}
		var page struct {
			Items      []struct{ Message, Level string }
			NextCursor string `json:"next_cursor"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil || len(page.Items) != 1 || page.Items[0].Level != "warn" || page.NextCursor != "8" {
			t.Fatalf("page %s (%v)", rec.Body, err)
		}
	}
}

func TestLogSearchWithoutStoreAndBadCursor(t *testing.T) {
	if rec := getLogs(logsMux(auth.RoleOwner, nil), "/api/v1/logs"); rec.Code != http.StatusNotImplemented {
		t.Fatalf("no store: %d", rec.Code)
	}
	if rec := getLogs(logsMux(auth.RoleOwner, &fakeLogs{}), "/api/v1/logs?cursor=abc"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad cursor: %d", rec.Code)
	}
}
