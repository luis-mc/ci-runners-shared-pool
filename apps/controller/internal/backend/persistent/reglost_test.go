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

// CRSP382, runners R5.152: the listener's own words say the registration is gone.
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
	if err := os.WriteFile(filepath.Join(f, ".runner"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
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

// CRSP385: runner 2.338 keeps a copy of its settings in .runner_migrated; a
// folder with logs but neither file was cleaned for a register again that
// failed, so it is not registered, whatever its newest log says. Both copies
// are stale files of an old registration.
func TestRegistrationLostWhenTheFolderHoldsNoRegistration(t *testing.T) {
	f := t.TempDir()
	writeDiag(t, f, "Runner_20261128-040529-utc.log", "Cannot configure the runner because it is already configured.")
	if !RegistrationLost(f) {
		t.Fatal("a folder with logs and no .runner is not registered")
	}
	if err := os.WriteFile(filepath.Join(f, ".runner_migrated"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if RegistrationLost(f) {
		t.Fatal(".runner_migrated holds the registration as well")
	}
	for _, n := range []string{".runner", ".credentials", ".credentials_rsaparams", ".runner_migrated", ".credentials_migrated"} {
		found := false
		for _, s := range StaleRegistrationFiles {
			found = found || s == n
		}
		if !found {
			t.Errorf("%s is not removed before a register again", n)
		}
	}
}
