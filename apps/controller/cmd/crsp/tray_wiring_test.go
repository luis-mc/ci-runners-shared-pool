package main

import (
	"context"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/trayserver"
)

func TestTrayLinkLandsOnTargetPathAndRefusesOpenRedirects(t *testing.T) {
	b := trayBackend{Console: consoleURL("0.0.0.0:8460")}
	ctx := context.Background()
	c := trayserver.Caller{OSUser: "1000"}
	for target, want := range map[string]string{
		"":                       "http://127.0.0.1:8460",
		"/settings/updates":      "http://127.0.0.1:8460/settings/updates",
		"//evil.example/x":       "http://127.0.0.1:8460",
		"https://evil.example/x": "http://127.0.0.1:8460",
	} {
		got, err := b.Link(ctx, c, target)
		if err != nil || got != want {
			t.Errorf("%q: %q %v", target, got, err)
		}
	}
}

type fakeIssuer struct{ user, target string }

func (f *fakeIssuer) LinkFor(_ context.Context, u, t string) (string, error) {
	f.user, f.target = u, t
	return "http://x/auth/link?code=1", nil
}

func TestTrayLinkUsesIssuerWithTheOSCaller(t *testing.T) {
	f := &fakeIssuer{}
	b := trayBackend{Console: "https://127.0.0.1:8460", Links: f}
	if u, err := b.Link(context.Background(), trayserver.Caller{OSUser: "S-1-5-21-1"}, "/x"); err != nil || u == "" {
		t.Fatal(u, err)
	}
	if f.user != "S-1-5-21-1" || f.target != "/x" {
		t.Fatalf("%+v", f)
	}
}
