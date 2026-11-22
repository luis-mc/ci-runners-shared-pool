package update

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// CRSP224, TRANSITION: the updater of 0.1.21 and older looks for crsp-<goos>-<goarch>_<version>.<ext>.
// OPS69 renamed the release assets to Crsp-<version>-<os>-<arch>.<ext> and those updaters were stranded.
// These functions are a copy of the naming rule of v0.1.21 (internal/update/source.go: VersionedName,
// PickAsset, PickFromSums, AssetName, AssetFor), kept as a fixture so a rename cannot break it again.

func v0121VersionedName(plain, version string) string {
	version = strings.TrimPrefix(version, "v")
	for _, ext := range []string{".tar.gz", ".zip", ".msi", ".pkg", ".deb", ".rpm"} {
		if strings.HasSuffix(plain, ext) {
			return strings.TrimSuffix(plain, ext) + "_" + version + ext
		}
	}
	return plain
}

func v0121AssetName(goos, goarch string) string {
	if goos == "windows" {
		return "crsp-windows-" + goarch + ".msi"
	}
	return "crsp-" + goos + "-" + goarch + ".tar.gz"
}

func v0121AssetFor(kind, goos, goarch string) string {
	switch kind {
	case KindMSI:
		return "crsp-windows-" + goarch + ".msi"
	case KindPKG:
		return "crsp-darwin-" + goarch + ".pkg"
	case KindDEB:
		return "crsp-linux-" + goarch + ".deb"
	case KindRPM:
		return "crsp-linux-" + goarch + ".rpm"
	}
	return v0121AssetName(goos, goarch)
}

// v0121Lookup is what 0.1.21 does: the versioned name, else the plain name, in a list of names.
func v0121Lookup(names map[string]bool, plain, version string) (string, bool) {
	if n := v0121VersionedName(plain, version); names[n] {
		return n, true
	}
	if names[plain] {
		return plain, true
	}
	return "", false
}

// releaseAssetNames is the installers and archives of a release, as the publish job names them.
func releaseAssetNames(version string) []string {
	var out []string
	for _, arch := range []string{"x64", "arm64"} {
		out = append(out, "Crsp-"+version+"-win-"+arch+".msi", "Crsp-"+version+"-win-"+arch+".zip")
		for _, os := range []string{"linux", "macos"} {
			out = append(out, "Crsp-"+version+"-"+os+"-"+arch+".tar.gz")
		}
		out = append(out, "Crsp-"+version+"-linux-"+arch+".deb", "Crsp-"+version+"-linux-"+arch+".rpm", "Crsp-"+version+"-macos-"+arch+".pkg")
	}
	return out
}

// legacyCopies runs scripts/ci/release-legacy.sh, the step release.yml runs, on a folder with the
// Crsp-* assets and returns the file names it leaves.
func legacyCopies(t *testing.T, version string) []string {
	t.Helper()
	dir := t.TempDir()
	for _, n := range releaseAssetNames(version) {
		if err := os.WriteFile(filepath.Join(dir, n), []byte(n), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	script, err := filepath.Abs(filepath.Join("..", "..", "scripts", "ci", "release-legacy.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if out, err := exec.CommandContext(t.Context(), "bash", script, dir, version).CombinedOutput(); err != nil { //nolint:gosec // fixed script, temp dir
		t.Fatalf("release-legacy.sh: %v\n%s", err, out)
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range ents {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names
}

type legacyTarget struct{ kind, goos, goarch string }

func v0121Targets() []legacyTarget {
	var out []legacyTarget
	for _, arch := range []string{"amd64", "arm64"} {
		out = append(out,
			legacyTarget{KindMSI, "windows", arch},
			legacyTarget{KindPKG, "darwin", arch},
			legacyTarget{KindDEB, "linux", arch},
			legacyTarget{KindRPM, "linux", arch},
			legacyTarget{KindTarball, "linux", arch},
			legacyTarget{KindTarball, "darwin", arch},
		)
	}
	return out
}

func TestV0121UpdaterFindsAnAssetForEveryTarget(t *testing.T) {
	for _, version := range []string{"0.1.22", "edge-abc1234"} {
		names := legacyCopies(t, version)
		if len(names) != 28 {
			t.Fatalf("%s: %d files, want 14 Crsp-* and 14 legacy copies: %v", version, len(names), names)
		}
		set := map[string]bool{}
		sums := ""
		for _, n := range names {
			set[n] = true
			sums += fmt.Sprintf("%064x  %s\n", len(n), n)
		}
		for _, tg := range v0121Targets() {
			plain := v0121AssetFor(tg.kind, tg.goos, tg.goarch)
			got, ok := v0121Lookup(set, plain, version)
			if !ok {
				t.Errorf("%s: a 0.1.21 %s updater on %s/%s finds no asset (wants %s)", version, tg.kind, tg.goos, tg.goarch, v0121VersionedName(plain, version))
				continue
			}
			// SHA256SUMS lookup, the same name as the release asset
			if _, ok := SumFor([]byte(sums), got); !ok {
				t.Errorf("%s: %s is not in SHA256SUMS", version, got)
			}
		}
	}
}

func TestLegacyNamesAreExactlyTheV0121Names(t *testing.T) {
	names := legacyCopies(t, "0.1.22")
	want := []string{}
	for _, n := range []string{"windows-amd64.msi", "windows-arm64.msi", "windows-amd64.zip", "windows-arm64.zip",
		"linux-amd64.tar.gz", "linux-arm64.tar.gz", "darwin-amd64.tar.gz", "darwin-arm64.tar.gz",
		"linux-amd64.deb", "linux-arm64.deb", "linux-amd64.rpm", "linux-arm64.rpm", "darwin-amd64.pkg", "darwin-arm64.pkg"} {
		want = append(want, v0121VersionedName("crsp-"+n, "0.1.22"))
	}
	want = append(want, releaseAssetNames("0.1.22")...)
	sort.Strings(want)
	if strings.Join(names, "\n") != strings.Join(want, "\n") {
		t.Errorf("got\n%s\nwant\n%s", strings.Join(names, "\n"), strings.Join(want, "\n"))
	}
}

// The new updater, on a release that carries both names, takes the Crsp-* name.
func TestNewUpdaterPrefersTheNewNamesWhenBothArePublished(t *testing.T) {
	version := "0.1.22"
	names := legacyCopies(t, version)
	assets := map[string]string{}
	sums := ""
	for _, n := range names {
		assets[n] = "https://github.com/x/" + n
		sums += fmt.Sprintf("%064x  %s\n", len(n), n)
	}
	rel := Release{Version: version, Assets: assets}
	for _, tg := range v0121Targets() {
		plain := v0121AssetFor(tg.kind, tg.goos, tg.goarch)
		want := ReleaseName(plain, version)
		if want == "" {
			t.Fatalf("no release name for %s", plain)
		}
		if got := PickAsset(rel, plain); got != want {
			t.Errorf("PickAsset(%s) = %s, want %s", plain, got, want)
		}
		if got := PickFromSums([]byte(sums), plain, version); got != want {
			t.Errorf("PickFromSums(%s) = %s, want %s", plain, got, want)
		}
	}
}
