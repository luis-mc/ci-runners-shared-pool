package leftovers

import (
	"context"
	"errors"
	"time"
)

// ErrNotExist is what an OS layer returns (wrapped) when the service, account
// or path to act on is not there. Remove then reports the item as already gone.
var ErrNotExist = errors.New("does not exist")

// OSService is one runner service as the OS lists it.
type OSService struct {
	// Name is the exact service name (an 84-character name is normal).
	Name string
	// Binary is the path of the service program (or the unit's working directory).
	Binary string
	// Account is the account the service runs as.
	Account string
	Running bool
}

// Dir is a subdirectory of the runners folder.
type Dir struct {
	Name    string
	Path    string
	ModTime time.Time
}

// File is a file directly inside the runners folder.
type File struct {
	Name    string
	Path    string
	Size    int64
	ModTime time.Time
}

// OSAccount is a local account.
type OSAccount struct {
	Name        string
	Description string
}

// OS is the operating-system layer of the scan and the removal. The real one
// is NewHostOS; tests give a fake. Scan calls only the read methods.
type OS interface {
	// GOOS is "windows", "linux" or "darwin"; it picks the path rules.
	GOOS() string
	// Root is the crsp runners folder.
	Root() string
	// Admin reports whether the agent can remove services and accounts.
	Admin() bool

	// Services lists every service whose name starts with ServicePrefix, by the exact name.
	Services(ctx context.Context) ([]OSService, error)
	Dirs(ctx context.Context, root string) ([]Dir, error)
	Files(ctx context.Context, root string) ([]File, error)
	DirSize(ctx context.Context, path string) (int64, error)
	Accounts(ctx context.Context) ([]OSAccount, error)
	// Resolve gives the path with every link resolved; an error that wraps
	// ErrNotExist (or fs.ErrNotExist) when the path is not there.
	Resolve(path string) (string, error)

	StopService(ctx context.Context, name string) error
	DeleteService(ctx context.Context, name string) error
	RemoveAll(path string) error
	DeleteAccount(ctx context.Context, name string) error
}
