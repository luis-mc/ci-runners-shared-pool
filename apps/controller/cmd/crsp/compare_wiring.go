package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/compare"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/github"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/repos"
)

// newCompareService reads job timings with the installation token of the
// repository's installation (observability R5.34). Only repositories crsp can
// see are compared.
func newCompareService(gh *github.REST, store repos.Store) (*compare.Service, error) {
	prices, err := compare.DefaultPrices()
	if err != nil {
		return nil, err
	}
	bearer := func(ctx context.Context, owner, repo string) (string, error) {
		full := owner + "/" + repo
		rs, err := store.ListRepositories(ctx, repos.ListFilter{Query: repo, Limit: 200})
		if err != nil {
			return "", err
		}
		for _, r := range rs {
			if strings.EqualFold(r.FullName, full) {
				return gh.InstallationToken(ctx, r.InstallationID)
			}
		}
		return "", fmt.Errorf("%w: %s", compare.ErrNotFound, full)
	}
	return &compare.Service{Source: &compare.API{Base: gh.APIBase(), Bearer: bearer}, Prices: prices}, nil
}
