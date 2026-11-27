// Package leftovers finds runner services, folders, local accounts and
// downloads that no runner of this controller owns, and removes them (doctor
// R5.48, OPS33).
//
// An old test controller, or a failed install, can leave a Windows service
// actions.runner.*, its folder under the crsp runners folder, its local
// account and a download behind. The scan reads only. The removal runs only on
// items a fresh scan still finds, that this controller still does not own and
// that lie inside the crsp folders.
package leftovers

import (
	"context"
	"time"
)

// Kind says what a leftover is.
type Kind string

// Kinds.
const (
	KindService  Kind = "service"
	KindFolder   Kind = "folder"
	KindAccount  Kind = "account"
	KindDownload Kind = "download"
)

// AccountDescription is the description of every local runner account made by
// a crsp controller; with the crsp- name prefix it marks an account as a crsp
// runner account.
const AccountDescription = "CRSP runner account"

// ServicePrefix starts the name of every GitHub runner service.
const ServicePrefix = "actions.runner."

// Item is one leftover.
type Item struct {
	Kind Kind
	// Name is the service name, the account name, or the full path of a folder or file.
	Name string
	// Path is the folder or file path; for a service, the folder of its binary.
	Path      string
	SizeBytes int64
	// Account is the account a service runs as; for an account, its own name.
	Account string
	// Why says why it is not owned, in words for people.
	Why string
}

// ID is the stable key of an item inside one machine.
func (i Item) ID() string { return string(i.Kind) + ":" + i.Name }

// Ref names an item to remove.
type Ref struct {
	Kind Kind
	Name string
}

// ID is the key of the item the ref names.
func (r Ref) ID() string { return string(r.Kind) + ":" + r.Name }

// Owned is a runner this controller owns on a machine.
type Owned struct {
	Name string
	// ServiceName is the OS service name of the runner; empty when it has none.
	ServiceName string
}

// Scan is the result of one scan.
type Scan struct {
	Items []Item
	// Admin is true when the agent has the rights to remove services and accounts.
	Admin bool
	At    time.Time
}

// Outcome is what happened to one requested item.
type Outcome string

// Outcomes.
const (
	Removed     Outcome = "removed"
	AlreadyGone Outcome = "already_gone"
	Refused     Outcome = "refused"
	Failed      Outcome = "failed"
)

// Result is what happened to one requested item.
type Result struct {
	Kind    Kind
	Name    string
	Outcome Outcome
	// Error says why it was refused or failed; empty when removed.
	Error string
	// Steps is what was done, one line per step, in order.
	Steps []string
}

// Source scans and removes on a machine through its agent (the agent link, or
// the in-process agent of the controller's own machine). A scan or removal that
// could not run at all is an error; an item that failed is a Result.
type Source interface {
	Scan(ctx context.Context, machineID string, owned []Owned) (Scan, error)
	Remove(ctx context.Context, machineID string, owned []Owned, refs []Ref) ([]Result, error)
}
