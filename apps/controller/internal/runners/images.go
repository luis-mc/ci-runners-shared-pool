package runners

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
	"time"
)

// DefaultKeepImages is how many images per OS a machine keeps (runners R5.33;
// a setting, open question 3).
const DefaultKeepImages = 3

// ImageKeyInput is what makes one image: its base, its layers and the pinned
// versions (runners R5.33).
type ImageKeyInput struct {
	Base   string
	Layers []string
	// Pins maps a tool name to its pinned version, such as the Actions runner.
	Pins map[string]string
	// Extras are the pool's extra distribution packages (R5.51). No names
	// leave the key as it was, so existing images stay valid.
	Extras []string
}

// ImageKey is the key of an image. The same base, the same layers (in any
// order, repeats ignored) and the same pins give the same key, so a machine
// builds each key once and reuses it. Any change gives another key.
func ImageKey(in ImageKeyInput) string {
	layers := NormalizeLayers(in.Layers)
	pins := make([]string, 0, len(in.Pins))
	for k, v := range in.Pins {
		pins = append(pins, k+"="+v)
	}
	sort.Strings(pins)
	parts := []string{"base:" + in.Base, "layers:" + strings.Join(layers, ","), "pins:" + strings.Join(pins, ",")}
	if extras := NormalizeLayers(in.Extras); len(extras) > 0 {
		parts = append(parts, "extras:"+strings.Join(extras, ","))
	}
	h := sha256.New()
	for _, part := range parts {
		h.Write([]byte(part))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:12]
}

// NormalizeLayers sorts layer names and drops repeats and blanks.
func NormalizeLayers(layers []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, l := range layers {
		l = strings.TrimSpace(l)
		if l != "" && !seen[l] {
			seen[l] = true
			out = append(out, l)
		}
	}
	sort.Strings(out)
	return out
}

// Image is one built image on a machine (runners R5.32: the layer list is
// shown with the size).
type Image struct {
	Key       string
	Tag       string
	OS        string
	Layers    []string
	SizeMB    int64
	CreatedAt time.Time
}

// SelectPrune says which images to delete: for each OS the newest keep images
// stay, and an older one goes only when no runner uses it (runners R5.33). An
// image that runners use is never in the answer, even when it is old.
func SelectPrune(images []Image, inUse map[string]bool, keep int) []Image {
	if keep < 1 {
		keep = DefaultKeepImages
	}
	byOS := map[string][]Image{}
	for _, im := range images {
		byOS[im.OS] = append(byOS[im.OS], im)
	}
	var del []Image
	for _, list := range byOS {
		sort.SliceStable(list, func(i, j int) bool { return list[i].CreatedAt.After(list[j].CreatedAt) })
		for i, im := range list {
			if i >= keep && !inUse[im.Key] {
				del = append(del, im)
			}
		}
	}
	sort.Slice(del, func(i, j int) bool { return del[i].Key < del[j].Key })
	return del
}
