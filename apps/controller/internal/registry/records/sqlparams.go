package records

import (
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/store/sqlcgen"
)

func sqlcgenLayer(l registry.Layer, firstKind registry.Kind) sqlcgen.UpsertRegistryLayerParams {
	return sqlcgen.UpsertRegistryLayerParams{Digest: l.Digest, SizeBytes: l.SizeBytes, MediaType: l.MediaType, ChargedTo: string(firstKind)}
}

func sqlcgenLink(imageID, digest string, position int) sqlcgen.InsertRegistryImageLayerParams {
	return sqlcgen.InsertRegistryImageLayerParams{ImageID: imageID, LayerDigest: digest, Position: int64(position)}
}

func sqlcgenCharge(kind, digest string) sqlcgen.SetRegistryLayerChargeParams {
	return sqlcgen.SetRegistryLayerChargeParams{ChargedTo: kind, Digest: digest, ChargedTo_2: kind}
}

func sqlcgenSizes(id string, size, unique int64) sqlcgen.SetRegistryImageSizesParams {
	return sqlcgen.SetRegistryImageSizesParams{SizeBytes: size, UniqueBytes: unique, ID: id}
}
