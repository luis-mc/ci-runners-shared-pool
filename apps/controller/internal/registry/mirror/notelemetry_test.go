package mirror

import (
	"reflect"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry"
)

// The mirror sends no telemetry, metrics, logs or event notifications (R5.81).
// Its pulls from the upstream (docker.io and others) are its purpose and are not
// telemetry. The configuration is built the way the wiring builds it.
func TestMirrorConfigSendsNothing(t *testing.T) {
	for _, up := range []string{"docker.io", "ghcr.io", "quay.io"} {
		cfg, err := ProxyConfig(registry.MirrorSettings{}, t.TempDir(), up)
		if err != nil {
			t.Fatalf("%s: %v", up, err)
		}
		if n := len(cfg.Notifications.Endpoints); n != 0 {
			t.Errorf("%s: %d notification endpoints", up, n)
		}
		if f := reflect.ValueOf(cfg).Elem().FieldByName("Reporting"); f.IsValid() && !f.IsZero() { // absent in Distribution v3.1.2
			t.Errorf("%s: reporting is set", up)
		}
		if h := cfg.Health; len(h.HTTPCheckers) != 0 || len(h.TCPCheckers) != 0 {
			t.Errorf("%s: health probes are set", up)
		}
		if len(cfg.Middleware) != 0 {
			t.Errorf("%s: middleware is set", up)
		}
		if cfg.Proxy.RemoteURL == "" {
			t.Errorf("%s: no upstream: the mirror lost its purpose", up)
		}
	}
}
