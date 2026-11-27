package auth

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/github"
)

// maxKeyPEM bounds an uploaded key; a real App key is about 1.7 KiB.
const maxKeyPEM = 16 << 10

// RotateAppKey replaces the App's private key (github-integration R5.7).
// Owner only. The candidate key signs an App JWT and GitHub's GET /app must
// accept it for the same App id before the old key is replaced in the secret
// store. When any step fails the old key stays and keeps working. The audit
// row says who and whether it worked, never the key; the key is not part of
// any error text.
func (s *Service) RotateAppKey(ctx context.Context, p Principal, pemBytes []byte) error {
	if err := Require(p, RoleOwner); err != nil {
		return err
	}
	app, err := s.store.GetApp(ctx)
	if err != nil {
		return ErrNoApp
	}
	s.keyMu.Lock()
	defer s.keyMu.Unlock()

	fail := func(reason string, err error) error {
		_ = s.store.Audit(ctx, AuditEvent{Actor: p.Login, ActorID: strconv.FormatInt(p.UserID, 10), Source: p.Source,
			Action: "app.key_rotation_failed", TargetType: "github_app", Subject: app.Slug, Detail: reason, Result: "failed"})
		return err
	}
	if len(pemBytes) == 0 || len(pemBytes) > maxKeyPEM {
		return fail("not a private key file", Invalid("that is not a GitHub App private key file. The old key was kept."))
	}
	if err := s.cfg.GitHub.CheckAppKey(ctx, app.ID, pemBytes); err != nil {
		switch {
		case errors.Is(err, github.ErrBadKey):
			return fail("not a usable private key", Invalid("that file is not a private key in PEM form (RSA). The old key was kept."))
		case errors.Is(err, github.ErrKeyRefused):
			return fail("GitHub refused the key for this App", Invalid("GitHub does not accept this key for the App %q. Download a key from this App's page at GitHub. The old key was kept.", app.Slug))
		default:
			return fail("could not check the key with GitHub", err)
		}
	}
	name := secretKey(app.ID, github.SecretPrivateKey)
	old, oldErr := s.cfg.Secrets.Get(ctx, name)
	if err := s.cfg.Secrets.Put(ctx, name, pemBytes); err != nil {
		if oldErr == nil {
			// The store may have half-written; put the old key back.
			_ = s.cfg.Secrets.Put(ctx, name, old)
		}
		return fail("the secret store refused the key", fmt.Errorf("storing the new App key: %w", err))
	}
	if s.cfg.OnApp != nil {
		s.cfg.OnApp(app) // the GitHub client drops its cached key and reads the new one
	}
	s.auditUser(ctx, p, "app.key_rotated", "github_app", app.Slug, "")
	return nil
}
