//go:build windows

package secrets

import (
	"context"
	"errors"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Open returns a store whose files are protected with DPAPI (user scope), the
// per-user protection Windows Credential Manager itself is built on. The
// credential blob size limit of Credential Manager (2.5 KB) is too small for
// an App private key, so DPAPI-wrapped files are used.
func Open(_ context.Context, dataDir string) (Store, error) {
	f, err := NewFile(filepath.Join(dataDir, "secrets"))
	if err != nil {
		return nil, err
	}
	f.backend = "wincred"
	f.transform = dpapiProtect
	f.untransform = dpapiUnprotect
	return f, nil
}

func dpapiProtect(b []byte) ([]byte, error) {
	if len(b) == 0 {
		return nil, errors.New("empty secret")
	}
	in := windows.DataBlob{Size: uint32(len(b)), Data: &b[0]}
	var out windows.DataBlob
	if err := windows.CryptProtectData(&in, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return nil, err
	}
	defer func() { _, _ = windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data))) }()
	return append([]byte(nil), unsafe.Slice(out.Data, out.Size)...), nil
}

func dpapiUnprotect(b []byte) ([]byte, error) {
	if len(b) == 0 {
		return nil, errors.New("empty blob")
	}
	in := windows.DataBlob{Size: uint32(len(b)), Data: &b[0]}
	var out windows.DataBlob
	if err := windows.CryptUnprotectData(&in, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return nil, err
	}
	defer func() { _, _ = windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data))) }()
	return append([]byte(nil), unsafe.Slice(out.Data, out.Size)...), nil
}
