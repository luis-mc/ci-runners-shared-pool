package profiles

import (
	"context"
)

// Store keeps profiles and the profile each repository chose.
type Store interface {
	Get(ctx context.Context, id string) (Profile, error)
	List(ctx context.Context) ([]Profile, error)
	Put(ctx context.Context, p Profile) error
	// SeedIfAbsent inserts p only when no profile has its id, so a seed
	// never overwrites an edit.
	SeedIfAbsent(ctx context.Context, p Profile) error
	Delete(ctx context.Context, id string) error
	SetRepositoryProfile(ctx context.Context, repository, profileID string) error
	RepositoryProfile(ctx context.Context, repository string) (string, error) // "" when none
	ClearRepositoryProfile(ctx context.Context, repository string) error
	RepositoryProfiles(ctx context.Context) (map[string]string, error)
}

// Seed inserts the default standard profiles that are missing.
func Seed(ctx context.Context, s Store) error {
	for _, p := range Defaults() {
		if err := s.SeedIfAbsent(ctx, p); err != nil {
			return err
		}
	}
	return nil
}
