package backend

import "time"

import "context"

// Found is a runner seen in the OS service list, made by CRSP or not (runners
// R5.5; port of dev-standards DS R5.38).
type Found struct {
	ServiceName string
	Name        string
	Folder      string
	Account     string
	Running     bool
	// Managed is true when the folder holds CRSP's own meta file.
	Managed bool
	// FolderUnreadable is true when the agent could not read the folder.
	FolderUnreadable bool
	Labels           []string
	RunnerVersion    string
	// SessionRefused is true when the listener's newest log says a session
	// for the runner already exists. SessionWaitUntil is the end of the wait
	// window while it is expected; zero with SessionRefused means the window
	// is over (runners R5.95).
	SessionRefused   bool
	SessionWaitUntil time.Time
}

// Finder lists runners from the OS service list, not only from folders.
type Finder interface {
	Find(ctx context.Context) ([]Found, error)
}

// Deregisterer removes a runner from GitHub with a remove token before its
// folder is touched (runners R5.19, DL315). A failure leaves the runner and
// folder as they were. The token is used once and dropped.
type Deregisterer interface {
	Deregister(ctx context.Context, h Handle, removeToken string) error
}

// Tool image attachment kinds, guest mount point and virtiofs tag (runners
// R5.80). The tool tree is always read-only in the guest.
const (
	ToolKindDisk     = "disk"
	ToolKindVirtiofs = "virtiofs"
	ToolGuestPath    = "/opt/crsp-tools"
	ToolShareTag     = "crsp-tools"
)

// ToolMount is one prebuilt tool tree to attach to a VM at boot.
type ToolMount struct {
	// Kind is ToolKindDisk or ToolKindVirtiofs.
	Kind string
	// HostPath is the disk file or the shared folder on the host.
	HostPath string
	// GuestPath is where the VM mounts it.
	GuestPath string
	// Tag is the virtiofs tag (a disk is found by its label).
	Tag string
	// Digest is the OCI digest the tree came from.
	Digest string
	// ReadOnly is always true; a backend refuses a mount that is not.
	ReadOnly bool
}

// ToolBooter is the VM backend hook: start the runner VM with tool trees
// attached at boot, so the job does not download them inside the VM. A
// backend that cannot attach does not implement it.
type ToolBooter interface {
	StartWithTools(ctx context.Context, r RunnerSpec, cfg StartConfig, tools []ToolMount) (Handle, error)
}

// ServiceHealth is one runner service after the backend re-asserted its logon
// right (runners R5.89).
type ServiceHealth struct {
	Runner  string
	Service string
	Account string
	Running bool
	// LogonKnown is false when the rights of the account could not be read;
	// LogonHeld is true when the account holds the right to log on as a
	// service after the pass.
	LogonKnown, LogonHeld bool
	// Regranted is true when this pass gave the right back.
	Regranted bool
	// Problem is why the service does not run, after the one retry. Empty
	// while it runs.
	Problem string
}

// ServiceHealer re-asserts the logon right of every runner service account of
// the machine and starts a stopped service (runners R5.89). It is idempotent.
type ServiceHealer interface {
	HealServices(ctx context.Context) ([]ServiceHealth, error)
}

// ListenerState is what the agent sees of a persistent runner's listener.
type ListenerState string

// The listener states.
const (
	ListenerOffline ListenerState = "offline"
	ListenerOnline  ListenerState = "online"
	ListenerBusy    ListenerState = "busy"
)

// ListenerControl starts, stops and reads the listener of a persistent runner
// that runs on the machine itself, by its service ref (runners R5.134). A stop
// waits for a clean exit and never kills; it is refused while a job is assigned.
// maintenance on a start is the one moment the runner may update itself.
type ListenerControl interface {
	StartListener(ctx context.Context, ref string, maintenance bool) error
	StopListener(ctx context.Context, ref string, jobAssigned bool) error
	ListenerState(ctx context.Context, ref string) (ListenerState, error)
}
