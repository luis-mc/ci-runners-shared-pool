package scan

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const (
	ociManifestType = "application/vnd.oci.image.manifest.v1+json"
	ociEmptyType    = "application/vnd.oci.empty.v1+json"
	maxReferrerSize = 256 << 20
)

// RegistryClient stores referrers in the embedded registry over its /v2/ API
// (OCI distribution spec 1.1: blob upload, then a manifest with a subject).
type RegistryClient struct {
	Base  string // https://controller:port, no trailing slash
	HTTP  *http.Client
	Token func(ctx context.Context) (string, error) // optional bearer token
}

var _ Referrers = (*RegistryClient)(nil)

type descriptor struct {
	MediaType string `json:"mediaType"`
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
}

// Put uploads body as a blob and links it to the subject with an artifact
// manifest. It returns the digest of that manifest.
func (c *RegistryClient) Put(ctx context.Context, name, subjectDigest, artifactType string, body io.Reader) (string, error) {
	data, err := io.ReadAll(io.LimitReader(body, maxReferrerSize+1))
	if err != nil {
		return "", fmt.Errorf("read %s: %w", artifactType, err)
	}
	if len(data) > maxReferrerSize {
		return "", fmt.Errorf("%s is larger than %d bytes", artifactType, maxReferrerSize)
	}
	empty := []byte("{}")
	cfg, err := c.putBlob(ctx, name, empty, ociEmptyType)
	if err != nil {
		return "", err
	}
	layer, err := c.putBlob(ctx, name, data, artifactType)
	if err != nil {
		return "", err
	}
	// The subject's size is not known here; the registry only needs the digest
	// to index the referrer, and the descriptor needs a media type.
	subject := descriptor{MediaType: ociManifestType, Digest: subjectDigest}
	m, err := json.Marshal(map[string]any{
		"schemaVersion": 2,
		"mediaType":     ociManifestType,
		"artifactType":  artifactType,
		"config":        cfg,
		"layers":        []descriptor{layer},
		"subject":       subject,
	})
	if err != nil {
		return "", err
	}
	dig := digestOf(m)
	req, err := c.request(ctx, http.MethodPut, c.Base+"/v2/"+name+"/manifests/"+dig, bytes.NewReader(m))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", ociManifestType)
	if err := c.do(req, http.StatusCreated, http.StatusOK); err != nil {
		return "", fmt.Errorf("put referrer manifest: %w", err)
	}
	return dig, nil
}

func (c *RegistryClient) putBlob(ctx context.Context, name string, data []byte, mediaType string) (descriptor, error) {
	dig := digestOf(data)
	d := descriptor{MediaType: mediaType, Digest: dig, Size: int64(len(data))}
	start, err := c.request(ctx, http.MethodPost, c.Base+"/v2/"+name+"/blobs/uploads/", nil)
	if err != nil {
		return d, err
	}
	resp, err := c.client().Do(start)
	if err != nil {
		return d, fmt.Errorf("start blob upload: %w", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		return d, fmt.Errorf("start blob upload: status %d", resp.StatusCode)
	}
	loc, err := start.URL.Parse(resp.Header.Get("Location"))
	if err != nil || resp.Header.Get("Location") == "" {
		return d, fmt.Errorf("start blob upload: bad Location %q", resp.Header.Get("Location"))
	}
	q := loc.Query()
	q.Set("digest", dig)
	loc.RawQuery = q.Encode()
	put, err := c.request(ctx, http.MethodPut, loc.String(), bytes.NewReader(data))
	if err != nil {
		return d, err
	}
	put.Header.Set("Content-Type", "application/octet-stream")
	if err := c.do(put, http.StatusCreated); err != nil {
		return d, fmt.Errorf("put blob: %w", err)
	}
	return d, nil
}

func (c *RegistryClient) request(ctx context.Context, method, u string, body io.Reader) (*http.Request, error) {
	if _, err := url.Parse(u); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, err
	}
	if c.Token != nil {
		tok, err := c.Token(ctx)
		if err != nil {
			return nil, fmt.Errorf("registry token: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	return req, nil
}

func (c *RegistryClient) do(req *http.Request, ok ...int) error {
	resp, err := c.client().Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	for _, s := range ok {
		if resp.StatusCode == s {
			return nil
		}
	}
	msg, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
	return fmt.Errorf("status %d %s", resp.StatusCode, strings.TrimSpace(string(msg)))
}

func (c *RegistryClient) client() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

func digestOf(b []byte) string {
	h := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(h[:])
}
