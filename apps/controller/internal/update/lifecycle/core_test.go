package lifecycle

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"testing"
)

type env struct {
	manifest []byte
	getErr   error
	sum      string
	installs int
	fail     bool
	handed   bool
}

func (e *env) Get(string) ([]byte, error) { return e.manifest, e.getErr }
func (e *env) Package(Manifest) (string, error) {
	return e.sum, nil
}
func (e *env) Install(Manifest, string, bool) (bool, error) {
	e.installs++
	if e.handed {
		return false, nil
	}
	if e.fail {
		return true, errors.New("self-test failed")
	}
	return true, nil
}

type rows struct{ got []Row }

func (r *rows) Audit(x Row) { r.got = append(r.got, x) }

func signed(t *testing.T, priv ed25519.PrivateKey, version string, expires float64, sum string) []byte {
	t.Helper()
	m := map[string]interface{}{"product": "crsp", "channel": "stable", "version": version, "required": false,
		"expires_at": expires, "url": "https://example/pkg", "sha256": sum}
	body, err := Canonical(m)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]interface{}{"manifest": m, "signature": hex.EncodeToString(ed25519.Sign(priv, body))})
	return raw
}

func fixture(t *testing.T) (*Updater, *env, *rows, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	e := &env{sum: "aa"}
	r := &rows{}
	e.manifest = signed(t, priv, "1.1.0", 1e12, "aa")
	return New("1.0.0", true, PolicyAutomatic, nil, "stable", true, 1_000_000, "http://x", pub, 7, e, r), e, r, priv
}

func TestFailedInstallsRetryAt1hThen4hThenSkip(t *testing.T) {
	u, e, _, _ := fixture(t)
	e.fail = true
	u.Advance(601)
	if u.State != "rolled_back" || u.RetryAt == nil || *u.RetryAt-u.Now < 3500 || *u.RetryAt-u.Now > 3600 {
		t.Fatalf("first failure %s %v", u.State, u.RetryAt)
	}
	u.Advance(3600)
	if u.Failures != 2 || e.installs != 2 {
		t.Fatalf("second failure: failures %d installs %d", u.Failures, e.installs)
	}
	u.Advance(14400)
	if u.State != "skipped" || u.Failures != 3 {
		t.Fatalf("third failure ends in skipped: %s", u.State)
	}
	n := e.installs
	u.Advance(86400)
	if e.installs != n {
		t.Fatal("a skipped version installed again")
	}
}

func TestHandedOverInstallResumes(t *testing.T) {
	u, e, r, _ := fixture(t)
	e.handed = true
	u.Advance(601)
	if u.State != "installing" {
		t.Fatalf("state %s", u.State)
	}
	u.Resume(true)
	if u.State != "updated" || u.Version != "1.1.0" {
		t.Fatalf("%s %s", u.State, u.Version)
	}
	last := r.got[len(r.got)-1]
	if last.Kind != "install" || last.Result != "ok" || last.Actor != "updater" {
		t.Fatalf("%+v", last)
	}
}

func TestManifestRefusals(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	now := 1000.0
	cases := []struct {
		name string
		raw  []byte
		pub  ed25519.PublicKey
		want string
	}{
		{"good", signed(t, priv, "1.1.0", 2000, "aa"), pub, ""},
		{"bad signature", signed(t, other, "1.1.0", 2000, "aa"), pub, BadSignature},
		{"no key", signed(t, priv, "1.1.0", 2000, "aa"), nil, BadSignature},
		{"expired", signed(t, priv, "1.1.0", 900, "aa"), pub, Expired},
		{"older", signed(t, priv, "0.9.0", 2000, "aa"), pub, OlderVersion},
		{"garbage", []byte("{"), pub, BadSignature},
	}
	for _, c := range cases {
		if _, got := ParseManifest(c.raw, c.pub, "", now, "1.0.0"); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

func TestPlaceholderKeyHasNoKey(t *testing.T) {
	if _, err := ParsePublicKey([]byte("# a comment only\n")); !errors.Is(err, ErrNoKey) {
		t.Fatalf("%v", err)
	}
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	der, _ := x509.MarshalPKIXPublicKey(pub)
	text := append([]byte("# placeholder comment\n"), pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})...)
	got, err := ParsePublicKey(text)
	if err != nil || !got.Equal(pub) {
		t.Fatalf("%v", err)
	}
}

func TestWindowOpensTheInstall(t *testing.T) {
	u, _, _, _ := fixture(t)
	u.Window = &Window{Start: "02:00", End: "03:00"}
	u.Now = 86400 * 100 // 00:00 UTC
	u.Advance(601)
	if u.State != "waiting" {
		t.Fatalf("outside the window: %s", u.State)
	}
	u.Advance(2 * 3600)
	if u.State != "updated" {
		t.Fatalf("the window opened: %s", u.State)
	}
}

func TestRestoreAfterFailedInstall(t *testing.T) {
	u, _, _, _ := fixture(t)
	u.Restore("1.1.0", false, 2, 5000)
	if u.State != "rolled_back" || u.RetryAt == nil || *u.RetryAt != 5000+14400 {
		t.Fatalf("%s %v", u.State, u.RetryAt)
	}
	u.Restore("1.1.0", false, 3, 5000)
	if u.State != "skipped" {
		t.Fatalf("%s", u.State)
	}
}
