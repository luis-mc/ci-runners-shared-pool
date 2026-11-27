package core

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opencontainers/go-digest"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry"
)

// Layers lists the layers of the manifest manifestDigest in repository name
// (records R5.31). An index lists the layers of every manifest it holds, once
// each.
func (r *Registry) Layers(ctx context.Context, name, manifestDigest string) ([]registry.Layer, error) {
	d, err := digest.Parse(manifestDigest)
	if err != nil {
		return nil, fmt.Errorf("manifest digest %q: %w", manifestDigest, err)
	}
	repo, err := r.repo(ctx, name)
	if err != nil {
		return nil, err
	}
	ms, err := repo.Manifests(ctx)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []registry.Layer
	var walk func(d digest.Digest, depth int) error
	walk = func(d digest.Digest, depth int) error {
		m, err := ms.Get(ctx, d)
		if err != nil {
			return err
		}
		_, payload, err := m.Payload()
		if err != nil {
			return err
		}
		var doc struct {
			Layers []struct {
				MediaType string `json:"mediaType"`
				Digest    string `json:"digest"`
				Size      int64  `json:"size"`
			} `json:"layers"`
			Manifests []struct {
				Digest string `json:"digest"`
			} `json:"manifests"`
		}
		if err := json.Unmarshal(payload, &doc); err != nil {
			return err
		}
		for _, l := range doc.Layers {
			if !seen[l.Digest] {
				seen[l.Digest] = true
				out = append(out, registry.Layer{Digest: l.Digest, SizeBytes: l.Size, MediaType: l.MediaType})
			}
		}
		if depth < 2 {
			for _, c := range doc.Manifests {
				cd, err := digest.Parse(c.Digest)
				if err != nil {
					return err
				}
				if err := walk(cd, depth+1); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk(d, 0); err != nil {
		return nil, err
	}
	return out, nil
}
