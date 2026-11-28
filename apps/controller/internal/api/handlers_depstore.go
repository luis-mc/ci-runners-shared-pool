package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api/gen"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/depstore"
)

// DepStoreService is what the dependency store routes need (*depstore.Service).
type DepStoreService interface {
	List(ctx context.Context) []depstore.Entry
	Get(ctx context.Context, id string) (depstore.Entry, error)
	Fetch(ctx context.Context, actor, id string) error
	Import(ctx context.Context, actor, id string, r io.Reader, limit int64) error
}

var _ DepStoreService = (*depstore.Service)(nil)

// depImportMax is the largest file an import takes (Docker Desktop is about 600 MB).
const depImportMax = 2 << 30

func storeEntryToAPI(e depstore.Entry) gen.DependencyStoreEntry {
	out := gen.DependencyStoreEntry{
		Id: e.ID, Name: e.Name, Kind: gen.DependencyStoreEntryKind(e.Kind), State: gen.DependencyStoreEntryState(e.State),
		Pinned: e.SHA256 != "", CanFetch: e.URL != "" || e.Kind == depstore.KindAgent && e.SHA256 != "",
	}
	set := func(v string) *string {
		if v == "" {
			return nil
		}
		return &v
	}
	out.Os, out.Arch, out.Version, out.Source = set(e.OS), set(e.Arch), set(e.Version), set(e.Source)
	out.Sha256, out.Error = set(e.SHA256), set(e.Error)
	if e.Origin != "" {
		o := gen.DependencyStoreEntryOrigin(e.Origin)
		out.Origin = &o
	}
	if e.Stored > 0 {
		out.StoredBytes = &e.Stored
	}
	if !e.FetchedAt.IsZero() {
		t := e.FetchedAt
		out.FetchedAt = &t
	}
	return out
}

// depStoreErr maps a store error to the error envelope; the reason is in plain words.
func depStoreErr(s *server, err error) (int, gen.ErrorEnvelope) {
	env := func(code, msg string) gen.ErrorEnvelope {
		return gen.ErrorEnvelope{Error: gen.ErrorBody{Code: code, Message: msg}}
	}
	switch {
	case errors.Is(err, depstore.ErrUnknown):
		return http.StatusNotFound, env(CodeNotFound, "no such dependency")
	case errors.Is(err, depstore.ErrMismatch):
		return http.StatusBadRequest, env(CodeBadRequest, err.Error())
	case errors.Is(err, depstore.ErrNotPinned), errors.Is(err, depstore.ErrNoSource), errors.Is(err, depstore.ErrNotAllowed):
		return http.StatusConflict, env(CodeConflict, err.Error())
	case errors.Is(err, os.ErrNotExist):
		return http.StatusNotFound, env(CodeNotFound, "no such dependency")
	}
	if strings.Contains(err.Error(), "failed") || strings.Contains(err.Error(), "larger than") || strings.Contains(err.Error(), "SHA256SUMS") || strings.Contains(err.Error(), "no agent installers") {
		return http.StatusConflict, env(CodeConflict, err.Error())
	}
	s.opts.Logger.Error("dependency store failed", "err", err)
	return http.StatusServiceUnavailable, env(CodeUnavailable, "the controller could not do that: "+err.Error())
}

func depStoreFail[T ~struct {
	Body       gen.ErrorEnvelope
	StatusCode int
}](s *server, err error) T {
	st, env := depStoreErr(s, err)
	return T(errShape{Body: env, StatusCode: st})
}

// ListDependencyStore implements GET /api/v1/dependencies/store.
func (s *server) ListDependencyStore(ctx context.Context, _ gen.ListDependencyStoreRequestObject) (gen.ListDependencyStoreResponseObject, error) {
	if s.opts.DepStore == nil {
		return machinesUnavailable[gen.ListDependencyStoredefaultJSONResponse](), nil
	}
	items := []gen.DependencyStoreEntry{}
	for _, e := range s.opts.DepStore.List(ctx) {
		items = append(items, storeEntryToAPI(e))
	}
	return gen.ListDependencyStore200JSONResponse{Items: items}, nil
}

// FetchDependency implements POST /api/v1/dependencies/store/{dependencyId}/fetch.
func (s *server) FetchDependency(ctx context.Context, req gen.FetchDependencyRequestObject) (gen.FetchDependencyResponseObject, error) {
	if s.opts.DepStore == nil {
		return machinesUnavailable[gen.FetchDependencydefaultJSONResponse](), nil
	}
	if err := s.opts.DepStore.Fetch(ctx, s.actor(ctx), req.DependencyId); err != nil {
		return depStoreFail[gen.FetchDependencydefaultJSONResponse](s, err), nil
	}
	e, err := s.opts.DepStore.Get(ctx, req.DependencyId)
	if err != nil {
		return depStoreFail[gen.FetchDependencydefaultJSONResponse](s, err), nil
	}
	return gen.FetchDependency200JSONResponse(storeEntryToAPI(e)), nil
}

// ImportDependency implements PUT /api/v1/dependencies/store/{dependencyId}/file.
func (s *server) ImportDependency(ctx context.Context, req gen.ImportDependencyRequestObject) (gen.ImportDependencyResponseObject, error) {
	if s.opts.DepStore == nil {
		return machinesUnavailable[gen.ImportDependencydefaultJSONResponse](), nil
	}
	if req.Body == nil {
		return fail[gen.ImportDependencydefaultJSONResponse](s, errors.New("no file")), nil
	}
	if err := s.opts.DepStore.Import(ctx, s.actor(ctx), req.DependencyId, req.Body, depImportMax); err != nil {
		return depStoreFail[gen.ImportDependencydefaultJSONResponse](s, err), nil
	}
	e, err := s.opts.DepStore.Get(ctx, req.DependencyId)
	if err != nil {
		return depStoreFail[gen.ImportDependencydefaultJSONResponse](s, err), nil
	}
	return gen.ImportDependency200JSONResponse(storeEntryToAPI(e)), nil
}
