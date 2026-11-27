package secrets

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Runner runs an external command with optional stdin and returns its stdout.
// It is a seam so the command-based stores are tested without the real tools.
type Runner func(ctx context.Context, stdin []byte, name string, args ...string) ([]byte, error)

// ExecRunner runs real commands with a 10-second deadline.
func ExecRunner(ctx context.Context, stdin []byte, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return out.Bytes(), err
	}
	return out.Bytes(), nil
}

const service = "crsp"

// SecretService is the Linux Secret Service store, through the secret-tool
// command. The value goes through stdin, never the command line.
type SecretService struct{ run Runner }

// NewSecretService returns the store; probe it with Available before use.
func NewSecretService(run Runner) *SecretService {
	if run == nil {
		run = ExecRunner
	}
	return &SecretService{run: run}
}

// Available reports whether a Secret Service answers. A missing tool or a
// missing session bus both mean "use the file fallback".
func (s *SecretService) Available(ctx context.Context) bool {
	_, err := s.run(ctx, nil, "secret-tool", "search", "service", service, "key", "crsp-probe")
	if err == nil {
		return true
	}
	var ee *exec.ExitError
	// secret-tool search exits 1 when nothing matches; any other failure is
	// an unusable store.
	return errors.As(err, &ee) && ee.ExitCode() == 1
}

// Get implements Store.
func (s *SecretService) Get(ctx context.Context, key Key) ([]byte, error) {
	out, err := s.run(ctx, nil, "secret-tool", "lookup", "service", service, "key", string(key))
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && ee.ExitCode() == 1 && len(out) == 0 {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("%w: secret-tool lookup", ErrUnavailable)
	}
	if len(out) == 0 {
		return nil, ErrNotFound
	}
	b, err := hex.DecodeString(strings.TrimSpace(string(out)))
	if err != nil {
		return nil, fmt.Errorf("%w: unreadable value", ErrUnavailable)
	}
	return b, nil
}

// Put implements Store. Values are hex encoded so binary and multi-line
// values (PEM) survive the tool.
func (s *SecretService) Put(ctx context.Context, key Key, value []byte) error {
	_, err := s.run(ctx, []byte(hex.EncodeToString(value)), "secret-tool", "store",
		"--label=crsp "+string(key), "service", service, "key", string(key))
	if err != nil {
		return fmt.Errorf("%w: secret-tool store", ErrUnavailable)
	}
	return nil
}

// Delete implements Store.
func (s *SecretService) Delete(ctx context.Context, key Key) error {
	_, err := s.run(ctx, nil, "secret-tool", "clear", "service", service, "key", string(key))
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && ee.ExitCode() == 1 {
			return nil
		}
		return fmt.Errorf("%w: secret-tool clear", ErrUnavailable)
	}
	return nil
}

// Backend implements Store.
func (s *SecretService) Backend() string { return "secret-service" }

// Keychain is the macOS Keychain store, through the security command.
//
// Limit: the security command takes the value as an argument, so it is visible
// to other processes of the same user for the moment the command runs. The
// value is hex encoded. Moving to the Security framework removes this; the
// file store is the alternative when that is not acceptable.
type Keychain struct{ run Runner }

// NewKeychain returns the store.
func NewKeychain(run Runner) *Keychain {
	if run == nil {
		run = ExecRunner
	}
	return &Keychain{run: run}
}

// Get implements Store.
func (k *Keychain) Get(ctx context.Context, key Key) ([]byte, error) {
	out, err := k.run(ctx, nil, "security", "find-generic-password", "-s", service, "-a", string(key), "-w")
	if err != nil {
		var ee *exec.ExitError
		// 44: the item could not be found.
		if errors.As(err, &ee) && ee.ExitCode() == 44 {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("%w: security find", ErrUnavailable)
	}
	b, err := hex.DecodeString(strings.TrimSpace(string(out)))
	if err != nil {
		return nil, fmt.Errorf("%w: unreadable value", ErrUnavailable)
	}
	return b, nil
}

// Put implements Store.
func (k *Keychain) Put(ctx context.Context, key Key, value []byte) error {
	_, err := k.run(ctx, nil, "security", "add-generic-password", "-U", "-s", service, "-a", string(key),
		"-w", hex.EncodeToString(value))
	if err != nil {
		return fmt.Errorf("%w: security add", ErrUnavailable)
	}
	return nil
}

// Delete implements Store.
func (k *Keychain) Delete(ctx context.Context, key Key) error {
	_, err := k.run(ctx, nil, "security", "delete-generic-password", "-s", service, "-a", string(key))
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && ee.ExitCode() == 44 {
			return nil
		}
		return fmt.Errorf("%w: security delete", ErrUnavailable)
	}
	return nil
}

// Backend implements Store.
func (k *Keychain) Backend() string { return "keychain" }

var (
	_ Store = (*SecretService)(nil)
	_ Store = (*Keychain)(nil)
)
