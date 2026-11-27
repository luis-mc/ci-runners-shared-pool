package runners

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
)

// AccountPrefix and RunnersGroup name the runner accounts and their group
// (runners R5.13, R5.21, R5.23).
const (
	AccountPrefix = "crsp-"
	RunnersGroup  = "crsp-runners"
	// MaxAccountLen is the longest account name; the Windows limit is 20.
	MaxAccountLen = 20
)

// sanitizeSlug keeps lower-case letters, digits and single hyphens.
func sanitizeSlug(s string) string {
	var b strings.Builder
	lastHyphen := true
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastHyphen = false
		case !lastHyphen:
			b.WriteByte('-')
			lastHyphen = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func hash4(s string, salt int) string {
	sum := sha256.Sum256([]byte(s + "#" + strconv.Itoa(salt)))
	return hex.EncodeToString(sum[:])[:4]
}

// AccountName is `crsp-<short slug>`: at most 20 characters, lower-case
// letters, digits and hyphens, the same for the same runner name on every run.
// A slug that does not fit, or a name that taken says is in use by someone
// else, gets a 4-character hash (port of dev-standards DS R5.31 and the slug
// tests of test_dl310_windows_runner.py).
func AccountName(runnerName string, taken func(string) bool) string {
	slug := sanitizeSlug(runnerName)
	// A runner already named crsp-… must not get the prefix twice.
	for strings.HasPrefix(slug, AccountPrefix) {
		slug = strings.TrimLeft(strings.TrimPrefix(slug, AccountPrefix), "-")
	}
	if slug == strings.TrimSuffix(AccountPrefix, "-") {
		slug = ""
	}
	if slug == "" {
		slug = "runner"
	}
	base := AccountPrefix + slug
	if len(base) <= MaxAccountLen && (taken == nil || !taken(base)) {
		return base
	}
	for salt := 0; ; salt++ {
		h := hash4(runnerName, salt)
		keep := MaxAccountLen - len(AccountPrefix) - 1 - len(h)
		short := slug
		if len(short) > keep {
			short = strings.TrimRight(short[:keep], "-")
		}
		name := AccountPrefix + short + "-" + h
		if taken == nil || !taken(name) {
			return name
		}
	}
}
