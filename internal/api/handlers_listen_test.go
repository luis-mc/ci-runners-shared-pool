package api

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/internal/audit"
	"github.com/luis-mc/ci-runners-shared-pool/internal/auth"
	"github.com/luis-mc/ci-runners-shared-pool/internal/listenaddr"
	"github.com/luis-mc/ci-runners-shared-pool/internal/settings"
	"github.com/luis-mc/ci-runners-shared-pool/internal/store"
)

type oneNIC struct{}

func (oneNIC) Interfaces() ([]listenaddr.Iface, error) {
	return []listenaddr.Iface{{Name: "Ethernet", Up: true, Addrs: []netip.Addr{netip.MustParseAddr("192.168.1.20")}}}, nil
}

func listenMux(t *testing.T, role auth.Role, env string, up UpdateService) (*http.ServeMux, *audit.Log) {
	t.Helper()
	db, err := store.OpenForTests(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	svc := &listenaddr.Service{Settings: settings.New(db), Source: oneNIC{}, Running: "127.0.0.1:8460",
		Getenv: func(k string) string {
			if k == listenaddr.EnvName {
				return env
			}
			return ""
		}}
	mux := http.NewServeMux()
	Register(mux, Options{Version: "t", DB: okPinger{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Update: up, Listen: svc, Authn: asRole(role)})
	return mux, audit.New(db)
}

func put(mux *http.ServeMux, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), "PUT", "/api/v1/settings/listen-address", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)
	return rec
}

func TestListenAddressAdminOnly(t *testing.T) {
	for _, role := range []auth.Role{auth.RoleViewer} {
		mux, _ := listenMux(t, role, "", &fakeUpdate{})
		if rec := doReq(mux, "GET", "/api/v1/settings/listen-address"); rec.Code != 403 {
			t.Fatalf("get as %v: %d", role, rec.Code)
		}
		if rec := put(mux, `{"address":"192.168.1.20:8460"}`); rec.Code != 403 {
			t.Fatalf("put as %v: %d", role, rec.Code)
		}
	}
}

func TestListenAddressGetListsCandidates(t *testing.T) {
	mux, _ := listenMux(t, auth.RoleAdmin, "", &fakeUpdate{})
	rec := doReq(mux, "GET", "/api/v1/settings/listen-address")
	b := rec.Body.String()
	if rec.Code != 200 || !strings.Contains(b, `"running":"127.0.0.1:8460"`) || !strings.Contains(b, `"address":"192.168.1.20:8460"`) ||
		!strings.Contains(b, `"from_env":false`) || !strings.Contains(b, `"recommended":true`) {
		t.Fatalf("%d %s", rec.Code, b)
	}
}

func TestListenAddressPutKeepsAuditsAndRestartsOnce(t *testing.T) {
	up := &fakeUpdate{}
	mux, log := listenMux(t, auth.RoleAdmin, "", up)
	rec := put(mux, `{"address":"192.168.1.20:8460"}`)
	if rec.Code != 202 || !strings.Contains(rec.Body.String(), `"restarting":true`) {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if len(up.applied) != 1 || up.applied[0] != "restart" || !up.confirmed[0] {
		t.Fatalf("one confirmed restart through the drain path: %+v", up.applied)
	}
	if got := doReq(mux, "GET", "/api/v1/settings/listen-address").Body.String(); !strings.Contains(got, `"stored":"192.168.1.20:8460"`) {
		t.Fatal(got)
	}
	page, _ := log.List(context.Background(), audit.Filter{})
	if len(page.Items) != 1 || page.Items[0].Action != settings.ActionListenSet || page.Items[0].Actor != "tester" {
		t.Fatalf("%+v", page.Items)
	}
}

func TestListenAddressPutRefusals(t *testing.T) {
	up := &fakeUpdate{}
	mux, log := listenMux(t, auth.RoleAdmin, "", up)
	if rec := put(mux, `{"address":"10.9.9.9:8460"}`); rec.Code != 400 || !strings.Contains(rec.Body.String(), "not an address of this PC") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	envMux, _ := listenMux(t, auth.RoleAdmin, "10.0.0.2:8460", up)
	rec := put(envMux, `{"address":"192.168.1.20:8460"}`)
	if rec.Code != 409 || !strings.Contains(rec.Body.String(), "CRSP_LISTEN_ADDR") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if g := doReq(envMux, "GET", "/api/v1/settings/listen-address").Body.String(); !strings.Contains(g, `"from_env":true`) {
		t.Fatal(g)
	}
	if len(up.applied) != 0 {
		t.Fatal("a refusal must not restart")
	}
	if page, _ := log.List(context.Background(), audit.Filter{}); len(page.Items) != 0 {
		t.Fatal("a refusal must not audit a change")
	}
}
