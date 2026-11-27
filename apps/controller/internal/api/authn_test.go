package api

import (
	"context"
	"net/http"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/auth"
)

// fixedAuthn signs every request in as one principal, with no session: like a
// CLI token it needs no CSRF token and no Origin. Tests of an area that is not
// about sign-in use it; sign-in itself is tested through the GitHub harness.
type fixedAuthn struct{ p auth.Principal }

func (f fixedAuthn) Authenticate(context.Context, *http.Request) (auth.Principal, error) {
	return f.p, nil
}

// asRole is an Authenticator that acts as a CLI user with the given role.
func asRole(role auth.Role) Authenticator {
	return fixedAuthn{auth.Principal{UserID: 1, Login: "tester", Role: role, Source: "cli", TokenID: "t"}}
}
