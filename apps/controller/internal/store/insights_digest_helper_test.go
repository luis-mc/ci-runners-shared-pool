package store

import "github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/insights/digest"

func digestRow(w string) digest.Digest {
	return digest.Digest{Week: digest.Week(w), Path: "ci-data/digests/" + w + ".md", TopIDs: []int64{1}, WrittenAt: tBase}
}
