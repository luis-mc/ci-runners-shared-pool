package core

import (
	"errors"
	"fmt"
	"strings"

	"github.com/distribution/reference"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry"
)

// The two actions a token scope can hold. A machine never gets "delete": the
// controller deletes in process (Registry.DeleteManifest).
const (
	ActionPull = "pull"
	ActionPush = "push"
)

// Prefixes of the two promoted roots (R5.4).
const (
	stdPrefix    = registry.NamePrefix + "std/"
	customPrefix = registry.NamePrefix + "custom/"
)

// ErrBadName is a repository name that is not a valid OCI name or does not sit
// where the registry allows it.
var ErrBadName = errors.New("registry: bad repository name")

// ValidName reports whether name is a valid OCI repository name. A path
// component may not start with an underscore, so crsp/_incoming/... is not one.
func ValidName(name string) bool {
	_, err := reference.WithName(name)
	return err == nil
}

// TargetKind classifies a promoted name: crsp/std/<profile> is standard,
// crsp/custom/<owner>-<repo>/<profile> is custom (R5.4).
func TargetKind(name string) (registry.Kind, bool) {
	if !ValidName(name) {
		return "", false
	}
	switch {
	case strings.HasPrefix(name, stdPrefix):
		if rest := name[len(stdPrefix):]; rest != "" && !strings.Contains(rest, "/") {
			return registry.KindStandard, true
		}
	case strings.HasPrefix(name, customPrefix):
		parts := strings.Split(name[len(customPrefix):], "/")
		if len(parts) == 2 && parts[0] != "" && parts[1] != "" {
			return registry.KindCustom, true
		}
	}
	return "", false
}

// ownIncoming is the staging root of one machine: crsp/incoming/<id>/.
func ownIncoming(machineID string) string { return registry.IncomingPrefix + machineID + "/" }

// checkMachineID refuses an id that cannot be one path component.
func checkMachineID(id string) error {
	if id == "" || strings.ContainsAny(id, "/\\*:@ \x00") || id == "." || id == ".." {
		return fmt.Errorf("%w: machine id %q", ErrBadName, id)
	}
	return nil
}

// OwnStaging reports whether name is below the staging root of machineID.
func OwnStaging(machineID, name string) bool {
	if checkMachineID(machineID) != nil || !ValidName(name) {
		return false
	}
	p := ownIncoming(machineID)
	return strings.HasPrefix(name, p) && len(name) > len(p)
}

// scopeCovers reports whether a scope name (exact, or a prefix ending in *)
// covers a repository name.
func scopeCovers(scopeName, name string) bool {
	if p, ok := strings.CutSuffix(scopeName, "*"); ok {
		return strings.HasPrefix(name, p)
	}
	return scopeName == name
}

// Allows reports whether the scopes grant action on the repository name.
func Allows(scopes []registry.Scope, name, action string) bool {
	for _, s := range scopes {
		if !scopeCovers(s.Name, name) {
			continue
		}
		for _, a := range s.Actions {
			if a == action {
				return true
			}
		}
	}
	return false
}
