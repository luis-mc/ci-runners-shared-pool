// Package fsdriver is Distribution's filesystem storage driver with one fix for
// Windows. Distribution v3.1.2 writes a small file (PutContent) to a temporary
// name, commits it, and renames it over the target while the temporary file is
// still open (the close is deferred to the end of the call). Windows refuses to
// rename an open file ("The process cannot access the file because it is being
// used by another process"), so every upload start failed on a Windows
// controller. This driver closes the temporary file before the rename. Other
// systems behave as before.
package fsdriver

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/distribution/distribution/v3/registry/storage/driver/factory"
	"github.com/distribution/distribution/v3/registry/storage/driver/filesystem"

	storagedriver "github.com/distribution/distribution/v3/registry/storage/driver"
)

// Name is the storage driver name to put in a Distribution configuration.
const Name = "crspfs"

func init() {
	factory.Register(Name, creator{})
}

type creator struct{}

func (creator) Create(_ context.Context, params map[string]any) (storagedriver.StorageDriver, error) {
	return New(params)
}

// Driver wraps the Distribution filesystem driver.
type Driver struct {
	storagedriver.StorageDriver
	root string
}

// New builds the driver from Distribution's filesystem parameters
// (rootdirectory, maxthreads).
func New(params map[string]any) (*Driver, error) {
	inner, err := filesystem.FromParameters(params)
	if err != nil {
		return nil, err
	}
	root := "/var/lib/registry"
	if v, ok := params["rootdirectory"]; ok {
		root = fmt.Sprint(v)
	}
	return &Driver{StorageDriver: inner, root: root}, nil
}

// Name implements storagedriver.StorageDriver.
func (d *Driver) Name() string { return Name }

// List returns the children of a path with slashes. The Distribution driver
// joins with filepath.Join, so on Windows it answers with backslashes, and the
// storage code that reads a child's name with path.Base then finds no tag, no
// manifest and no referrer.
func (d *Driver) List(ctx context.Context, subPath string) ([]string, error) {
	keys, err := d.StorageDriver.List(ctx, subPath)
	for i, k := range keys {
		keys[i] = filepath.ToSlash(k)
	}
	return keys, err
}

// Walk walks through this driver's List, so the paths it gives f use slashes.
func (d *Driver) Walk(ctx context.Context, path string, f storagedriver.WalkFn, options ...func(*storagedriver.WalkOptions)) error {
	return storagedriver.WalkFallback(ctx, d, path, f, options...)
}

// PutContent writes through a temporary file, closes it, then renames it over
// the target.
func (d *Driver) PutContent(ctx context.Context, subPath string, contents []byte) error {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return err
	}
	tmp := fmt.Sprintf("%s.%s.tmp", subPath, hex.EncodeToString(id[:]))
	w, err := d.Writer(ctx, tmp, false)
	if err != nil {
		return err
	}
	if _, err := io.Copy(w, bytes.NewReader(contents)); err != nil {
		cErr := w.Cancel(ctx)
		return errors.Join(err, cErr)
	}
	if err := w.Commit(ctx); err != nil {
		return errors.Join(err, w.Close())
	}
	if err := w.Close(); err != nil { // before the rename: Windows cannot move an open file
		return errors.Join(err, d.Delete(ctx, tmp))
	}
	if err := d.Move(ctx, tmp, subPath); err != nil {
		return errors.Join(err, d.Delete(ctx, tmp))
	}
	return syncDir(filepath.Dir(filepath.Join(d.root, filepath.FromSlash(subPath))))
}

// syncDir makes the rename durable. A directory cannot be opened for sync on
// Windows, so there it is skipped.
func syncDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("sync dir: %w", err)
	}
	defer func() { _ = f.Close() }()
	if err := f.Sync(); err != nil && filepath.Separator == '/' {
		return fmt.Errorf("sync dir: %w", err)
	}
	return nil
}
