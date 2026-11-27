package auth

import (
	"net"
	"net/url"
	"strings"
)

// CallbackPath is appended to every console address (R5.4).
const CallbackPath = "/auth/github/callback"

// IsLoopbackHost reports whether host (with or without a port) is 127.0.0.1
// or ::1. The name "localhost" is not accepted: GitHub rejects
// https://localhost and a name can be remapped (R5.4, R6.4).
func IsLoopbackHost(host string) bool {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	ip := net.ParseIP(host)
	return ip != nil && (ip.Equal(net.IPv4(127, 0, 0, 1)) || ip.Equal(net.IPv6loopback))
}

// NormalizeConsoleURL checks one console address: loopback uses http, any
// other host uses https (R5.4). It returns the origin without a trailing path.
func NormalizeConsoleURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return "", Invalid("console address %q is not an origin like https://host:8460", raw)
	}
	switch u.Scheme {
	case "http":
		if !IsLoopbackHost(u.Host) {
			return "", Invalid("console address %q: plain http is allowed on 127.0.0.1 and ::1 only", raw)
		}
	case "https":
		if strings.EqualFold(u.Hostname(), "localhost") {
			return "", Invalid("console address %q: GitHub rejects https://localhost; use http://127.0.0.1:<port>", raw)
		}
	default:
		return "", Invalid("console address %q: use http (loopback) or https", raw)
	}
	return u.Scheme + "://" + u.Host, nil
}

// CallbackURLs turns console addresses into callback URLs, refusing any that
// break R5.4. The result has no duplicates and keeps the order.
func CallbackURLs(consoleURLs []string) ([]string, error) {
	if len(consoleURLs) == 0 {
		return nil, Invalid("at least one console address is needed")
	}
	seen := map[string]bool{}
	var out []string
	for _, c := range consoleURLs {
		o, err := NormalizeConsoleURL(c)
		if err != nil {
			return nil, err
		}
		if !seen[o] {
			seen[o] = true
			out = append(out, o+CallbackPath)
		}
	}
	return out, nil
}

// AllLoopback reports whether every console address is loopback (R5.5).
func AllLoopback(consoleURLs []string) bool {
	for _, c := range consoleURLs {
		u, err := url.Parse(c)
		if err != nil || !IsLoopbackHost(u.Host) {
			return false
		}
	}
	return len(consoleURLs) > 0
}
