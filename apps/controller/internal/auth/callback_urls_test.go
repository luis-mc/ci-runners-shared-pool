package auth

import "testing"

func TestCallbackURLs(t *testing.T) { // R5.4
	got, err := CallbackURLs([]string{"http://127.0.0.1:8460", "https://pool.lan:8460/", "http://127.0.0.1:8460", "http://[::1]:8460"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"http://127.0.0.1:8460/auth/github/callback", "https://pool.lan:8460/auth/github/callback", "http://[::1]:8460/auth/github/callback"}
	if len(got) != len(want) {
		t.Fatalf("%v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%d: %s != %s", i, got[i], want[i])
		}
	}
}

func TestCallbackURLsRefusals(t *testing.T) { // R5.4, R6.4
	for _, bad := range []string{
		"https://localhost:8460",        // GitHub rejects it
		"http://localhost:8460",         // a name, not loopback
		"http://pool.lan:8460",          // http on a non-loopback host
		"http://192.168.1.5:8460",       // http on a LAN address
		"ftp://127.0.0.1",               // scheme
		"https://pool.lan:8460/x",       // a path
		"pool.lan:8460",                 // no scheme
		"https://user:pw@pool.lan:8460", // credentials
		"",
	} {
		if _, err := CallbackURLs([]string{bad}); err == nil {
			t.Errorf("%q must be refused", bad)
		}
	}
	if _, err := CallbackURLs(nil); err == nil {
		t.Error("no address must be refused")
	}
}

func TestIsLoopbackHost(t *testing.T) {
	for h, want := range map[string]bool{"127.0.0.1": true, "127.0.0.1:8460": true, "::1": true, "[::1]:8460": true,
		"localhost": false, "10.0.0.1": false, "pool.lan": false, "": false} {
		if IsLoopbackHost(h) != want {
			t.Errorf("%q: want %v", h, want)
		}
	}
}

func TestAllLoopback(t *testing.T) {
	if !AllLoopback([]string{"http://127.0.0.1:8460"}) || AllLoopback([]string{"http://127.0.0.1:8460", "https://pool.lan"}) || AllLoopback(nil) {
		t.Fatal("AllLoopback wrong")
	}
}
