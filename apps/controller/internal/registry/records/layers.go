package records

import (
	"context"
	"fmt"
	"sort"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry"
)

// PutLayers records the layers of an image once each with their size (R5.31) and in order, then recounts. A layer
// another image already holds is not recorded again; it only gets one more holder. It is safe to call twice.
func (s *Service) PutLayers(ctx context.Context, imageID string, layers []registry.Layer) error {
	img, err := s.Image(ctx, imageID)
	if err != nil {
		return err
	}
	for i, l := range layers {
		if l.Digest == "" || l.SizeBytes < 0 {
			return fmt.Errorf("%w: layer %d has no digest or a negative size", errBadRecord, i)
		}
		if err := s.q.UpsertRegistryLayer(ctx, sqlcgenLayer(l, img.Kind)); err != nil {
			return err
		}
		if err := s.q.InsertRegistryImageLayer(ctx, sqlcgenLink(imageID, l.Digest, i)); err != nil {
			return err
		}
	}
	return s.Recount(ctx, imageID)
}

// Recount redoes the accounting around one image: for each of its layers the kind it is charged to (the kind of the
// oldest image that holds it, deleted images aside), then the size and unique bytes of every image that holds one of
// them. REG6 calls it after it marks an image deleted, so the layers the others share move to the right kind.
func (s *Service) Recount(ctx context.Context, imageID string) error {
	holders, err := s.q.ListRegistryLayerHolders(ctx, imageID)
	if err != nil {
		return err
	}
	type holder struct{ id, kind, at string }
	byLayer := map[string][]holder{}
	affected := map[string]bool{imageID: true}
	for _, h := range holders {
		at := h.PromotedAt
		if at == "" {
			at = h.PushedAt
		}
		byLayer[h.LayerDigest] = append(byLayer[h.LayerDigest], holder{h.ImageID, h.Kind, at})
		affected[h.ImageID] = true
	}
	for digest, hs := range byLayer {
		// Oldest first; an image with no time yet is the newest. The id breaks a tie so the answer is stable.
		sort.Slice(hs, func(i, j int) bool {
			a, b := hs[i], hs[j]
			if (a.at == "") != (b.at == "") {
				return b.at == ""
			}
			if a.at != b.at {
				return a.at < b.at
			}
			return a.id < b.id
		})
		if err := s.q.SetRegistryLayerCharge(ctx, sqlcgenCharge(hs[0].kind, digest)); err != nil {
			return err
		}
	}
	ids := make([]string, 0, len(affected))
	for id := range affected {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		use, err := s.q.ListRegistryLayerUseOfImage(ctx, id)
		if err != nil {
			return err
		}
		var size, unique int64
		for _, u := range use {
			size += u.SizeBytes
			if u.LiveHolders <= 1 {
				unique += u.SizeBytes
			}
		}
		if err := s.q.SetRegistryImageSizes(ctx, sqlcgenSizes(id, size, unique)); err != nil {
			return err
		}
	}
	return nil
}

// PromotedHook is the REG4 hook of the promotion (interfaces.go Promoter): it reads the layers of the new image
// from the registry storage and accounts them. A failure is logged by the promoter and never undoes the promotion.
func (s *Service) PromotedHook(src LayerSource) registry.PromotedHook {
	return func(ctx context.Context, img registry.Image) error {
		layers, err := src.Layers(ctx, img.Name, img.Manifest)
		if err != nil {
			return fmt.Errorf("read the layers of %s: %w", img.ID, err)
		}
		return s.PutLayers(ctx, img.ID, layers)
	}
}
