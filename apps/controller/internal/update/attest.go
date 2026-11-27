package update

import (
	"context"
	_ "embed" // the Sigstore trusted root
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/addresses"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/sigstore/sigstore-go/pkg/bundle"
	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/verify"
)

// trustedRootJSON is the Sigstore public-good trusted root (Fulcio, Rekor,
// CT log, timestamp authority keys), copied from sigstore/root-signing. This
// repository is public, so its build provenance is signed by the public-good
// instance. Embedding the root keeps the verifier small (no TUF client) and
// offline-capable; a build carries the root of its day (operations R5.12).
//
//go:embed sigstoreroot/trusted_root.json
var trustedRootJSON []byte

// SignerIssuer is the OIDC issuer of GitHub Actions certificates.
const SignerIssuer = addresses.ActionsOIDCIssuer

// BundleSource finds the Sigstore bundles that attest a file by its SHA-256.
type BundleSource interface {
	Bundles(ctx context.Context, sha256hex string) ([]verify.SignedEntity, error)
}

// SigstoreAttester checks the build provenance attestation inside crsp: the
// bundle must verify against the Sigstore trusted root (certificate chain,
// transparency log, signed timestamp), its subject must be the file's SHA-256,
// and its signing certificate must name this repository's release workflow
// (operations R5.12). Any failure refuses the file (fail closed).
type SigstoreAttester struct {
	// Source fetches the bundles; nil uses the GitHub attestations API at APIBase.
	Source BundleSource
	// APIBase is the GitHub REST address of the default Source; empty takes addresses.DefaultGitHubAPIURL.
	APIBase string
	// Trusted is the trust material; nil uses the embedded public-good root.
	Trusted root.TrustedMaterial
	// SANRegex is the certificate identity (the workflow ref); empty means the release workflow of Repo.
	SANRegex string
	// noSCT drops the certificate-timestamp check; only tests set it, because the
	// virtual Sigstore of the tests issues certificates without one.
	noSCT bool
}

// signerSAN matches the release workflow at any ref of this repository.
func signerSAN() string {
	return addresses.ReleaseSignerSANPrefix + strings.ReplaceAll(SignerWorkflow[:len(SignerWorkflow)-len("/.github/workflows/release.yml")], ".", "\\.") +
		"/\\.github/workflows/release\\.yml@refs/.+$"
}

// Verify implements Attester.
func (s SigstoreAttester) Verify(ctx context.Context, file string) error {
	sum, err := FileSHA256(file)
	if err != nil {
		return &Failure{Check: "provenance", Reason: "read the download: " + err.Error()}
	}
	digest, err := hex.DecodeString(sum)
	if err != nil {
		return &Failure{Check: "provenance", Reason: err.Error()}
	}
	src := s.Source
	if src == nil {
		src = GitHubBundles{Base: s.APIBase}
	}
	ents, err := src.Bundles(ctx, sum)
	if err != nil {
		return &Failure{Check: "provenance", Reason: err.Error()}
	}
	if len(ents) == 0 {
		return &Failure{Check: "provenance", Reason: "GitHub has no build provenance attestation for this file"}
	}
	tm := s.Trusted
	if tm == nil {
		tr, err := root.NewTrustedRootFromJSON(trustedRootJSON)
		if err != nil {
			return &Failure{Check: "provenance", Reason: "the built-in Sigstore trust root is unreadable: " + err.Error()}
		}
		tm = tr
	}
	opts := []verify.VerifierOption{verify.WithTransparencyLog(1), verify.WithObserverTimestamps(1)}
	if !s.noSCT {
		opts = append(opts, verify.WithSignedCertificateTimestamps(1))
	}
	v, err := verify.NewVerifier(tm, opts...)
	if err != nil {
		return &Failure{Check: "provenance", Reason: err.Error()}
	}
	san := s.SANRegex
	if san == "" {
		san = signerSAN()
	}
	id, err := verify.NewShortCertificateIdentity(SignerIssuer, "", "", san)
	if err != nil {
		return &Failure{Check: "provenance", Reason: err.Error()}
	}
	pol := verify.NewPolicy(verify.WithArtifactDigest("sha256", digest), verify.WithCertificateIdentity(id))
	var errs []string
	for _, e := range ents {
		if _, err := v.Verify(e, pol); err != nil {
			errs = append(errs, err.Error())
			continue
		}
		return nil
	}
	msg := strings.Join(errs, "; ")
	if len(msg) > 300 {
		msg = msg[:300]
	}
	return &Failure{Check: "provenance", Reason: msg}
}

// GitHubBundles reads attestations from the GitHub API:
// GET /repos/<repo>/attestations/sha256:<digest>.
type GitHubBundles struct {
	Base   string
	Repo   string
	Client *http.Client
}

// Bundles implements BundleSource.
func (g GitHubBundles) Bundles(ctx context.Context, sum string) ([]verify.SignedEntity, error) {
	base, repo, c := g.Base, g.Repo, g.Client
	if base == "" {
		base = addresses.DefaultGitHubAPIURL
	}
	if repo == "" {
		repo = Repo
	}
	if c == nil {
		c = &http.Client{Timeout: 30 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+"/repos/"+repo+"/attestations/sha256:"+sum, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "crsp-updater")
	resp, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("could not reach GitHub for the attestation: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub answered %d for the attestation", resp.StatusCode)
	}
	var body struct {
		Attestations []struct {
			Bundle json.RawMessage `json:"bundle"`
		} `json:"attestations"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&body); err != nil {
		return nil, fmt.Errorf("GitHub sent an unreadable attestation: %w", err)
	}
	var out []verify.SignedEntity
	for _, a := range body.Attestations {
		var b bundle.Bundle
		if err := b.UnmarshalJSON(a.Bundle); err != nil {
			return nil, fmt.Errorf("unreadable attestation bundle: %w", err)
		}
		out = append(out, &b)
	}
	return out, nil
}
