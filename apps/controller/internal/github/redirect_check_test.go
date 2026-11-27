package github

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCheckRedirectURI(t *testing.T) { // OPS16
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Query().Get("redirect_uri")
		switch got {
		case "https://known.lan:8460/auth/github/callback":
			http.Redirect(w, r, "/login?return_to=x", http.StatusFound)
		case "https://boom.lan:8460/auth/github/callback":
			http.Error(w, "boom", http.StatusBadGateway)
		default:
			http.Error(w, "Be careful! The redirect_uri is not associated with this application.", http.StatusNotFound)
		}
	}))
	defer srv.Close()
	c := New(Config{WebBase: srv.URL})
	ctx := context.Background()
	if _, err := c.CheckRedirectURI(ctx, "x"); err == nil {
		t.Fatal("no App yet")
	}
	c.UseApp(1, "Iv1.client")
	if ok, err := c.CheckRedirectURI(ctx, "https://known.lan:8460/auth/github/callback"); err != nil || !ok {
		t.Fatalf("known: %v %v", ok, err)
	}
	if ok, err := c.CheckRedirectURI(ctx, "https://new.lan:8460/auth/github/callback"); err != nil || ok {
		t.Fatalf("unknown: %v %v", ok, err)
	}
	if _, err := c.CheckRedirectURI(ctx, "https://boom.lan:8460/auth/github/callback"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("a server error is not an answer: %v", err)
	}
}
