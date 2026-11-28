package persistent

import (
	"os"
	"path/filepath"
	"testing"
)

func writeDiag(t *testing.T, folder, name, body string) {
	t.Helper()
	d := filepath.Join(folder, "_diag")
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// CRSP382, runners R5.140: the listener's own words say the registration is gone.
func TestIsRegistrationLost(t *testing.T) {
	for text, want := range map[string]bool{
		"VssOAuthTokenRequestException: Registration was not found or is not medium trust":                           true,
		"Failed to create a session. The runner registration has been deleted from the server, please re-configure.": true,
		"Runner registrations are automatically deleted for runners that have not connected to the service recently": false,
		"A session for this runner already exists.":                                                                  false,
		"Listening for Jobs": false,
	} {
		if got := IsRegistrationLost(text); got != want {
			t.Errorf("%q: %v", text, got)
		}
	}
}

func TestRegistrationLostReadsTheNewestLogWithContent(t *testing.T) {
	f := t.TempDir()
	if RegistrationLost(f) || RegistrationLost("") {
		t.Fatal("no logs is not lost")
	}
	writeDiag(t, f, "Runner_20261128-100000-utc.log", "Registration was not found or is not medium trust")
	if !RegistrationLost(f) {
		t.Fatal("the newest log says so")
	}
	writeDiag(t, f, "Runner_20261128-100100-utc.log", "") // a start in progress writes nothing yet
	if !RegistrationLost(f) {
		t.Fatal("an empty newer log does not hide the loss")
	}
	writeDiag(t, f, "Runner_20261128-100200-utc.log", "Listening for Jobs")
	if RegistrationLost(f) {
		t.Fatal("a healthy newest log means the registration is back")
	}
}

func TestWithLost(t *testing.T) {
	for _, a := range []Action{Already, UpdateRights, RewriteEnv} {
		if WithLost(a, true) != RegisterLost || WithLost(a, false) != a {
			t.Errorf("%s", a)
		}
	}
	for _, a := range []Action{Fresh, Reregister} {
		if WithLost(a, true) != a {
			t.Errorf("%s registers anyway", a)
		}
	}
}
