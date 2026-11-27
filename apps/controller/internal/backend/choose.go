package backend

import (
	"fmt"
	"strings"
)

// Order is the backends of an OS, best first (decision 0003, runners R5.31).
// On Linux a microVM comes first only when the machine has /dev/kvm; a
// container is next, and a persistent runner is the last choice.
func Order(os string, kvm bool) []Name {
	switch os {
	case "linux":
		var o []Name
		if kvm {
			o = append(o, Firecracker, KVM)
		}
		return append(o, Podman, Docker, Persistent)
	case "windows":
		return []Name{HyperV, WSL2, Docker, Persistent}
	case "macos":
		return []Name{Tart, Persistent}
	}
	return []Name{Persistent}
}

// Choose picks the backend for a runner (runners R5.31). When the user chose
// one (want), only that one is taken, and the reason says why when it cannot
// be. When the user did not, the first backend in Order that is present,
// usable and allowed on the machine is taken. A backend that is present but
// not allowed, or not usable, is never taken, so the order falls through to
// the next one. The reason is empty on success.
func Choose(os string, kvm bool, found []Support, allowed map[Name]bool, want Name) (Support, string) {
	by := map[Name]Support{}
	for _, s := range found {
		by[s.Name] = s
	}
	why := func(n Name) string {
		s, ok := by[n]
		switch {
		case !ok || !s.Present:
			return fmt.Sprintf("%s is not installed here", n)
		case !allowed[n]:
			return fmt.Sprintf("%s is not allowed on this machine", n)
		case !s.Usable:
			if s.Reason != "" {
				return fmt.Sprintf("%s is not usable: %s", n, s.Reason)
			}
			return fmt.Sprintf("%s is not usable", n)
		}
		return ""
	}
	if want != "" {
		if r := why(want); r != "" {
			return Support{}, r
		}
		return by[want], ""
	}
	var skipped []string
	for _, n := range Order(os, kvm) {
		r := why(n)
		if r == "" {
			return by[n], ""
		}
		skipped = append(skipped, r)
	}
	return Support{}, "no backend can run a " + os + " runner here: " + strings.Join(skipped, "; ")
}
