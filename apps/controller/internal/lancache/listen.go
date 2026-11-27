package lancache

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/netip"
)

// ErrNotLAN is returned for a listen address that is not one private LAN
// address with a port.
var ErrNotLAN = errors.New("the LAN cache binds one private LAN address, never a wildcard or a public one")

// CheckLANAddr accepts host:port only when the host is a literal private,
// link-local or loopback address. A wildcard, a public address or a host name
// is refused, so the cache is never open to the internet.
func CheckLANAddr(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrNotLAN, err)
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return ErrNotLAN
	}
	ip = ip.Unmap()
	if ip.IsUnspecified() || ip.IsMulticast() {
		return ErrNotLAN
	}
	if ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return nil
	}
	return ErrNotLAN
}

// Listen opens the TLS listener on the LAN address. The certificate is the
// controller's, so peers pin it like the agent link does.
func Listen(ctx context.Context, addr string, cfg *tls.Config) (net.Listener, error) {
	if err := CheckLANAddr(addr); err != nil {
		return nil, err
	}
	l, err := (&net.ListenConfig{}).Listen(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", addr, err)
	}
	return tls.NewListener(l, cfg), nil
}
