package api

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api/gen"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/machines"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/packages"
)

// PackageServer gives the release packages (machines R5.65). *packages.Store implements it.
type PackageServer interface {
	Sums(ctx context.Context) ([]byte, string, error)
	Open(ctx context.Context, name string) (*os.File, int64, error)
}

var _ PackageServer = (*packages.Store)(nil)

// CreateJoinFile implements POST /api/v1/join-files (machines R5.65, CRSP122):
// one join code, one installer file for the OS. The audit entry is written by
// the service and never holds the code.
func (s *server) CreateJoinFile(ctx context.Context, r gen.CreateJoinFileRequestObject) (gen.CreateJoinFileResponseObject, error) {
	if s.machinesOff() {
		return machinesUnavailable[gen.CreateJoinFiledefaultJSONResponse](), nil
	}
	if r.Body == nil {
		return fail[gen.CreateJoinFiledefaultJSONResponse](s, machines.ErrInvalid), nil
	}
	var ttl time.Duration
	if r.Body.TtlMinutes != nil {
		ttl = time.Duration(*r.Body.TtlMinutes) * time.Minute
	}
	f, err := s.opts.Machines.CreateJoinFile(ctx, s.actor(ctx), string(r.Body.Os), ttl)
	if err != nil {
		return fail[gen.CreateJoinFiledefaultJSONResponse](s, err), nil
	}
	return gen.CreateJoinFile201ApplicationoctetStreamResponse{
		Body: bytes.NewReader(f.Content), ContentLength: int64(len(f.Content)),
		Headers: gen.CreateJoinFile201ResponseHeaders{ContentDisposition: fmt.Sprintf("attachment; filename=%q", f.Name), XJoinCodeId: f.JoinCode.ID},
	}, nil
}

// GetPackage implements GET /api/v1/packages/{name}: a release package or
// SHA256SUMS, public like the GitHub release it comes from.
func (s *server) GetPackage(ctx context.Context, r gen.GetPackageRequestObject) (gen.GetPackageResponseObject, error) {
	errAnswer := func(status int, code, msg string) gen.GetPackageResponseObject {
		return gen.GetPackagedefaultJSONResponse{StatusCode: status, Body: gen.ErrorEnvelope{Error: gen.ErrorBody{Code: code, Message: msg}}}
	}
	if s.opts.Packages == nil {
		return errAnswer(http.StatusNotImplemented, CodeNotImplemented, packages.ErrUnavailable.Error()), nil
	}
	if r.Name == "SHA256SUMS" {
		b, _, err := s.opts.Packages.Sums(ctx)
		if err != nil {
			return errAnswer(http.StatusNotImplemented, CodeNotImplemented, packages.ErrUnavailable.Error()), nil
		}
		return gen.GetPackage200ApplicationoctetStreamResponse{Body: bytes.NewReader(b), ContentLength: int64(len(b))}, nil
	}
	f, n, err := s.opts.Packages.Open(ctx, r.Name)
	switch {
	case err == nil:
		return gen.GetPackage200ApplicationoctetStreamResponse{Body: f, ContentLength: n}, nil
	case errors.Is(err, packages.ErrNotListed):
		return errAnswer(http.StatusNotFound, CodeNotFound, "no such package"), nil
	case errors.Is(err, packages.ErrUnavailable):
		return errAnswer(http.StatusNotImplemented, CodeNotImplemented, "the package is not available from this controller"), nil
	}
	s.opts.Logger.Error("package: open failed", "err", err)
	return errAnswer(http.StatusServiceUnavailable, CodeUnavailable, "the package could not be read"), nil
}
