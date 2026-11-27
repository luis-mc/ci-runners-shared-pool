package main

import (
	"context"
	"errors"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/compare"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/github"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/repos"
)

func TestCompareServiceRefusesRepositoryCrspCannotSee(t *testing.T) {
	svc, err := newCompareService(github.New(github.Config{}), repos.NewMemStore())
	if err != nil {
		t.Fatal(err)
	}
	api, ok := svc.Source.(*compare.API)
	if !ok {
		t.Fatalf("source is %T", svc.Source)
	}
	if _, err := api.Bearer(context.Background(), "o", "r"); !errors.Is(err, compare.ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}
