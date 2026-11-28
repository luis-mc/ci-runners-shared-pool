package api

import (
	"context"
	"errors"
	"io"
	"net/http"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/agentinstallers"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api/gen"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/audit"
)

// MaxAgentInstallerBytes is the largest file an import takes (operations R5.132).
const MaxAgentInstallerBytes = 512 << 20

// AgentInstallerService lists and imports agent installers (operations R5.132, CRSP347).
// *agentinstallers.Store implements it.
type AgentInstallerService interface {
	State(ctx context.Context) (agentinstallers.State, error)
	Import(ctx context.Context, name string, r io.Reader) error
	ImportSums(r io.Reader) error
}

var _ AgentInstallerService = (*agentinstallers.Store)(nil)

func agentInstallersAnswer(st agentinstallers.State) gen.AgentInstallers {
	out := gen.AgentInstallers{Version: st.Version, HaveSums: st.HaveSums, FromRelease: st.FromRelease, Installers: []gen.AgentInstaller{}}
	for _, in := range st.Installers {
		out.Installers = append(out.Installers, gen.AgentInstaller{
			Name: in.Name, Os: gen.AgentInstallerOs(in.OS), Arch: gen.AgentInstallerArch(in.Arch),
			Format: gen.AgentInstallerFormat(in.Format), Sha256: in.SHA256, Stored: in.Cached,
		})
	}
	return out
}

func agentInstallerError(status int, code, msg string) gen.ImportAgentInstallerResponseObject {
	return gen.ImportAgentInstallerdefaultJSONResponse{StatusCode: status, Body: gen.ErrorEnvelope{Error: gen.ErrorBody{Code: code, Message: msg}}}
}

// ListAgentInstallers implements GET /api/v1/agent-installers.
func (s *server) ListAgentInstallers(ctx context.Context, _ gen.ListAgentInstallersRequestObject) (gen.ListAgentInstallersResponseObject, error) {
	if s.opts.AgentInstallers == nil {
		return gen.ListAgentInstallersdefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: notImplemented()}, nil
	}
	st, err := s.opts.AgentInstallers.State(ctx)
	if err != nil {
		s.opts.Logger.Error("agent installers: state failed", "err", err)
		return gen.ListAgentInstallersdefaultJSONResponse{StatusCode: http.StatusServiceUnavailable, Body: gen.ErrorEnvelope{Error: gen.ErrorBody{Code: CodeUnavailable, Message: "the agent installers could not be read"}}}, nil
	}
	return gen.ListAgentInstallers200JSONResponse(agentInstallersAnswer(st)), nil
}

// ImportAgentInstaller implements PUT /api/v1/agent-installers/{name}: SHA256SUMS or one installer.
func (s *server) ImportAgentInstaller(ctx context.Context, r gen.ImportAgentInstallerRequestObject) (gen.ImportAgentInstallerResponseObject, error) {
	svc := s.opts.AgentInstallers
	if svc == nil {
		return gen.ImportAgentInstallerdefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: notImplemented()}, nil
	}
	if r.Body == nil {
		return agentInstallerError(http.StatusBadRequest, CodeBadRequest, "send the file as the body"), nil
	}
	if r.Name != agentinstallers.SumsName {
		if _, ok := agentinstallers.ParseName(r.Name); !ok {
			return agentInstallerError(http.StatusNotFound, CodeNotFound, "that is not the name of an agent installer"), nil
		}
	}
	body := &limitedBody{r: r.Body, left: MaxAgentInstallerBytes}
	var err error
	if r.Name == agentinstallers.SumsName {
		err = svc.ImportSums(body)
	} else {
		err = svc.Import(ctx, r.Name, body)
	}
	switch {
	case err == nil:
	case body.over:
		return agentInstallerError(http.StatusRequestEntityTooLarge, CodeBadRequest, "the file is larger than 512 MB"), nil
	case errors.Is(err, agentinstallers.ErrMismatch):
		return agentInstallerError(http.StatusBadRequest, CodeBadRequest, "the file does not match the SHA-256 in SHA256SUMS; nothing was kept"), nil
	case errors.Is(err, agentinstallers.ErrNotListed):
		return agentInstallerError(http.StatusNotFound, CodeNotFound, "SHA256SUMS does not list that file for this version"), nil
	case errors.Is(err, agentinstallers.ErrSumsKnown):
		return agentInstallerError(http.StatusConflict, CodeConflict, "this controller already has the checksums of its release"), nil
	case errors.Is(err, agentinstallers.ErrUnavailable):
		return agentInstallerError(http.StatusConflict, CodeConflict, "import SHA256SUMS of this release first"), nil
	default:
		s.opts.Logger.Error("agent installers: import failed", "err", err)
		return agentInstallerError(http.StatusServiceUnavailable, CodeUnavailable, "the file could not be stored"), nil
	}
	if s.opts.Audit != nil {
		_ = s.opts.Audit.Record(ctx, audit.ActorFrom(ctx), "agent_installer.import", audit.Target{Type: "agent_installer", ID: r.Name}, nil)
	}
	st, err := svc.State(ctx)
	if err != nil {
		s.opts.Logger.Error("agent installers: state failed", "err", err)
		return agentInstallerError(http.StatusServiceUnavailable, CodeUnavailable, "the agent installers could not be read"), nil
	}
	return gen.ImportAgentInstaller200JSONResponse(agentInstallersAnswer(st)), nil
}

// limitedBody stops a read after left bytes and says it did, so an oversize file is refused, not cut and kept.
type limitedBody struct {
	r    io.Reader
	left int64
	over bool
}

func (l *limitedBody) Read(p []byte) (int, error) {
	if l.left <= 0 {
		var one [1]byte
		if n, _ := l.r.Read(one[:]); n > 0 {
			l.over = true
			return 0, errors.New("body too large")
		}
		return 0, io.EOF
	}
	if int64(len(p)) > l.left {
		p = p[:l.left]
	}
	n, err := l.r.Read(p)
	l.left -= int64(n)
	return n, err
}
