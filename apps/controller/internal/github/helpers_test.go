package github

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"strings"
	"sync"
	"testing"
	"time"

	"context"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/github/fake"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/secrets"
)

var (
	keyOnce sync.Once
	testKey *rsa.PrivateKey
)

func sharedKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	keyOnce.Do(func() {
		k, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			panic(err)
		}
		testKey = k
	})
	return testKey
}

func pemOf(k *rsa.PrivateKey) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(k)})
}

type env struct {
	fake    *fake.Server
	client  *REST
	secrets *secrets.Memory
	now     time.Time
	mu      sync.Mutex
	sleeps  []time.Duration
}

func newEnv(t *testing.T) *env {
	t.Helper()
	k := sharedKey(t)
	f := fake.New()
	t.Cleanup(f.Close)
	f.AppPublicKey = &k.PublicKey
	f.PEM = string(pemOf(k))
	e := &env{fake: f, secrets: secrets.NewMemory(), now: time.Now()}
	f.Now = func() time.Time { return e.clock() }
	_ = e.secrets.Put(context.Background(), SecretKey(f.AppID, SecretPrivateKey), pemOf(k))
	_ = e.secrets.Put(context.Background(), SecretKey(f.AppID, SecretClientSecret), []byte(f.ClientSecret))
	e.client = New(Config{APIBase: f.URL, WebBase: f.URL, Secrets: e.secrets, Now: e.clock,
		Sleep: func(_ context.Context, d time.Duration) error {
			e.mu.Lock()
			e.sleeps = append(e.sleeps, d)
			e.mu.Unlock()
			return nil
		}})
	e.client.UseApp(f.AppID, "Iv23liFAKECLIENTID00")
	return e
}

func (e *env) clock() time.Time {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.now
}

func (e *env) advance(d time.Duration) {
	e.mu.Lock()
	e.now = e.now.Add(d)
	e.mu.Unlock()
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if strings.EqualFold(x, s) {
			return true
		}
	}
	return false
}
