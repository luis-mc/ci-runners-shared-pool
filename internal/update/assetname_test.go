package update

import "testing"

func TestReleaseNameAndPick(t *testing.T) {
	plain := "crsp-windows-amd64.msi"
	cases := []struct{ plain, version, want string }{
		{plain, "0.1.22", "Crsp-0.1.22-win-x64.msi"},
		{plain, "v0.1.22", "Crsp-0.1.22-win-x64.msi"},
		{"crsp-windows-arm64.msi", "0.1.22", "Crsp-0.1.22-win-arm64.msi"},
		{"crsp-linux-amd64.deb", "1.0.0", "Crsp-1.0.0-linux-x64.deb"},
		{"crsp-darwin-arm64.pkg", "1.0.0", "Crsp-1.0.0-macos-arm64.pkg"},
		{"crsp-linux-amd64.tar.gz", "1.0.0", "Crsp-1.0.0-linux-x64.tar.gz"},
		{"odd.msi", "1.0.0", ""},
	}
	for _, c := range cases {
		if got := ReleaseName(c.plain, c.version); got != c.want {
			t.Errorf("ReleaseName(%q,%q) = %q, want %q", c.plain, c.version, got, c.want)
		}
	}
	if got := PlainName("Crsp-0.1.22-win-x64.msi"); got != plain {
		t.Errorf("PlainName of the lifecycle name: %q", got)
	}
	// new name first, old versioned name second, plain name last
	rel := Release{Version: "0.1.22", Assets: map[string]string{
		plain: "p", "crsp-windows-amd64_0.1.22.msi": "v", "Crsp-0.1.22-win-x64.msi": "n"}}
	if got := PickAsset(rel, plain); got != "Crsp-0.1.22-win-x64.msi" {
		t.Errorf("all three: %q", got)
	}
	delete(rel.Assets, "Crsp-0.1.22-win-x64.msi")
	if got := PickAsset(rel, plain); got != "crsp-windows-amd64_0.1.22.msi" {
		t.Errorf("old versioned name second: %q", got)
	}
	if ManifestName("Crsp-0.1.22-win-x64.msi") != "Crsp-0.1.22-win-x64.msi.manifest.json" {
		t.Error("manifest name")
	}
}
