package github

import (
	"fmt"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/secrets"
)

// APIVersion is the one pinned X-GitHub-Api-Version (R6.8). Changing it is a
// reviewed change with new fixtures.
const APIVersion = "2022-11-28"

// Secret names kept for each App (R6.1).
const (
	SecretPrivateKey    = "private-key"
	SecretClientSecret  = "client-secret"
	SecretWebhookSecret = "webhook-secret"
)

// SecretKey is the OS-store key of an App secret, for example
// crsp/github-app/1234567/private-key.
func SecretKey(appID int64, name string) secrets.Key {
	return secrets.Key(fmt.Sprintf("crsp/github-app/%d/%s", appID, name))
}
