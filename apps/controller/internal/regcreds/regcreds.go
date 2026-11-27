// Package regcreds holds private registry logins per backend and a private CA
// bundle that runners trust (runners R5.77, R5.78).
//
// A login's password lives only in the OS secret store; metadata keeps a
// CredentialRef, the name of that stored entry. The password reaches a
// container engine or VM only on standard input or in a one-time boot file,
// never in a command line, a log line or a job environment (R6.2).
// The CA bundle is public data. Where it is trusted depends on the backend:
// container: a mounted folder plus update-ca-certificates; VM: files injected
// at boot; persistent: the runner's own environment only, never the host
// trust store.
package regcreds

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/redact"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/secrets"
)

// ErrNotFound is returned for a login that is not stored.
var ErrNotFound = errors.New("registry login not found")

// ErrInvalid wraps every refusal of an input.
var ErrInvalid = errors.New("invalid registry setting")

var hostRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]*[a-z0-9])?(:[0-9]{1,5})?$`)

// maxBundle bounds a CA bundle.
const maxBundle = 256 << 10

// Backend kinds a login or a CA bundle is injected into.
const (
	KindContainer  = "container"
	KindVM         = "vm"
	KindPersistent = "persistent"
)

// RegistryLogin is the stored description of a login. It carries no password.
type RegistryLogin struct {
	Kind          string `json:"kind"` // KindContainer, KindVM or KindPersistent
	Host          string `json:"host"` // registry host, with an optional port
	Username      string `json:"username"`
	CredentialRef string `json:"credentialRef"` // the secret store entry of the password
}

// Meta persists login metadata and the CA bundle.
type Meta interface {
	Load(ctx context.Context, name string) ([]byte, error) // nil, nil when absent
	Save(ctx context.Context, name string, data []byte) error
	Remove(ctx context.Context, name string) error
	Names(ctx context.Context, prefix string) ([]string, error)
}

// MemoryMeta is an in-process Meta for tests and the first wiring.
type MemoryMeta struct {
	mu sync.Mutex
	m  map[string][]byte
}

// Load implements Meta.
func (m *MemoryMeta) Load(_ context.Context, name string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]byte(nil), m.m[name]...), nil
}

// Save implements Meta.
func (m *MemoryMeta) Save(_ context.Context, name string, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.m == nil {
		m.m = map[string][]byte{}
	}
	m.m[name] = append([]byte(nil), data...)
	return nil
}

// Remove implements Meta.
func (m *MemoryMeta) Remove(_ context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.m, name)
	return nil
}

// Names implements Meta.
func (m *MemoryMeta) Names(_ context.Context, prefix string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []string
	for k := range m.m {
		if strings.HasPrefix(k, prefix) {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out, nil
}

// Store keeps logins and the CA bundle.
type Store struct {
	Meta     Meta
	Secrets  secrets.Store
	Redactor *redact.Redactor
}

func loginName(kind, host string) string { return "login/" + kind + "/" + host }

func credentialRef(kind, host string) string { return "crsp/registry/" + kind + "/" + host }

// ValidateLogin checks a login.
func ValidateLogin(l RegistryLogin) error {
	switch l.Kind {
	case KindContainer, KindVM, KindPersistent:
	default:
		return fmt.Errorf("%w: backend kind %q", ErrInvalid, l.Kind)
	}
	if !hostRe.MatchString(l.Host) {
		return fmt.Errorf("%w: registry host %q", ErrInvalid, l.Host)
	}
	if l.Username == "" || strings.ContainsAny(l.Username, "\x00\r\n:") {
		return fmt.Errorf("%w: user name", ErrInvalid)
	}
	return nil
}

// PutLogin stores a login. An empty password keeps the stored one.
func (s *Store) PutLogin(ctx context.Context, l RegistryLogin, password string) (RegistryLogin, error) {
	if err := ValidateLogin(l); err != nil {
		return RegistryLogin{}, err
	}
	if strings.ContainsAny(password, "\x00\r\n") {
		return RegistryLogin{}, fmt.Errorf("%w: the password holds a line break or NUL", ErrInvalid)
	}
	l.CredentialRef = credentialRef(l.Kind, l.Host)
	if password != "" {
		if err := s.Secrets.Put(ctx, secrets.Key(l.CredentialRef), []byte(password)); err != nil {
			return RegistryLogin{}, fmt.Errorf("store the credential: %w", err)
		}
		if s.Redactor != nil {
			s.Redactor.Add(password)
		}
	} else if _, err := s.Secrets.Get(ctx, secrets.Key(l.CredentialRef)); err != nil {
		return RegistryLogin{}, fmt.Errorf("%w: a new login needs a password", ErrInvalid)
	}
	data, err := json.Marshal(l)
	if err != nil {
		return RegistryLogin{}, err
	}
	if err := s.Meta.Save(ctx, loginName(l.Kind, l.Host), data); err != nil {
		return RegistryLogin{}, err
	}
	return l, nil
}

// Logins lists the stored logins of a kind ("" lists all), without passwords.
func (s *Store) Logins(ctx context.Context, kind string) ([]RegistryLogin, error) {
	prefix := "login/"
	if kind != "" {
		prefix += kind + "/"
	}
	names, err := s.Meta.Names(ctx, prefix)
	if err != nil {
		return nil, err
	}
	var out []RegistryLogin
	for _, n := range names {
		data, err := s.Meta.Load(ctx, n)
		if err != nil {
			return nil, err
		}
		var l RegistryLogin
		if err := json.Unmarshal(data, &l); err != nil {
			return nil, fmt.Errorf("read login %s: %w", n, err)
		}
		out = append(out, l)
	}
	return out, nil
}

// DeleteLogin removes a login and its stored password.
func (s *Store) DeleteLogin(ctx context.Context, kind, host string) error {
	data, err := s.Meta.Load(ctx, loginName(kind, host))
	if err != nil {
		return err
	}
	if len(data) == 0 {
		return ErrNotFound
	}
	if err := s.Secrets.Delete(ctx, secrets.Key(credentialRef(kind, host))); err != nil {
		return err
	}
	return s.Meta.Remove(ctx, loginName(kind, host))
}

// Secret is a password read for one use. Its String and GoString methods
// never show the value, so a stray %v or %+v does not leak it.
type Secret struct{ v string }

// Reveal returns the value. Call it only to write standard input.
func (s Secret) Reveal() string { return s.v }

// String implements fmt.Stringer.
func (Secret) String() string { return redact.Mask }

// GoString implements fmt.GoStringer.
func (Secret) GoString() string { return redact.Mask }

// Password reads the stored password of a login for one use.
func (s *Store) Password(ctx context.Context, l RegistryLogin) (Secret, error) {
	b, err := s.Secrets.Get(ctx, secrets.Key(l.CredentialRef))
	if err != nil {
		return Secret{}, fmt.Errorf("read the credential of %s: %w", l.Host, err)
	}
	if s.Redactor != nil {
		s.Redactor.Add(string(b))
	}
	return Secret{v: string(b)}, nil
}

const caName = "ca-bundle"

// ValidateBundle checks that the PEM holds only certificates, at least one,
// and no private key. It returns the cleaned PEM.
func ValidateBundle(pemData []byte) ([]byte, error) {
	if len(pemData) == 0 || len(pemData) > maxBundle {
		return nil, fmt.Errorf("%w: the CA bundle is empty or over %d KiB", ErrInvalid, maxBundle>>10)
	}
	var out []byte
	n := 0
	rest := pemData
	for {
		var b *pem.Block
		b, rest = pem.Decode(rest)
		if b == nil {
			break
		}
		if b.Type != "CERTIFICATE" {
			return nil, fmt.Errorf("%w: the CA bundle holds a %q block; only certificates are allowed", ErrInvalid, b.Type)
		}
		if _, err := x509.ParseCertificate(b.Bytes); err != nil {
			return nil, fmt.Errorf("%w: certificate %d: %v", ErrInvalid, n+1, err)
		}
		out = append(out, pem.EncodeToMemory(b)...)
		n++
	}
	if n == 0 || len(strings.TrimSpace(string(rest))) != 0 {
		return nil, fmt.Errorf("%w: the CA bundle is not a list of PEM certificates", ErrInvalid)
	}
	return out, nil
}

// PutBundle stores the private CA bundle.
func (s *Store) PutBundle(ctx context.Context, pemData []byte) error {
	clean, err := ValidateBundle(pemData)
	if err != nil {
		return err
	}
	return s.Meta.Save(ctx, caName, clean)
}

// Bundle returns the stored CA bundle, or nil when none is set.
func (s *Store) Bundle(ctx context.Context) ([]byte, error) { return s.Meta.Load(ctx, caName) }

// DeleteBundle removes the CA bundle.
func (s *Store) DeleteBundle(ctx context.Context) error { return s.Meta.Remove(ctx, caName) }
