package runners

import (
	"context"
	"sync"
)

// MirrorPuller pulls an image reference. Fakes stand in for the engine in
// tests; a real one runs `docker pull` (runners R5.34).
type MirrorPuller func(ctx context.Context, ref string) error

// MirrorPull pulls images through a pool's registry mirror and falls back to
// the upstream registry when the mirror cannot be reached (runners R5.34).
type MirrorPull struct {
	// Mirror is the mirror host, such as "mirror.lan:5000". Empty means the
	// pool has no mirror and every pull goes upstream, with no notice.
	Mirror string
	// Pull pulls one full reference.
	Pull MirrorPuller
	// Notice is told once, the first time a pull falls back.
	Notice func(msg string)

	once sync.Once
}

// Ref is the reference to pull through the mirror: the mirror host in front of
// the upstream reference.
func (m *MirrorPull) Ref(upstream string) string {
	if m.Mirror == "" {
		return upstream
	}
	return m.Mirror + "/" + upstream
}

// Do pulls upstream through the mirror. If the mirror fails, it pulls from
// the upstream registry and says so once. The error of the upstream pull is
// the result then: the mirror is a speed-up, not a dependency.
func (m *MirrorPull) Do(ctx context.Context, upstream string) error {
	if m.Mirror == "" {
		return m.Pull(ctx, upstream)
	}
	if err := m.Pull(ctx, m.Ref(upstream)); err == nil {
		return nil
	} else if ctx.Err() != nil {
		return ctx.Err()
	}
	m.once.Do(func() {
		if m.Notice != nil {
			m.Notice("registry mirror " + m.Mirror + " cannot be reached; pulling from the upstream registry")
		}
	})
	return m.Pull(ctx, upstream)
}
