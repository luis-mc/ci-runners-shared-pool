// Package poolname is the name of the pool: the runner label every crsp
// runner carries and crsp writes into each repository's CRSP_RUNS_ON_*
// variables (runners R5.36, github-integration R5.28). It stores the name,
// validates it, and renames the pool in ordered, stoppable steps.
package poolname

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/auth"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/repos"
)

// Default is the name a pool has until the owner chooses another.
const Default = "crsp-home"

// EnvName is the environment value that overrides the stored name when set.
// It is kept for a service that already sets it; the console does not need it.
const EnvName = "CRSP_POOL_NAME"

// MaxLen is the longest pool name. GitHub allows long labels; a short one keeps
// the 100-label cap and the variables readable.
const MaxLen = 40

// Source says where the name in force came from.
const (
	SourceDefault = "default"
	SourceStored  = "stored"
	SourceEnv     = "env"
)

// Validate checks a pool name: a GitHub runner label (no commas, spaces or
// capitals, so it reads the same everywhere) that the generic-label rule of
// repos accepts, which is crsp- and words joined by hyphens.
func Validate(name string) error {
	if name == "" {
		return auth.Invalid("Give the pool a name.")
	}
	if len(name) > MaxLen {
		return auth.Invalid("A pool name has at most %d characters.", MaxLen)
	}
	if name != strings.ToLower(name) || strings.ContainsAny(name, " ,\t\r\n/\\") {
		return auth.Invalid("A pool name uses small letters, digits and hyphens only, like crsp-home.")
	}
	if !repos.IsGenericLabel(name) || !strings.HasPrefix(name, "crsp-") {
		return auth.Invalid("A pool name starts with crsp- and then small letters, digits and hyphens, like crsp-home.")
	}
	return nil
}

// Name is the pool name in force, read by every part of the controller on each
// use so a rename needs no restart. During a rename it also keeps the old name
// as an alias until the old label is gone, so a job queued with the old label
// still finds a runner.
type Name struct {
	mu     sync.RWMutex
	cur    string
	alias  string
	source string
}

// NewName starts a Name at name from source.
func NewName(name, source string) *Name {
	return &Name{cur: name, source: source}
}

// Get is the pool name in force.
func (n *Name) Get() string {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.cur
}

// Accepted is the labels that mean this pool: the name, then the old name while
// a rename is unfinished.
func (n *Name) Accepted() []string {
	n.mu.RLock()
	defer n.mu.RUnlock()
	if n.alias != "" && n.alias != n.cur {
		return []string{n.cur, n.alias}
	}
	return []string{n.cur}
}

// Source is where the name came from.
func (n *Name) Source() string {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.source
}

func (n *Name) set(cur, alias string) {
	n.mu.Lock()
	n.cur, n.alias = cur, alias
	if n.source == SourceDefault {
		n.source = SourceStored
	}
	n.mu.Unlock()
}

// KV is the small key and value store the name and the job live in (the
// settings table).
type KV interface {
	RawSetting(ctx context.Context, key string) (string, bool, error)
	PutRawSetting(ctx context.Context, key, jsonValue, by string) error
}

const (
	nameKey = "pool_name"
	jobKey  = "pool_rename_job"
)

// Resolve reads the name in force at start: the environment value when it is
// set and valid, else the stored name, else Default. It never fails the
// start: a bad environment value is ignored with its reason returned.
func Resolve(ctx context.Context, kv KV) (n *Name, ignored string) {
	if env := strings.TrimSpace(os.Getenv(EnvName)); env != "" {
		if err := Validate(env); err == nil {
			return NewName(env, SourceEnv), ""
		}
		ignored = fmt.Sprintf("%s=%q is not a pool name and is ignored", EnvName, env)
	}
	if kv != nil {
		if raw, ok, err := kv.RawSetting(ctx, nameKey); err == nil && ok {
			var v string
			if json.Unmarshal([]byte(raw), &v) == nil && Validate(v) == nil {
				return NewName(v, SourceStored), ignored
			}
		}
	}
	return NewName(Default, SourceDefault), ignored
}
