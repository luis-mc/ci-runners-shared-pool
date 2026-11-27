package notify

import (
	"context"
	"net"
	"strings"
	"syscall"
	"time"
)

// The guard keeps a channel from turning the controller into a way to reach
// services that only the controller machine can reach (SSRF). A webhook or a
// mail server may be on the local network, which is where a self-hosted pool
// lives (notifications section 4: "hooks.example.lan"), but never:
//
//   - the controller machine itself (loopback, the unspecified address),
//   - a link-local address, which includes the cloud metadata address
//     169.254.169.254,
//   - a multicast address.
//
// The address is checked twice: when the channel is saved (a literal address
// or the name "localhost"), and again at connect time on the address actually
// dialled, so a name that later resolves to a refused address, or a redirect,
// cannot get through. A mail server on the controller machine itself must be
// reached by the machine's network address, not by 127.0.0.1.

// ipRefused says why an address may not be dialled, or "".
func ipRefused(ip net.IP, allowLoopback bool) string {
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	switch {
	case ip.IsLoopback():
		if allowLoopback {
			return ""
		}
		return "points to this machine (a loopback address), which the controller does not send to."
	case ip.IsUnspecified():
		return "points to this machine (the unspecified address), which the controller does not send to."
	case ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast():
		return "points to a link-local address, such as the cloud metadata address, which the controller does not send to."
	case ip.IsMulticast():
		return "points to a multicast address, which the controller does not send to."
	}
	return ""
}

// hostRefused checks a host name or a literal address given by a user.
func hostRefused(host string, allowLoopback bool) string {
	host = strings.Trim(strings.TrimSpace(host), "[]")
	if ip := net.ParseIP(host); ip != nil {
		return ipRefused(ip, allowLoopback)
	}
	lower := strings.ToLower(strings.TrimSuffix(host, "."))
	if !allowLoopback && (lower == "localhost" || strings.HasSuffix(lower, ".localhost")) {
		return "points to this machine (localhost), which the controller does not send to."
	}
	return ""
}

// guardError is the refusal of the address guard at connect time.
type guardError struct{ why string }

func (e *guardError) Error() string { return "refused by the address guard: the address " + e.why }

// guardedDialer dials like net.Dialer and refuses a refused address at the
// moment of connecting.
func (s *Service) guardedDialer(timeout time.Duration) *net.Dialer {
	return &net.Dialer{
		Timeout: timeout,
		Control: func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return &guardError{why: "is not valid."}
			}
			ip := net.ParseIP(host)
			if ip == nil {
				return &guardError{why: "is not valid."}
			}
			if why := ipRefused(ip, s.cfg.AllowLoopback); why != "" {
				return &guardError{why: why}
			}
			return nil
		},
	}
}

// dialContext is the guarded DialContext of both senders.
func (s *Service) dialContext(timeout time.Duration) func(ctx context.Context, network, addr string) (net.Conn, error) {
	d := s.guardedDialer(timeout)
	return d.DialContext
}
