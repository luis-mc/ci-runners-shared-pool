package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/lancache"
)

type fakeHub struct {
	up       bool
	trustErr error
	asked    string
}

func (f *fakeHub) Up(_ context.Context, base string) bool { f.asked = base; return f.up }

func (f *fakeHub) Trust(context.Context) (string, string, error) {
	return "/hub/controller-ca.pem", "/hub/combined.pem", f.trustErr
}

// Caches R5.76: the runner gets the npm and pip values only when the hub
// answers at start and its CA is written; else it stays on the real registries.
func TestPackageEnvOnlyWhenTheHubAnswers(t *testing.T) {
	grant := lancache.ProxyGrant{Repo: "o__r", Token: "tok"}
	spec := backend.RunnerSpec{PackageProxy: grant}
	h := &fakeHub{up: true}
	l := &link{cfg: Config{Controller: "ctl:8443"}, hub: h}
	env := l.packageEnv(context.Background(), spec)
	if h.asked != "https://ctl:8443" || !strings.Contains(env["npm_config_registry"], "/pkg/npm/o__r/tok/") || env["PIP_INDEX_URL"] == "" {
		t.Fatalf("asked %q env %v", h.asked, env)
	}
	h.up = false
	if env := l.packageEnv(context.Background(), spec); env != nil {
		t.Errorf("a down hub gave %v", env)
	}
	h.up, h.trustErr = true, errors.New("no CA")
	if env := l.packageEnv(context.Background(), spec); env != nil {
		t.Errorf("no CA gave %v", env)
	}
	h.trustErr = nil
	for name, s := range map[string]backend.RunnerSpec{
		"no grant":  {},
		"container": {PackageProxy: grant, Container: true},
	} {
		if env := l.packageEnv(context.Background(), s); env != nil {
			t.Errorf("%s gave %v", name, env)
		}
	}
}

func TestHubTrustWritesTheCAAndTheCombinedBundle(t *testing.T) {
	dir := t.TempDir()
	pub := filepath.Join(dir, "system.pem")
	if err := os.WriteFile(pub, []byte("SYSTEM"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "hub-ca")
	ca, bundle, err := writeHubTrust(out, []byte("CTLCA"), []string{filepath.Join(dir, "missing.pem"), pub})
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(ca); string(b) != "CTLCA" {
		t.Errorf("ca %q", b)
	}
	if b, _ := os.ReadFile(bundle); string(b) != "SYSTEM\nCTLCA" {
		t.Errorf("bundle %q", b)
	}
	if _, bundle, err := writeHubTrust(out, []byte("CTLCA"), nil); err != nil || bundle != "" {
		t.Errorf("no public bundle: %q %v", bundle, err)
	}
	if _, _, err := writeHubTrust(out, nil, nil); err == nil {
		t.Error("an empty CA was written")
	}
}
