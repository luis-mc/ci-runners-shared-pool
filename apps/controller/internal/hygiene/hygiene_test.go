package hygiene

import "testing"

func TestKeepDefaultsToOnAndReadsTheFile(t *testing.T) {
	for text, want := range map[string]bool{"": true, "junk": true, "CRSP_KEEP_DEPENDENCIES=1\n": true, "CRSP_KEEP_DEPENDENCIES=0\r\n": false, "CRSP_KEEP_DEPENDENCIES=off": false} {
		if ParseKeep(text) != want {
			t.Errorf("%q: want %v", text, want)
		}
	}
	if ParseKeep(SettingsText(false)) || !ParseKeep(SettingsText(true)) {
		t.Error("the text does not read back")
	}
}
