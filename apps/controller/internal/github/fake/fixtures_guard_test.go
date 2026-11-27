package fake

import (
	"io/fs"
	"regexp"
	"testing"
)

var secretPatterns = regexp.MustCompile(`(ghs_|ghu_|ghr_|gho_|ghp_|github_pat_)[A-Za-z0-9_]{10,}|-----BEGIN [A-Z ]*PRIVATE KEY-----`)

// TestFixturesHoldNoSecrets is the R6.9 guard: no token pattern, no PEM block.
func TestFixturesHoldNoSecrets(t *testing.T) {
	n := 0
	err := fs.WalkDir(fixtureFS, "fixtures", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		n++
		b, err := fixtureFS.ReadFile(path)
		if err != nil {
			return err
		}
		if loc := secretPatterns.Find(b); loc != nil {
			t.Errorf("%s holds a secret-looking value", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("no fixtures found; an empty guard passes for the wrong reason")
	}
}

func TestGuardPatternCatchesTokens(t *testing.T) {
	if !secretPatterns.MatchString("x ghs_" + "abcdefghijklmnop") {
		t.Fatal("pattern must match a token")
	}
	if !secretPatterns.MatchString("-----BEGIN RSA PRIVATE KEY-----") {
		t.Fatal("pattern must match a PEM block")
	}
}
