//go:build darwin

package toolstore

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// MacIdentity deletes the keychains of a job user (the hidden user of runners R5.23). It runs as the agent's
// admin identity with `sudo -u`; with no keychain it does nothing.
type MacIdentity struct{ HomeOf func(user string) string }

var _ Identity = MacIdentity{}

// Wipe implements Identity.
func (m MacIdentity) Wipe(ctx context.Context, user string) error {
	home := "/Users/" + user
	if m.HomeOf != nil {
		home = m.HomeOf(user)
	}
	kc := filepath.Join(home, "Library", "Keychains", "login.keychain-db")
	out, err := exec.CommandContext(ctx, "sudo", "-n", "-u", user, "security", "delete-keychain", kc).CombinedOutput()
	if err != nil && !strings.Contains(string(out), "could not be found") && !strings.Contains(string(out), "does not exist") {
		return fmt.Errorf("delete keychain: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
