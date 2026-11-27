package api

import "testing"

func TestSameMachine(t *testing.T) { // OPS16
	for addr, want := range map[string]bool{
		"127.0.0.1:5000": true, "[::1]:5000": true, "203.0.113.9:5000": false, "": false, "not-an-address": false,
	} {
		if got := sameMachine(addr); got != want {
			t.Errorf("sameMachine(%q) = %v, want %v", addr, got, want)
		}
	}
}
