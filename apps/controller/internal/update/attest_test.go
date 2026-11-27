package update

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/testing/ca"
	"github.com/sigstore/sigstore-go/pkg/verify"
)

type fakeBundles struct {
	ents []verify.SignedEntity
	err  error
}

func (f fakeBundles) Bundles(context.Context, string) ([]verify.SignedEntity, error) {
	return f.ents, f.err
}

func writeAsset(t *testing.T, content string) (string, string) {
	t.Helper()
	f := filepath.Join(t.TempDir(), "crsp-linux-amd64.tar.gz")
	if err := os.WriteFile(f, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	sum, err := FileSHA256(f)
	if err != nil {
		t.Fatal(err)
	}
	return f, sum
}

func statementFor(sum string) []byte {
	b, _ := json.Marshal(map[string]any{
		"_type":         "https://in-toto.io/Statement/v1",
		"subject":       []map[string]any{{"name": "crsp", "digest": map[string]string{"sha256": sum}}},
		"predicateType": "https://slsa.dev/provenance/v1",
		"predicate":     map[string]any{},
	})
	return b
}

const goodSAN = "https://github.com/" + Repo + "/.github/workflows/release.yml@refs/tags/v1.5.0"

func TestSigstoreAttesterAcceptsReleaseWorkflowBundle(t *testing.T) {
	vs, err := ca.NewVirtualSigstore()
	if err != nil {
		t.Fatal(err)
	}
	file, sum := writeAsset(t, "the real build")
	ent, err := vs.Attest(goodSAN, SignerIssuer, statementFor(sum))
	if err != nil {
		t.Fatal(err)
	}
	a := SigstoreAttester{Source: fakeBundles{ents: []verify.SignedEntity{ent}}, Trusted: vs, noSCT: true}
	if err := a.Verify(context.Background(), file); err != nil {
		t.Fatalf("a valid attestation must pass: %v", err)
	}
}

func TestSigstoreAttesterFailsClosed(t *testing.T) {
	vs, err := ca.NewVirtualSigstore()
	if err != nil {
		t.Fatal(err)
	}
	other, err := ca.NewVirtualSigstore() // a different trust root
	if err != nil {
		t.Fatal(err)
	}
	file, sum := writeAsset(t, "the real build")
	good, _ := vs.Attest(goodSAN, SignerIssuer, statementFor(sum))
	wrongWorkflow, _ := vs.Attest("https://github.com/"+Repo+"/.github/workflows/other.yml@refs/heads/main", SignerIssuer, statementFor(sum))
	wrongRepo, _ := vs.Attest("https://github.com/someone/else/.github/workflows/release.yml@refs/tags/v1", SignerIssuer, statementFor(sum))
	wrongIssuer, _ := vs.Attest(goodSAN, "https://accounts.example.com", statementFor(sum))
	wrongFile, _ := vs.Attest(goodSAN, SignerIssuer, statementFor(strings.Repeat("ab", 32)))
	foreign, _ := other.Attest(goodSAN, SignerIssuer, statementFor(sum))

	cases := map[string]SigstoreAttester{
		"no attestation":      {Source: fakeBundles{}, Trusted: vs, noSCT: true},
		"source error":        {Source: fakeBundles{err: os.ErrDeadlineExceeded}, Trusted: vs, noSCT: true},
		"other workflow":      {Source: fakeBundles{ents: []verify.SignedEntity{wrongWorkflow}}, Trusted: vs, noSCT: true},
		"other repository":    {Source: fakeBundles{ents: []verify.SignedEntity{wrongRepo}}, Trusted: vs, noSCT: true},
		"other issuer":        {Source: fakeBundles{ents: []verify.SignedEntity{wrongIssuer}}, Trusted: vs, noSCT: true},
		"other file":          {Source: fakeBundles{ents: []verify.SignedEntity{wrongFile}}, Trusted: vs, noSCT: true},
		"untrusted signature": {Source: fakeBundles{ents: []verify.SignedEntity{foreign}}, Trusted: vs, noSCT: true},
	}
	for name, a := range cases {
		err := a.Verify(context.Background(), file)
		f, ok := err.(*Failure)
		if !ok || f.Check != "provenance" {
			t.Errorf("%s: want a provenance failure, got %v", name, err)
		}
	}
	// One good bundle among bad ones is enough.
	a := SigstoreAttester{Source: fakeBundles{ents: []verify.SignedEntity{wrongWorkflow, good}}, Trusted: vs, noSCT: true}
	if err := a.Verify(context.Background(), file); err != nil {
		t.Fatalf("a good bundle among bad ones must pass: %v", err)
	}
}

func TestEmbeddedTrustRootParses(t *testing.T) {
	tr, err := root.NewTrustedRootFromJSON(trustedRootJSON)
	if err != nil {
		t.Fatal(err)
	}
	if len(tr.FulcioCertificateAuthorities()) == 0 || len(tr.RekorLogs()) == 0 {
		t.Fatal("the embedded trust root must hold Fulcio and Rekor keys")
	}
}

func TestGitHubBundlesAsksByDigestAndFailsClosed(t *testing.T) {
	var path string
	status := http.StatusOK
	body := `{"attestations":[]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	g := GitHubBundles{Base: srv.URL}
	ents, err := g.Bundles(context.Background(), "abc123")
	if err != nil || len(ents) != 0 {
		t.Fatalf("%v %v", ents, err)
	}
	if path != "/repos/"+Repo+"/attestations/sha256:abc123" {
		t.Fatalf("path %s", path)
	}
	status = http.StatusNotFound
	if ents, err := g.Bundles(context.Background(), "x"); err != nil || len(ents) != 0 {
		t.Fatalf("404 means no attestation: %v %v", ents, err)
	}
	status, body = http.StatusInternalServerError, ""
	if _, err := g.Bundles(context.Background(), "x"); err == nil {
		t.Fatal("a 500 must be an error")
	}
	status, body = http.StatusOK, `{"attestations":[{"bundle":{"mediaType":"nope"}}]}`
	if _, err := g.Bundles(context.Background(), "x"); err == nil {
		t.Fatal("a garbage bundle must be an error")
	}
	// Through the attester: an unreadable bundle is a provenance failure, never a pass.
	file, _ := writeAsset(t, "x")
	err = SigstoreAttester{Source: g}.Verify(context.Background(), file)
	if f, ok := err.(*Failure); !ok || f.Check != "provenance" {
		t.Fatalf("want provenance failure, got %v", err)
	}
}
