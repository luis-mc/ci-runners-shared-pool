package backend

import (
	"strings"
	"testing"
)

func sup(n Name, usable bool) Support { return Support{Name: n, Present: true, Usable: usable} }

func allow(n ...Name) map[Name]bool {
	m := map[Name]bool{}
	for _, x := range n {
		m[x] = true
	}
	return m
}

// R5.31: the first allowed, usable backend in the order of decision 0003.
func TestBestBackendFirst(t *testing.T) {
	all := []Support{sup(Persistent, true), sup(Docker, true), sup(Firecracker, true)}
	allowAll := allow(Persistent, Docker, Firecracker, Podman, KVM)

	// Linux with /dev/kvm: microVM, then container, then persistent.
	if got, why := Choose("linux", true, all, allowAll, ""); got.Name != Firecracker || why != "" {
		t.Fatalf("kvm: %v %q", got.Name, why)
	}
	// Linux without /dev/kvm: the microVM is not in the order at all.
	if got, _ := Choose("linux", false, all, allowAll, ""); got.Name != Docker {
		t.Fatalf("no kvm: %v", got.Name)
	}
	// Podman before Docker.
	withPodman := append([]Support{sup(Podman, true)}, all...)
	if got, _ := Choose("linux", false, withPodman, allowAll, ""); got.Name != Podman {
		t.Fatalf("podman: %v", got.Name)
	}
	// Not allowed or not usable falls through; never used.
	if got, _ := Choose("linux", true, all, allow(Persistent, Docker), ""); got.Name != Docker {
		t.Fatalf("firecracker not allowed: %v", got.Name)
	}
	down := []Support{sup(Docker, false), sup(Persistent, true)}
	if got, _ := Choose("linux", false, down, allowAll, ""); got.Name != Persistent {
		t.Fatalf("docker down: %v", got.Name)
	}
	// The user's choice is the only one tried, and its failure is named.
	if _, why := Choose("linux", false, down, allowAll, Docker); !strings.Contains(why, "docker is not usable") {
		t.Fatalf("why = %q", why)
	}
	if got, _ := Choose("linux", false, all, allowAll, Persistent); got.Name != Persistent {
		t.Fatalf("user choice: %v", got.Name)
	}
	// Nothing fits: every reason is in the message.
	if got, why := Choose("macos", false, nil, allowAll, ""); got.Name != "" || !strings.Contains(why, "tart is not installed") {
		t.Fatalf("none: %v %q", got.Name, why)
	}
	// Windows: container in WSL2 before persistent.
	if got, _ := Choose("windows", false, []Support{sup(Docker, true), sup(Persistent, true)}, allowAll, ""); got.Name != Docker {
		t.Fatalf("windows: %v", got.Name)
	}
}
