package machines_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/depstore"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/machines"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/machines/secretstest"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/tlsca"
)

// DEP15: the agent link serves a stored dependency to a machine that checks the pinned certificate, and a
// dependency that is not stored answers 404.
func TestLinkServesStoredDependencyOverPinnedTLS(t *testing.T) {
	ctx := context.Background()
	ca, err := tlsca.LoadOrCreate(ctx, tlsca.Options{Dir: t.TempDir(), Secrets: secretstest.New()})
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("installer bytes")
	sum := sha256.Sum256(body)
	st := &depstore.Store{
		Dir: filepath.Join(t.TempDir(), "deps"),
		Catalog: func(context.Context) []depstore.Spec {
			return []depstore.Spec{{ID: "docker-desktop-windows", Name: "Docker Desktop", SHA256: hex.EncodeToString(sum[:])}}
		},
	}
	svc := machines.New(machines.Options{Repo: machines.NewMemRepo(), ControllerAddress: "127.0.0.1", Fingerprint: ca.Fingerprint()})
	svc.SetDepFilesHandler(st.Handler())
	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := machines.NewLinkServer(ln.Addr().String(), ca, svc)
	go func() { _ = srv.Serve(tls.NewListener(ln, ca.ServerTLS())) }()
	t.Cleanup(func() { _ = srv.Close() })

	client := &http.Client{Transport: &http.Transport{ForceAttemptHTTP2: true, TLSClientConfig: tlsca.ClientTLS(ca.Fingerprint())}}
	get := func() (int, []byte) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+ln.Addr().String()+machines.DepFilesPath+"docker-desktop-windows", nil)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, b
	}
	if code, _ := get(); code != http.StatusNotFound {
		t.Fatalf("not stored yet: %d", code)
	}
	if err := st.Import(ctx, "docker-desktop-windows", bytes.NewReader(body), 0); err != nil {
		t.Fatal(err)
	}
	if code, b := get(); code != http.StatusOK || string(b) != string(body) {
		t.Fatalf("stored: %d %q", code, b)
	}
	// a client with another pin never gets the file
	other := &http.Client{Transport: &http.Transport{ForceAttemptHTTP2: true, TLSClientConfig: tlsca.ClientTLS("00" + ca.Fingerprint()[2:])}}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+ln.Addr().String()+machines.DepFilesPath+"docker-desktop-windows", nil)
	if resp, err := other.Do(req); err == nil {
		_ = resp.Body.Close()
		t.Fatal("a client with the wrong pin was served")
	}
}
