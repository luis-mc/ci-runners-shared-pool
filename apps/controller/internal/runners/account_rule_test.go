package runners

import (
	"strings"
	"testing"
)

// CRSP234: the account name rule, with long machine names in mind.
func TestAccountNameRule(t *testing.T) {
	seen := map[string]string{}
	for _, n := range []string{
		"gameroompc-pool-1-r1378816711", "gameroompc-pool-1-r1378816712", "gameroompc-pool-1-r1378816713",
		"a-very-long-machine-name-pool-1-r42", "box-1", "CRSP-box", "",
	} {
		got := AccountName(n, nil)
		if len(got) > MaxAccountLen || !strings.HasPrefix(got, AccountPrefix) {
			t.Errorf("%q -> %q: too long or no prefix", n, got)
		}
		if AccountName(n, nil) != got {
			t.Errorf("%q not stable", n)
		}
		if other, dup := seen[got]; dup {
			t.Errorf("%q and %q share %q", n, other, got)
		}
		seen[got] = n
	}
}
