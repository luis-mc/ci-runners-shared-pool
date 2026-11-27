package main

import "testing"

func TestConcreteListenHost(t *testing.T) {
	for in, want := range map[string]string{
		"192.168.1.20:8460": "192.168.1.20", "127.0.0.1:8460": "", "0.0.0.0:8460": "", "[::]:8460": "", "bad": "", "[fd00::5]:8460": "fd00::5",
	} {
		if got := concreteListenHost(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}

func TestIsWildcard(t *testing.T) {
	for in, want := range map[string]bool{"": true, "0.0.0.0": true, "::": true, "192.168.1.20": false, "127.0.0.1": false} {
		if isWildcard(in) != want {
			t.Errorf("%q", in)
		}
	}
}
