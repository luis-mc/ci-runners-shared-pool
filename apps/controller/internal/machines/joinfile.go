package machines

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/joinfile"
)

// PackageSource gives the SHA256SUMS text and version of the release the
// controller was built from (machines R5.65).
type PackageSource interface {
	Sums(ctx context.Context) (sums []byte, version string, err error)
}

// JoinFile is a downloadable installer and the join code made for it. The code
// is inside Content only; the record in JoinCode never holds it.
type JoinFile struct {
	joinfile.File
	JoinCode JoinCode
}

// CreateJoinFile mints a join code (R5.4: works once, 15 minutes by default, an
// older unused code becomes invalid) and renders the installer of os with it
// (R5.65). The audit entry names who, the OS and the expiry, never the code. If
// the file cannot be made the code is withdrawn.
func (s *Service) CreateJoinFile(ctx context.Context, actor, osName string, ttl time.Duration) (JoinFile, error) {
	if _, ok := joinfile.Extension(osName); !ok {
		return JoinFile{}, fmt.Errorf("%w: os must be windows, linux or macos", ErrInvalid)
	}
	if s.o.Packages == nil || s.o.ConsoleURL == "" {
		return JoinFile{}, fmt.Errorf("%w: this controller has no release packages to put in an installer; copy the command instead", ErrNotSupported)
	}
	sums, version, err := s.o.Packages.Sums(ctx)
	if err != nil {
		return JoinFile{}, fmt.Errorf("%w: the release packages are not available (%s); copy the command instead", ErrNotSupported, firstLine(err.Error()))
	}
	created, err := s.CreateJoinCode(ctx, actor, ttl)
	if err != nil {
		return JoinFile{}, err
	}
	f, err := joinfile.Render(joinfile.Params{
		OS: osName, Controller: fmt.Sprintf("%s:%d", s.o.ControllerAddress, s.o.AgentPort), DownloadBase: strings.TrimRight(s.o.ConsoleURL, "/"),
		Fingerprint: s.o.Fingerprint, Code: created.Code, Version: version, Expires: created.JoinCode.ExpiresAt, Sums: sums,
	})
	if err != nil {
		if _, rerr := s.o.Repo.RevokeJoinCode(ctx, created.JoinCode.ID, s.now()); rerr != nil {
			err = errors.Join(err, rerr)
		}
		if errors.Is(err, joinfile.ErrInvalid) {
			return JoinFile{}, fmt.Errorf("%w: %v", ErrNotSupported, err)
		}
		return JoinFile{}, err
	}
	_ = s.o.Repo.Audit(ctx, actor, "join_file.create", created.JoinCode.ID, "os "+osName+", expires "+created.JoinCode.ExpiresAt.Format(time.RFC3339))
	return JoinFile{File: f, JoinCode: created.JoinCode}, nil
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
