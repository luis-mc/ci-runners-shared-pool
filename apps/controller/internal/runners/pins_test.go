package runners

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const goodSHA = "af5c33fa94f3cc33b8e97937939136a6b04197e6dadfcfb3b6e33ae1bf41e79a"

// R5.79: only a digest reference is accepted; a tag never is.
func TestValidDigestRef(t *testing.T) {
	good := "ubuntu:24.04@sha256:" + goodSHA
	if !ValidDigestRef(good) || !ValidDigestRef("ghcr.io/o/tools@sha256:"+goodSHA) {
		t.Error("digest refs must be valid")
	}
	for _, bad := range []string{
		"", "ubuntu:24.04", "ubuntu", "ubuntu@sha256:abc",
		"ubuntu@sha256:" + strings.Repeat("a", 64),
		"ubuntu@sha256:" + strings.ToUpper(goodSHA),
		"ubuntu@sha512:" + goodSHA, "@sha256:" + goodSHA,
	} {
		if ValidDigestRef(bad) {
			t.Errorf("%q must be refused", bad)
		}
	}
}

// R5.79: the shipped pins file loads, and every image in it is by digest.
func TestShippedPinsFile(t *testing.T) {
	if pinsLoadEr != nil {
		t.Fatal(pinsLoadEr)
	}
	for _, os := range []string{"linux", "windows", "macos"} {
		for _, arch := range []string{"x64", "arm64"} {
			if _, err := PinFor(os, arch); err != nil {
				t.Errorf("%s/%s: %v", os, arch, err)
			}
		}
	}
	if len(imagePins) == 0 {
		t.Fatal("no image pins")
	}
	for name, ref := range imagePins {
		if !ValidDigestRef(ref) {
			t.Errorf("image %s is not by digest: %s", name, ref)
		}
	}
}

func TestLoadPinsRefuses(t *testing.T) {
	ok := `{"runner":[{"os":"linux","arch":"x64","version":"1.0.0","sha256":"` + goodSHA + `"}],"images":{"b":"u:1@sha256:` + goodSHA + `"}}`
	if _, _, err := loadPins([]byte(ok)); err != nil {
		t.Fatalf("good file: %v", err)
	}
	cases := map[string]struct {
		json string
		is   error
	}{
		"bad hash":    {strings.Replace(ok, goodSHA, "abc", 1), ErrBadHash},
		"repeat hash": {strings.Replace(ok, `"sha256":"`+goodSHA, `"sha256":"`+strings.Repeat("0", 64), 1), ErrBadHash},
		"tag image":   {strings.Replace(ok, "@sha256:"+goodSHA, "", 1), ErrNotDigestRef},
		"no version":  {strings.Replace(ok, `"1.0.0"`, `""`, 1), nil},
		"twice":       {strings.Replace(ok, `}],"images"`, `},{"os":"linux","arch":"x64","version":"1","sha256":"`+goodSHA+`"}],"images"`, 1), nil},
		"unknown key": {strings.Replace(ok, `"runner"`, `"extra":1,"runner"`, 1), nil},
		"not json":    {"{", nil},
	}
	for name, c := range cases {
		_, _, err := loadPins([]byte(c.json))
		if err == nil {
			t.Errorf("%s: must be refused", name)
		} else if c.is != nil && !errors.Is(err, c.is) {
			t.Errorf("%s: got %v", name, err)
		}
	}
}

// R5.79: the base image of the Dockerfile is the pinned reference of the
// pins file, so an update is one reviewed change of that file.
func TestDockerfileBaseIsThePinnedDigest(t *testing.T) {
	want, err := ImagePin("linux-base-from")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^FROM (\S+)( AS tools)?$`).FindStringSubmatch(readDockerfile(t))
	if m == nil || m[1] != want {
		t.Errorf("Dockerfile FROM %v, pins file %s", m, want)
	}
	// No FROM in the image tree may be a bare tag: a layer builds on the
	// ${BASE_IMAGE} argument, the base on the pinned digest.
	_ = filepath.Walk("../../../../images", func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || filepath.Base(p) != "Dockerfile" {
			return nil
		}
		b, rerr := os.ReadFile(p) //nolint:gosec // repository test data.
		if rerr != nil {
			t.Error(rerr)
			return nil
		}
		// A FROM may also name an earlier stage of the same file (`AS name`).
		stages := map[string]bool{}
		for _, s := range regexp.MustCompile(`(?m)^FROM \S+ AS (\S+)`).FindAllStringSubmatch(string(b), -1) {
			stages[s[1]] = true
		}
		for _, f := range regexp.MustCompile(`(?m)^FROM (\S+)`).FindAllStringSubmatch(string(b), -1) {
			if f[1] != "${BASE_IMAGE}" && !stages[f[1]] && !ValidDigestRef(f[1]) {
				t.Errorf("%s: FROM %s is not by digest", p, f[1])
			}
		}
		return nil
	})
}

func TestImagePinUnknown(t *testing.T) {
	if _, err := ImagePin("nope"); err == nil {
		t.Error("unknown image must fail")
	}
}
