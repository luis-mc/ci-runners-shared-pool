package update

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/update/lifecycle"
)

// assetsManifest is the manifest scripts/ci/release_manifest.py writes: the default package is the
// Windows x64 MSI and `assets` lists both Windows packages.
func assetsManifest() map[string]interface{} {
	return map[string]interface{}{
		"product": "crsp", "channel": "stable", "version": "1.1.0", "required": false,
		"expires_at": float64(1_900_000_000),
		"url":        "https://github.com/x/Crsp-1.1.0-win-x64.msi", "sha256": "x64sum",
		"assets": map[string]interface{}{
			"win-x64":   map[string]interface{}{"url": "https://github.com/x/Crsp-1.1.0-win-x64.msi", "sha256": "x64sum"},
			"win-arm64": map[string]interface{}{"url": "https://github.com/x/Crsp-1.1.0-win-arm64.msi", "sha256": "armsum"},
		},
	}
}

func TestTargetKey(t *testing.T) {
	for in, want := range map[string]string{
		"Crsp-1.1.0-win-x64.msi":       "win-x64",
		"Crsp-1.1.0-win-arm64.msi":     "win-arm64",
		"crsp-windows-arm64.msi":       "win-arm64",
		"crsp-windows-1.1.0-arm64.msi": "win-arm64",
		"crsp-linux-amd64.tar.gz":      "linux-x64",
		"Crsp-1.1.0-macos-arm64.pkg":   "macos-arm64",
		"notes.txt":                    "",
		"Crsp-1.1.0-plan9-arm64.msi":   "",
	} {
		if got := TargetKey(in); got != want {
			t.Errorf("TargetKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestManifestAssetsAreParsedAndPickedByTarget(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	m := assetsManifest()
	body, _ := lifecycle.Canonical(m)
	raw := []byte(`{"manifest":` + string(body) + `,"signature":"` + hex.EncodeToString(ed25519.Sign(priv, body)) + `"}`)
	mf, bad := lifecycle.ParseManifest(raw, pub, "crsp", 1_800_000_000, "1.0.0")
	if bad != "" {
		t.Fatalf("refused: %s", bad)
	}
	if a, ok := mf.For("win-arm64"); !ok || a.SHA256 != "armsum" {
		t.Fatalf("arm64 package: %+v %v", a, ok)
	}
	if a, ok := mf.For("win-x64"); !ok || a.SHA256 != "x64sum" {
		t.Fatalf("x64 package: %+v %v", a, ok)
	}
	if _, ok := mf.For("linux-x64"); ok {
		t.Fatal("a target that is not listed must not get the default package")
	}
}

func TestArm64UpdaterTakesItsOwnAsset(t *testing.T) {
	for _, tc := range []struct {
		target, sum, want string
	}{
		{"win-arm64", "armsum", "installing"},
		{"win-x64", "x64sum", "installing"},
		{"win-arm64", "x64sum", "rolled_back_or_refused"}, // the x64 file on an arm64 machine
	} {
		f := newSvc(t, true, true)
		f.eng.manifest = assetsManifest()
		f.eng.sum = tc.sum
		f.svc.u.Target = tc.target
		f.advance(11 * time.Minute)
		st := f.svc.Status(context.Background()).State
		if tc.want == "installing" && st != "installing" {
			t.Errorf("%s with %s: state %s", tc.target, tc.sum, st)
		}
		if tc.want != "installing" && st == "installing" {
			t.Errorf("%s with %s: installed the wrong package", tc.target, tc.sum)
		}
	}
}

func TestSharedManifestIsVerifiedPerAsset(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	dir := t.TempDir()
	pkg := filepath.Join(dir, "pkg.msi")
	if err := os.WriteFile(pkg, []byte("arm64 build"), 0o600); err != nil {
		t.Fatal(err)
	}
	sum, _ := FileSHA256(pkg)
	m := assetsManifest()
	m["assets"].(map[string]interface{})["win-arm64"].(map[string]interface{})["sha256"] = sum
	body, _ := lifecycle.Canonical(m)
	sig := hex.EncodeToString(ed25519.Sign(priv, body))
	files := map[string][]byte{"https://github.com/x/manifest.json": body, "https://github.com/x/manifest.json.sig": []byte(sig + "\n")}
	mgr := New(Options{
		Current: "1.0.0", Dir: filepath.Join(dir, "work"), Key: pub, RequireManifest: true,
		Now:      func() time.Time { return time.Unix(1_800_000_000, 0) },
		AllowURL: func(string) bool { return true },
		Fetch: func(_ context.Context, u, dst string) error {
			b, ok := files[u]
			if !ok {
				return errors.New("no such file " + u)
			}
			return os.WriteFile(dst, b, 0o600)
		},
	})
	rel := Release{Version: "1.1.0", Assets: map[string]string{
		"Crsp-1.1.0-win-x64.msi": "https://github.com/x/Crsp-1.1.0-win-x64.msi", "Crsp-1.1.0-win-arm64.msi": "https://github.com/x/Crsp-1.1.0-win-arm64.msi",
		"manifest.json": "https://github.com/x/manifest.json", "manifest.json.sig": "https://github.com/x/manifest.json.sig",
	}}
	if err := mgr.verifyManifest(context.Background(), rel, "Crsp-1.1.0-win-arm64.msi", pkg); err != nil {
		t.Fatalf("the arm64 file against the arm64 entry: %v", err)
	}
	var mf *ManifestFailure
	if err := mgr.verifyManifest(context.Background(), rel, "Crsp-1.1.0-win-x64.msi", pkg); !errors.As(err, &mf) || mf.Reason != "bad_hash" {
		t.Fatalf("the arm64 file against the x64 entry must be refused: %v", err)
	}
	files["https://github.com/x/manifest.json"] = append([]byte(" "), body...) // changed after signing
	files["https://github.com/x/manifest.json"][1] = '['                       // not even JSON
	if err := mgr.verifyManifest(context.Background(), rel, "Crsp-1.1.0-win-arm64.msi", pkg); !errors.As(err, &mf) || mf.Reason != "bad_signature" {
		t.Fatalf("a changed manifest must be refused: %v", err)
	}
}
