package main

import "testing"

// The health check goes to loopback whatever the listen address is (TRAY8).
func TestHealthURLIsAlwaysLoopback(t *testing.T) {
	for in, want := range map[string]string{
		"192.168.1.162:8460": "http://127.0.0.1:8460/healthz",
		"192.168.1.169:9000": "http://127.0.0.1:9000/healthz",
		"0.0.0.0:8460":       "http://127.0.0.1:8460/healthz",
		"[::]:8461":          "http://127.0.0.1:8461/healthz",
		"127.0.0.1:8460":     "http://127.0.0.1:8460/healthz",
		"[fd00::5]:8470":     "http://127.0.0.1:8470/healthz",
		"bad":                "http://127.0.0.1:8460/healthz",
	} {
		if got := healthURL(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}
