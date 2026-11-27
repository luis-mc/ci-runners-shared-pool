package toolimage

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend/sys"
)

// ErrNoToolMounts is returned when the VM backend cannot attach tool images;
// the caller then lets the job download its tools as before.
var ErrNoToolMounts = errors.New("this backend cannot attach tool images at boot")

// Kinds of attachment.
const (
	// KindDisk is a read-only disk image (squashfs) the VM sees as a block device.
	KindDisk = backend.ToolKindDisk
	// KindVirtiofs is a read-only virtiofs share of the unpacked folder.
	KindVirtiofs = backend.ToolKindVirtiofs
)

// DiskExt is the file extension of a packed disk.
const DiskExt = ".sqsh"

// Packer turns an unpacked tree into a read-only disk image.
type Packer interface {
	Pack(ctx context.Context, srcDir, outFile string) error
}

// SquashPacker packs with mksquashfs: owned by root, no extended attributes,
// no append to an old image.
type SquashPacker struct{ Cmd sys.Commands }

// Pack implements Packer.
func (s SquashPacker) Pack(ctx context.Context, srcDir, outFile string) error {
	res, err := s.Cmd.Run(ctx, sys.Command{Name: "mksquashfs",
		Args: []string{srcDir, outFile, "-noappend", "-all-root", "-no-xattrs", "-quiet"}})
	if err != nil {
		return fmt.Errorf("pack tool disk: %w", err)
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("pack tool disk: mksquashfs exit %d: %s", res.ExitCode, res.Stderr)
	}
	return nil
}

// Deps are the seams of Prepare.
type Deps struct {
	Pull   Puller
	FS     TreeFS
	Packer Packer
}

// Prepare makes the read-only attachment of one tool image under root. Both
// outputs are named by the digest, so they never change: a second job with the
// same digest reuses them. Kind disk keeps only the packed file (the
// unpacked tree is deleted); kind virtiofs keeps the tree.
func Prepare(ctx context.Context, d Deps, root string, ref Ref, kind string) (backend.ToolMount, error) {
	tree := filepath.Join(root, ref.Hex())
	m := backend.ToolMount{Kind: kind, Digest: ref.Digest, GuestPath: backend.ToolGuestPath, Tag: backend.ToolShareTag, ReadOnly: true}
	switch kind {
	case KindVirtiofs:
		m.HostPath = tree
	case KindDisk:
		if d.Packer == nil {
			return backend.ToolMount{}, errors.New("no disk packer configured")
		}
		m.HostPath = tree + DiskExt
	default:
		return backend.ToolMount{}, fmt.Errorf("unknown tool attachment kind %q", kind)
	}
	if d.FS.Exists(m.HostPath) {
		return m, nil
	}
	if err := d.FS.MkdirAll(root, 0o755); err != nil {
		return backend.ToolMount{}, err
	}
	if err := Unpack(ctx, d.Pull, d.FS, ref, tree); err != nil {
		return backend.ToolMount{}, err
	}
	if kind == KindVirtiofs {
		return m, nil
	}
	part := m.HostPath + ".part"
	_ = d.FS.RemoveAll(part)
	if err := d.Packer.Pack(ctx, tree, part); err != nil {
		_ = d.FS.RemoveAll(part)
		_ = d.FS.RemoveAll(tree)
		return backend.ToolMount{}, err
	}
	if err := d.FS.Rename(part, m.HostPath); err != nil {
		return backend.ToolMount{}, err
	}
	if err := d.FS.RemoveAll(tree); err != nil {
		return backend.ToolMount{}, err
	}
	return m, nil
}

// Start prepares the tool image and starts the runner with it attached at
// boot. A backend without ToolBooter gets ErrNoToolMounts and nothing is
// pulled.
func Start(ctx context.Context, b backend.Backend, d Deps, root, refText, kind string, r backend.RunnerSpec, cfg backend.StartConfig) (backend.Handle, error) {
	tb, ok := b.(backend.ToolBooter)
	if !ok {
		return backend.Handle{}, ErrNoToolMounts
	}
	ref, err := ParseRef(refText)
	if err != nil {
		return backend.Handle{}, err
	}
	m, err := Prepare(ctx, d, root, ref, kind)
	if err != nil {
		return backend.Handle{}, err
	}
	return tb.StartWithTools(ctx, r, cfg, []backend.ToolMount{m})
}
