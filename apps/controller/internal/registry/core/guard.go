package core

import (
	"net"
	"net/http"
	"net/netip"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/lancache"
)

// CheckListen accepts the address of the listener the registry is mounted on
// only when it follows the private LAN rule of lancache: one literal private,
// link-local or loopback address, never a wildcard, a public address or a name
// (R5.2).
func CheckListen(addr string) error { return lancache.CheckLANAddr(addr) }

// PeerAllowed reports whether a remote address (host:port) is a private,
// loopback or link-local peer.
func PeerAllowed(remote string) bool {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		host = remote
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	ip = ip.Unmap()
	if ip.IsUnspecified() || ip.IsMulticast() {
		return false
	}
	return ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast()
}

// privatePeersOnly refuses a request from a public peer before anything else.
func privatePeersOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !PeerAllowed(r.RemoteAddr) {
			http.Error(w, "the registry answers private network peers only", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// MirrorGuard puts the token check in front of the pull-through mirror (R5.3):
// a private peer with a valid machine token passes, anyone else gets 401 or 403.
func (i *Issuer) MirrorGuard(next http.Handler) http.Handler {
	return privatePeersOnly(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok := tokenOf(r)
		if tok == "" {
			challenge(w)
			return
		}
		if _, _, err := i.Verify(r.Context(), tok); err != nil {
			challenge(w)
			return
		}
		next.ServeHTTP(w, r)
	}))
}
