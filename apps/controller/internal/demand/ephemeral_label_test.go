package demand

import "testing"

func TestMatchAcceptsEphemeralLabel(t *testing.T) { // github-integration R5.60
	got, why := Match([]string{"self-hosted", "linux", "ephemeral"}, "x64", "")
	if why != "" {
		t.Fatalf("refused: %q", why)
	}
	found := false
	for _, l := range got {
		found = found || l == "ephemeral"
	}
	if !found {
		t.Fatalf("the runner must carry the label: %v", got)
	}
}
