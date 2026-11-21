//go:build windows

package toolstore

import (
	"context"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// DefaultCloner clones a tree file by file: an ReFS block clone when the volume is ReFS, else an NTFS hard
// link, else a copy. Folders are always new.
func DefaultCloner() Cloner { return winCloner{} }

type winCloner struct{}

// Clone implements Cloner.
func (winCloner) Clone(ctx context.Context, src, dst string) error {
	return walkClone(ctx, src, dst, func(from, to string) error {
		if err := blockClone(from, to); err == nil {
			return nil
		}
		_ = os.Remove(to)
		return os.Link(from, to)
	})
}

// fsctlDuplicateExtentsToFile is FSCTL_DUPLICATE_EXTENTS_TO_FILE.
const fsctlDuplicateExtentsToFile = 0x00098344

type duplicateExtents struct {
	FileHandle       windows.Handle
	SourceFileOffset int64
	TargetFileOffset int64
	ByteCount        int64
}

// blockClone makes `to` share the extents of `from` on ReFS. Sizes are rounded up to 64 KiB, a multiple of
// every ReFS cluster size, as the call requires. Any error means "not here": the caller links instead.
func blockClone(from, to string) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	st, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(to, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()
	size := st.Size()
	if size == 0 {
		return nil
	}
	if err := out.Truncate(size); err != nil {
		return err
	}
	const unit = 64 << 10
	rounded := (size + unit - 1) / unit * unit
	req := duplicateExtents{FileHandle: windows.Handle(in.Fd()), ByteCount: rounded}
	var n uint32
	return windows.DeviceIoControl(windows.Handle(out.Fd()), fsctlDuplicateExtentsToFile,
		(*byte)(unsafe.Pointer(&req)), uint32(unsafe.Sizeof(req)), nil, 0, &n, nil)
}
