package profiles

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry"
)

// Errors.
var (
	ErrNotFound       = errors.New("profiles: no such profile")
	ErrBadProfile     = errors.New("profiles: bad profile")
	ErrBaseUnresolved = errors.New("profiles: base digest not resolved")
)

// Service is a sidecar started per job. Services are not part of the image
// key (R5.23).
type Service struct {
	Name  string `json:"name"`
	Image string `json:"image"`
}

// Profile is base, toolchains, pins, services and the labels that pick it
// (R5.77).
type Profile struct {
	ID         string
	Kind       registry.Kind
	Repository string // owner/repo of a custom profile; empty for a standard one
	Title      string
	BaseName   string
	BaseDigest string // empty until resolved
	Pins       map[string]string
	Toolchains []registry.Toolchain
	Services   []Service
	Labels     []string // runs-on labels that pick it
	Stacks     []string // stack names (DetectStacks) that map to it
	BuiltIn    bool
}

var idRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)

// Validate checks a profile before it is stored.
func (p Profile) Validate() error {
	if !idRe.MatchString(p.ID) {
		return fmt.Errorf("%w: id %q must be lower case letters, digits and dashes", ErrBadProfile, p.ID)
	}
	switch p.Kind {
	case registry.KindStandard:
		if p.Repository != "" {
			return fmt.Errorf("%w: a standard profile has no repository", ErrBadProfile)
		}
	case registry.KindCustom:
		if strings.Count(p.Repository, "/") != 1 {
			return fmt.Errorf("%w: a custom profile needs owner/repo", ErrBadProfile)
		}
	default:
		return fmt.Errorf("%w: kind %q", ErrBadProfile, p.Kind)
	}
	if strings.TrimSpace(p.BaseName) == "" {
		return fmt.Errorf("%w: no base", ErrBadProfile)
	}
	return nil
}

// Ref is what the key needs from the profile (R5.78).
func (p Profile) Ref() registry.ProfileRef {
	pins := make(map[string]string, len(p.Pins))
	for k, v := range p.Pins {
		pins[k] = v
	}
	tc := append([]registry.Toolchain(nil), p.Toolchains...)
	sort.Slice(tc, func(i, j int) bool {
		if tc[i].Tool != tc[j].Tool {
			return tc[i].Tool < tc[j].Tool
		}
		return tc[i].Version < tc[j].Version
	})
	return registry.ProfileRef{ID: p.ID, Kind: p.Kind, BaseName: p.BaseName, BaseDigest: p.BaseDigest, Pins: pins, Toolchains: tc}
}
