//go:build !windows

package main

import (
	"context"
	"log/slog"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/auth"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/github"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/secrets"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/store"
)

// newAuthArea (tests only) builds the sign-in service on the controller's database and the
// GitHub area of the API. Secrets stay in the OS secret store (github-integration R6.1).
func newAuthArea(db *store.Store, sec secrets.Store, console string, logger *slog.Logger) (*api.GitHubArea, authLinks) {
	gh := github.New(github.Config{Secrets: sec, Logger: logger})
	svc := auth.New(db.Auth(), auth.Config{GitHub: gh, Secrets: sec,
		ConsoleURLs: func() []string { return []string{console} },
		OnApp:       func(a auth.AppRecord) { gh.UseApp(a.ID, a.ClientID) }})
	// After a restart the client must know the App again.
	if a, err := db.Auth().GetApp(context.Background()); err == nil {
		gh.UseApp(a.ID, a.ClientID)
	}
	return &api.GitHubArea{Auth: svc, Logger: logger}, authLinks{svc: svc, console: console}
}
