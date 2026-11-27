package core

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"go.opentelemetry.io/otel"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry"
)

// The registry sends no telemetry, metrics, logs or event notifications to any
// host (R5.81). These tests fail when that stops being true: a source scan for
// the code that would switch telemetry on, the core configuration, and a
// runtime check with every OpenTelemetry exporter switched on by environment.

// forbiddenImport says why an import path may not appear in a non-test file.
func forbiddenImport(path string) string {
	switch {
	case path == "github.com/distribution/distribution/v3/tracing" || strings.HasPrefix(path, "github.com/distribution/distribution/v3/tracing/"):
		return "Distribution's tracing package starts an OpenTelemetry exporter"
	case path == "github.com/distribution/distribution/v3/registry":
		return "the Distribution server package (registry.NewRegistry) starts tracing and notifications; mount handlers.NewApp instead"
	case path == "go.opentelemetry.io/contrib/exporters/autoexport" || strings.HasPrefix(path, "go.opentelemetry.io/contrib/exporters/autoexport/"):
		return "autoexport reads OTEL_* variables and exports to the host they name"
	case strings.HasPrefix(path, "go.opentelemetry.io/otel/exporters/"):
		return "an OpenTelemetry exporter sends data to another host"
	}
	return ""
}

// forbiddenCall says why a selector of an imported package may not be used.
func forbiddenCall(path, name string) bool {
	switch path {
	case "go.opentelemetry.io/otel":
		return name == "SetTracerProvider" || name == "SetMeterProvider"
	case "go.opentelemetry.io/otel/log/global":
		return name == "SetLoggerProvider"
	}
	return false
}

// scanSource returns what is wrong in one Go source.
func scanSource(name string, src any) ([]string, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, name, src, parser.ImportsOnly|parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	var bad []string
	for _, im := range f.Imports {
		p, _ := strconv.Unquote(im.Path.Value)
		if why := forbiddenImport(p); why != "" {
			bad = append(bad, fmt.Sprintf("%s: imports %s: %s", fset.Position(im.Pos()), p, why))
		}
	}
	f, err = parser.ParseFile(fset, name, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	local := map[string]string{} // local name -> import path
	for _, im := range f.Imports {
		p, _ := strconv.Unquote(im.Path.Value)
		n := filepath.Base(p)
		if im.Name != nil {
			n = im.Name.Name
		}
		local[n] = p
	}
	ast.Inspect(f, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if id, ok := sel.X.(*ast.Ident); ok && forbiddenCall(local[id.Name], sel.Sel.Name) {
			bad = append(bad, fmt.Sprintf("%s: uses %s.%s: it installs a telemetry provider", fset.Position(sel.Pos()), id.Name, sel.Sel.Name))
		}
		return true
	})
	return bad, nil
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for hops := 0; hops < 256; hops++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		up := filepath.Dir(dir)
		if up == dir {
			break
		}
		dir = up
	}
	t.Fatal("no go.mod above the test folder")
	return ""
}

func TestScannerSeesTelemetry(t *testing.T) {
	cases := map[string]string{
		"tracing":    `package x; import _ "github.com/distribution/distribution/v3/tracing"`,
		"server":     `package x; import "github.com/distribution/distribution/v3/registry"; var _ = registry.NewRegistry`,
		"autoexport": `package x; import _ "go.opentelemetry.io/contrib/exporters/autoexport"`,
		"exporter":   `package x; import _ "go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"`,
		"tracer":     `package x; import "go.opentelemetry.io/otel"; func f() { otel.SetTracerProvider(nil) }`,
		"meter":      `package x; import o "go.opentelemetry.io/otel"; func f() { o.SetMeterProvider(nil) }`,
		"logger":     `package x; import "go.opentelemetry.io/otel/log/global"; func f() { global.SetLoggerProvider(nil) }`,
	}
	for name, src := range cases {
		bad, err := scanSource(name+".go", src)
		if err != nil || len(bad) == 0 {
			t.Errorf("%s: not caught (%v, %v)", name, bad, err)
		}
	}
	ok := `package x; import ("github.com/distribution/distribution/v3/registry/handlers"; "go.opentelemetry.io/otel"); var _ = handlers.NewApp; var _ = otel.GetTracerProvider`
	if bad, err := scanSource("ok.go", ok); err != nil || len(bad) != 0 {
		t.Errorf("handlers and a read of the provider are fine: %v, %v", bad, err)
	}
}

func TestNoTelemetryInSource(t *testing.T) {
	root := moduleRoot(t)
	var files int
	var bad []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch n := d.Name(); {
			case path == root:
			case n == "node_modules" || n == "vendor" || n == "testdata" || strings.HasPrefix(n, ".") || strings.HasPrefix(n, "_"):
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		files++
		found, err := scanSource(path, nil)
		if err != nil {
			return err
		}
		for _, b := range found {
			bad = append(bad, strings.TrimPrefix(b, root+string(filepath.Separator)))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if files < 50 {
		t.Fatalf("scanned only %d files: the walk is broken", files)
	}
	sort.Strings(bad)
	for _, b := range bad {
		t.Error(b)
	}
}

// assertNoOutbound fails when a Distribution configuration would send anything
// to a host: no notification endpoint, no reporting, no health probe, no Redis.
// An endpoint on a loopback or private host is allowed (R5.2).
func assertNoOutbound(t *testing.T, name string, cfg any) {
	t.Helper()
	v := reflect.Indirect(reflect.ValueOf(cfg))
	if f := v.FieldByName("Reporting"); f.IsValid() && !f.IsZero() { // v3.1.2 has no such field (bugsnag, newrelic are gone)
		t.Errorf("%s: Reporting is set: %v", name, f.Interface())
	}
	if f := v.FieldByName("Notifications").FieldByName("Endpoints"); f.Len() != 0 {
		for i := range f.Len() {
			u := f.Index(i).FieldByName("URL").String()
			if !privateURL(u) {
				t.Errorf("%s: notification endpoint %q is not on a private or loopback host", name, u)
			}
		}
	}
	for _, p := range []string{"Health.HTTPCheckers", "Health.TCPCheckers"} {
		parts := strings.Split(p, ".")
		if f := v.FieldByName(parts[0]).FieldByName(parts[1]); f.Len() != 0 {
			t.Errorf("%s: %s is set", name, p)
		}
	}
	if f := v.FieldByName("Middleware"); f.Len() != 0 {
		t.Errorf("%s: middleware is set (a storage or registry middleware may call out)", name)
	}
}

func privateURL(raw string) bool {
	host := strings.TrimPrefix(strings.TrimPrefix(raw, "https://"), "http://")
	host = strings.SplitN(host, "/", 2)[0]
	return PeerAllowed(host)
}

func TestCoreConfigSendsNothing(t *testing.T) {
	cfg := newConfig(t.TempDir(), "secret")
	assertNoOutbound(t, "core", cfg)
	if len(cfg.Notifications.Endpoints) != 0 {
		t.Fatalf("core notification endpoints: %v", cfg.Notifications.Endpoints)
	}
	// the check itself must catch a public endpoint and accept a LAN one
	if !privateURL("http://192.168.1.5:9000/hook") || !privateURL("http://127.0.0.1/x") || privateURL("https://hooks.example.com/x") {
		t.Fatal("privateURL does not follow the private LAN rule")
	}
}

// TestNoTelemetryAtRuntime starts the core with every OpenTelemetry exporter
// asked for by environment, round-trips a push and a pull, and sees that the
// collector got nothing and no provider was installed.
func TestNoTelemetryAtRuntime(t *testing.T) {
	var hits atomic.Int64
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(collector.Close)
	for k, v := range map[string]string{
		"OTEL_TRACES_EXPORTER":        "otlp",
		"OTEL_METRICS_EXPORTER":       "otlp",
		"OTEL_LOGS_EXPORTER":          "otlp",
		"OTEL_EXPORTER_OTLP_ENDPOINT": collector.URL,
		"OTEL_EXPORTER_OTLP_PROTOCOL": "http/protobuf",
	} {
		t.Setenv(k, v)
	}
	tracerBefore, meterBefore := otel.GetTracerProvider(), otel.GetMeterProvider()

	h := newHarness(t)
	name := registry.IncomingName("m1", registry.StdName("base"))
	tok := h.token("m1", push(name)...)
	im := h.pushImage(tok, name, "", testInput("base"), nil)
	resp, _ := h.do(tok, http.MethodGet, "/v2/"+name+"/manifests/"+im.digest, nil, "Accept", "application/vnd.oci.image.manifest.v1+json")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("pull: %d", resp.StatusCode)
	}

	// No wait: an installed SDK provider shows in its type below; the collector count is checked as of now.
	if n := hits.Load(); n != 0 {
		t.Fatalf("the collector got %d requests", n)
	}
	if got := otel.GetTracerProvider(); got != tracerBefore || strings.Contains(fmt.Sprintf("%T", got), "sdk") {
		t.Fatalf("the tracer provider changed: %T", got)
	}
	if got := otel.GetMeterProvider(); got != meterBefore || strings.Contains(fmt.Sprintf("%T", got), "sdk") {
		t.Fatalf("the meter provider changed: %T", got)
	}
}
