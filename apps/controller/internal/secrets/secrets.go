// Package secrets is the OS secret store seam (decision 0002; github-integration
// R6.1; machines R5.7; operations R5.26).
//
// The GitHub App private key, client secret and webhook secret, the
// controller's certificate key, and an agent's machine key and pinned
// certificate live only here: Windows Credential Manager or DPAPI, macOS
// Keychain, Linux Secret Service, or a file with mode 0600 owned by the
// service account. They never go to the database, a log, a backup or a config
// file (observability R6.5, operations R6.8). One implementation per store is
// added by the feature that needs it; tests use an in-memory fake.
package secrets

import (
	"context"
	"errors"
)

// ErrNotFound is returned by Get when no secret is stored under the key. After
// a restore on a new machine the doctor lists each such key (operations R5.26).
var ErrNotFound = errors.New("secret not found")

// ErrUnavailable is returned when the OS store cannot be reached. Callers fail
// closed: a join or App setup stops with the reason (machines section 8).
var ErrUnavailable = errors.New("secret store unavailable")

// Key names one secret. Keys are stable and say what they hold, for example
// "crsp/github-app/1234567/private-key" or "crsp/agent/machine-key". A key is
// not itself secret and may be logged.
type Key string

// Store reads and writes secrets in the OS secret store.
type Store interface {
	// Get returns the secret under key, or ErrNotFound.
	Get(ctx context.Context, key Key) ([]byte, error)
	// Put stores value under key, replacing any old value.
	Put(ctx context.Context, key Key, value []byte) error
	// Delete removes the secret under key. Deleting a missing key is not an error.
	Delete(ctx context.Context, key Key) error
	// Backend names the store in use ("wincred", "keychain", "secret-service",
	// "file"), for the doctor and logs.
	Backend() string
}
