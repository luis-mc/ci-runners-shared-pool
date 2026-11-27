package profiles

import (
	"context"
	"sort"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry"
)

// View is one profile as the console shows it.
type View struct {
	Profile
	// Repositories are the repositories that chose this profile.
	Repositories []string
}

// ListViews returns every profile, standard first, with the repositories that
// chose each (the read side of the profile list, R5.77).
func ListViews(ctx context.Context, s Store) ([]View, error) {
	chosen, err := s.RepositoryProfiles(ctx)
	if err != nil {
		return nil, err
	}
	all, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	byProfile := map[string][]string{}
	for repo, id := range chosen {
		byProfile[id] = append(byProfile[id], repo)
	}
	out := make([]View, 0, len(all))
	for _, p := range all {
		repos := byProfile[p.ID]
		sort.Strings(repos)
		out = append(out, View{Profile: p, Repositories: repos})
	}
	sort.SliceStable(out, func(i, j int) bool {
		si, sj := out[i].Kind == registry.KindStandard, out[j].Kind == registry.KindStandard
		if si != sj {
			return si
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// OlderGeneration lists the local image tags the registry does not know
// (R5.80): keys made by runners.ImageKey have the same shape as the new key
// but no registry record, so they are read as an older generation and left to
// age out. isRegistryKey says whether a tag is a key the registry knows.
func OlderGeneration(localTags []string, isRegistryKey func(tag string) bool) []string {
	var out []string
	for _, t := range localTags {
		if !isRegistryKey(t) {
			out = append(out, t)
		}
	}
	return out
}
