package keys

import "github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry"

// SimilarShare is the share of bytes two images must have in common to be
// called similar (R5.28).
const SimilarShare = 0.60

// SharedShare returns the bytes the two layer lists share over the bytes of
// the larger image, 0 when either is empty. Layers are matched by digest.
func SharedShare(a, b []registry.Layer) float64 {
	sum := func(ls []registry.Layer) (int64, map[string]int64) {
		var t int64
		m := make(map[string]int64, len(ls))
		for _, l := range ls {
			if _, dup := m[l.Digest]; !dup {
				m[l.Digest] = l.SizeBytes
				t += l.SizeBytes
			}
		}
		return t, m
	}
	ta, ma := sum(a)
	tb, mb := sum(b)
	big := max(ta, tb)
	if big == 0 {
		return 0
	}
	var shared int64
	for d, n := range ma {
		if _, ok := mb[d]; ok {
			shared += n
		}
	}
	return float64(shared) / float64(big)
}

// Similar reports whether b is similar to a: same profile and arch, different
// image, and over 60% shared bytes. It is information for the image page and
// is never a reason to offer a replace (R5.28).
func Similar(a, b registry.Image, la, lb []registry.Layer) bool {
	if a.ID == b.ID || a.Profile != b.Profile || a.Arch != b.Arch {
		return false
	}
	return SharedShare(la, lb) > SimilarShare
}
